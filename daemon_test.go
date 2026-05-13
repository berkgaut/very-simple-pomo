package main

import (
	"bufio"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// newTestState constructs a timerState suitable for unit tests:
// a long-running timer (so it never fires during the test) with a
// fixed clock anchored at t0.
func newTestState(t0 time.Time, duration time.Duration) *timerState {
	return &timerState{
		timer:    time.NewTimer(duration),
		deadline: t0.Add(duration),
		now:      func() time.Time { return t0 },
	}
}

// sendRecv sends a command over client conn and reads one line back.
func sendRecv(t *testing.T, conn net.Conn, cmd string) string {
	t.Helper()
	fmt.Fprintf(conn, "%s\n", cmd)
	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		t.Fatalf("no response to %q", cmd)
	}
	return scanner.Text()
}

// runCommand calls handleCommand synchronously using net.Pipe().
// Returns the response line and the bool returned by handleCommand.
func runCommand(t *testing.T, s *timerState, signal func(string), cmd string) (string, bool) {
	t.Helper()
	server, client := net.Pipe()
	defer client.Close()

	fields := strings.Fields(cmd)

	var (
		resp  string
		close bool
		wg    sync.WaitGroup
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer server.Close()
		close = handleCommand(server, fields, s, signal)
	}()

	// Read one response line from the client side.
	scanner := bufio.NewScanner(client)
	if scanner.Scan() {
		resp = scanner.Text()
	}
	wg.Wait()
	return resp, close
}

// noSignal is a signal func that fails the test if called.
func noSignal(t *testing.T) func(string) {
	return func(reason string) {
		t.Errorf("unexpected signal: %q", reason)
	}
}

// captureSignal returns a signal func and a pointer to the captured reason.
func captureSignal() (func(string), *string) {
	var reason string
	var once sync.Once
	return func(r string) {
		once.Do(func() { reason = r })
	}, &reason
}

// ---------------------------------------------------------------------------
// REMAINING
// ---------------------------------------------------------------------------

func TestRemaining_Running(t *testing.T) {
	t0 := time.Now()
	s := newTestState(t0, 15*time.Minute)
	defer s.timer.Stop()

	resp, closed := runCommand(t, s, noSignal(t), "REMAINING")
	if closed {
		t.Fatal("expected connection to stay open")
	}
	if resp != "15:00" {
		t.Errorf("got %q, want %q", resp, "15:00")
	}
}

func TestRemaining_RunningPartial(t *testing.T) {
	t0 := time.Now()
	s := newTestState(t0, 15*time.Minute)
	defer s.timer.Stop()
	// Advance clock by 3 minutes 7 seconds.
	elapsed := 3*time.Minute + 7*time.Second
	s.now = func() time.Time { return t0.Add(elapsed) }

	resp, _ := runCommand(t, s, noSignal(t), "REMAINING")
	if resp != "11:53" {
		t.Errorf("got %q, want %q", resp, "11:53")
	}
}

func TestRemaining_Paused(t *testing.T) {
	t0 := time.Now()
	s := newTestState(t0, 15*time.Minute)
	defer s.timer.Stop()

	// Pause at t0 + 5 minutes.
	s.now = func() time.Time { return t0.Add(5 * time.Minute) }
	resp, _ := runCommand(t, s, noSignal(t), "PAUSE")
	if resp != "OK" {
		t.Fatalf("PAUSE: got %q, want OK", resp)
	}

	// Advance clock further — remaining should be frozen at 10:00.
	s.now = func() time.Time { return t0.Add(20 * time.Minute) }
	resp, _ = runCommand(t, s, noSignal(t), "REMAINING")
	if resp != "P10:00" {
		t.Errorf("got %q, want %q", resp, "P10:00")
	}
}

// ---------------------------------------------------------------------------
// STOP
// ---------------------------------------------------------------------------

func TestStop(t *testing.T) {
	t0 := time.Now()
	s := newTestState(t0, 15*time.Minute)
	defer s.timer.Stop()

	sig, reason := captureSignal()
	resp, closed := runCommand(t, s, sig, "STOP")

	if resp != "OK" {
		t.Errorf("got %q, want OK", resp)
	}
	if !closed {
		t.Error("expected connection to be closed")
	}
	if *reason != "CANCELED" {
		t.Errorf("signal reason: got %q, want CANCELED", *reason)
	}
}

// ---------------------------------------------------------------------------
// PAUSE
// ---------------------------------------------------------------------------

func TestPause_WhenRunning(t *testing.T) {
	t0 := time.Now()
	s := newTestState(t0, 15*time.Minute)
	defer s.timer.Stop()

	resp, closed := runCommand(t, s, noSignal(t), "PAUSE")
	if resp != "OK" {
		t.Errorf("got %q, want OK", resp)
	}
	if closed {
		t.Error("expected connection to stay open")
	}
	if !s.paused {
		t.Error("expected paused == true")
	}
	if s.remaining != 15*time.Minute {
		t.Errorf("remaining: got %v, want 15m", s.remaining)
	}
}

func TestPause_WhenAlreadyPaused(t *testing.T) {
	t0 := time.Now()
	s := newTestState(t0, 15*time.Minute)
	defer s.timer.Stop()

	runCommand(t, s, noSignal(t), "PAUSE")
	resp, closed := runCommand(t, s, noSignal(t), "PAUSE")
	if resp != "ERROR" {
		t.Errorf("got %q, want ERROR", resp)
	}
	if !closed {
		t.Error("expected connection to be closed after ERROR")
	}
}

// ---------------------------------------------------------------------------
// CONT
// ---------------------------------------------------------------------------

func TestCont_WhenPaused(t *testing.T) {
	t0 := time.Now()
	s := newTestState(t0, 15*time.Minute)
	defer s.timer.Stop()

	// Pause after 5 minutes.
	s.now = func() time.Time { return t0.Add(5 * time.Minute) }
	runCommand(t, s, noSignal(t), "PAUSE")

	// Resume at t0 + 8 minutes (3 minutes later).
	resumeAt := t0.Add(8 * time.Minute)
	s.now = func() time.Time { return resumeAt }
	resp, closed := runCommand(t, s, noSignal(t), "CONT")
	if resp != "OK" {
		t.Errorf("CONT: got %q, want OK", resp)
	}
	if closed {
		t.Error("expected connection to stay open")
	}
	if s.paused {
		t.Error("expected paused == false after CONT")
	}
	// New deadline should be resumeAt + 10m (the remaining at pause time).
	wantDeadline := resumeAt.Add(10 * time.Minute)
	if !s.deadline.Equal(wantDeadline) {
		t.Errorf("deadline: got %v, want %v", s.deadline, wantDeadline)
	}
}

func TestCont_WhenNotPaused(t *testing.T) {
	t0 := time.Now()
	s := newTestState(t0, 15*time.Minute)
	defer s.timer.Stop()

	resp, closed := runCommand(t, s, noSignal(t), "CONT")
	if resp != "ERROR" {
		t.Errorf("got %q, want ERROR", resp)
	}
	if !closed {
		t.Error("expected connection to be closed after ERROR")
	}
}

// ---------------------------------------------------------------------------
// ADD
// ---------------------------------------------------------------------------

func TestAdd_WhileRunning(t *testing.T) {
	t0 := time.Now()
	s := newTestState(t0, 15*time.Minute)
	defer s.timer.Stop()

	resp, closed := runCommand(t, s, noSignal(t), "ADD 5")
	if resp != "OK" {
		t.Errorf("got %q, want OK", resp)
	}
	if closed {
		t.Error("expected connection to stay open")
	}
	wantDeadline := t0.Add(20 * time.Minute)
	if !s.deadline.Equal(wantDeadline) {
		t.Errorf("deadline: got %v, want %v", s.deadline, wantDeadline)
	}
}

func TestAdd_WhilePaused(t *testing.T) {
	t0 := time.Now()
	s := newTestState(t0, 15*time.Minute)
	defer s.timer.Stop()

	runCommand(t, s, noSignal(t), "PAUSE")
	resp, _ := runCommand(t, s, noSignal(t), "ADD 5")
	if resp != "OK" {
		t.Errorf("got %q, want OK", resp)
	}
	if s.remaining != 20*time.Minute {
		t.Errorf("remaining: got %v, want 20m", s.remaining)
	}
}

func TestAdd_InvalidArgs(t *testing.T) {
	t0 := time.Now()
	s := newTestState(t0, 15*time.Minute)
	defer s.timer.Stop()

	for _, cmd := range []string{"ADD", "ADD abc", "ADD -1", "ADD 0"} {
		resp, closed := runCommand(t, s, noSignal(t), cmd)
		if resp != "ERROR" {
			t.Errorf("%q: got %q, want ERROR", cmd, resp)
		}
		if !closed {
			t.Errorf("%q: expected connection closed", cmd)
		}
	}
}

// ---------------------------------------------------------------------------
// Unknown command
// ---------------------------------------------------------------------------

func TestUnknownCommand(t *testing.T) {
	t0 := time.Now()
	s := newTestState(t0, 15*time.Minute)
	defer s.timer.Stop()

	// net.Pipe() won't return from Scan until the write side is closed,
	// so we use a real connection pair and close the server side after
	// handleCommand returns.
	server, client := net.Pipe()

	var closed bool
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer server.Close()
		closed = handleCommand(server, strings.Fields("BOGUS"), s, noSignal(t))
	}()

	// Close client write side so the scanner on the client unblocks;
	// since there's no response written for unknown commands, just
	// wait for handleCommand to finish.
	<-done
	client.Close()

	if closed {
		t.Error("unknown command should not close connection")
	}
}

// ---------------------------------------------------------------------------
// Integration test: serveDaemon
// ---------------------------------------------------------------------------

// dialUnix connects to the Unix socket, retrying briefly until available.
func dialUnix(t *testing.T, path string) net.Conn {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("unix", path, time.Second)
		if err == nil {
			return conn
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("could not connect to %s", path)
	return nil
}

// command sends a command and reads the response over a fresh connection.
func command(t *testing.T, sockPath, cmd string) string {
	t.Helper()
	conn := dialUnix(t, sockPath)
	defer conn.Close()
	return sendRecv(t, conn, cmd)
}

func TestServeDaemon_TimerExpiry(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "socket")

	listener, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}

	var (
		journalReason  string
		journalElapsed time.Duration
		journalMu      sync.Mutex
		journalCalled  = make(chan struct{})
	)

	opts := daemonOpts{
		listener: listener,
		writeJournal: func(start time.Time, elapsed time.Duration, reason, comment string) {
			journalMu.Lock()
			journalReason = reason
			journalElapsed = elapsed
			journalMu.Unlock()
			close(journalCalled)
		},
		runNotification: func(cfg Config, comment string, minutes int, elapsed time.Duration) {},
	}

	go serveDaemon(1*time.Minute, "", Config{}, opts) // 1-minute timer; we'll send STOP quickly

	// Send STOP immediately — exercises the CANCELED path without waiting a minute.
	resp := command(t, sock, "STOP")
	if resp != "OK" {
		t.Errorf("STOP: got %q, want OK", resp)
	}

	select {
	case <-journalCalled:
	case <-time.After(2 * time.Second):
		t.Fatal("writeJournal not called within timeout")
	}

	journalMu.Lock()
	defer journalMu.Unlock()
	if journalReason != "CANCELED" {
		t.Errorf("journal reason: got %q, want CANCELED", journalReason)
	}
	if journalElapsed > 2*time.Second {
		t.Errorf("elapsed suspiciously large: %v", journalElapsed)
	}
}

func TestServeDaemon_Complete(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "socket")

	listener, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}

	var (
		journalReason string
		journalMu     sync.Mutex
		journalCalled = make(chan struct{})
		notifyCalled  = make(chan struct{})
	)

	opts := daemonOpts{
		listener: listener,
		writeJournal: func(start time.Time, elapsed time.Duration, reason, comment string) {
			journalMu.Lock()
			journalReason = reason
			journalMu.Unlock()
			close(journalCalled)
		},
		runNotification: func(cfg Config, comment string, minutes int, elapsed time.Duration) {
			close(notifyCalled)
		},
	}

	go serveDaemon(50*time.Millisecond, "test comment", Config{}, opts)

	select {
	case <-journalCalled:
	case <-time.After(2 * time.Second):
		t.Fatal("writeJournal not called within timeout")
	}
	select {
	case <-notifyCalled:
	case <-time.After(2 * time.Second):
		t.Fatal("runNotification not called within timeout")
	}

	journalMu.Lock()
	defer journalMu.Unlock()
	if journalReason != "COMPLETE" {
		t.Errorf("journal reason: got %q, want COMPLETE", journalReason)
	}
}

func TestServeDaemon_PauseCont(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "socket")

	listener, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}

	journalCalled := make(chan struct{})
	opts := daemonOpts{
		listener: listener,
		writeJournal: func(start time.Time, elapsed time.Duration, reason, comment string) {
			close(journalCalled)
		},
		runNotification: func(cfg Config, comment string, minutes int, elapsed time.Duration) {},
	}

	go serveDaemon(1*time.Minute, "", Config{}, opts)

	if resp := command(t, sock, "PAUSE"); resp != "OK" {
		t.Fatalf("PAUSE: got %q, want OK", resp)
	}
	if resp := command(t, sock, "REMAINING"); !strings.HasPrefix(resp, "P") {
		t.Errorf("REMAINING while paused: got %q, want P prefix", resp)
	}
	if resp := command(t, sock, "CONT"); resp != "OK" {
		t.Fatalf("CONT: got %q, want OK", resp)
	}
	if resp := command(t, sock, "REMAINING"); strings.HasPrefix(resp, "P") {
		t.Errorf("REMAINING after cont: got %q, should not have P prefix", resp)
	}

	// Clean up.
	command(t, sock, "STOP")
	select {
	case <-journalCalled:
	case <-time.After(2 * time.Second):
		t.Fatal("writeJournal not called")
	}
}
