package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

func startDaemon(minutes int, comment string) {
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "pomo: cannot locate executable:", err)
		os.Exit(1)
	}

	args := []string{"--daemon", strconv.Itoa(minutes)}
	if comment != "" {
		args = append(args, comment)
	}

	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		fmt.Fprintln(os.Stderr, "pomo: cannot open /dev/null:", err)
		os.Exit(1)
	}
	defer null.Close()

	cmd := exec.Command(exe, args...)
	cmd.Stdin = null
	cmd.Stdout = null
	cmd.Stderr = null
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "pomo: failed to start daemon:", err)
		os.Exit(1)
	}
	cmd.Process.Release()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		if isDaemonRunning() {
			return
		}
	}
	fmt.Fprintln(os.Stderr, "pomo: daemon did not start in time")
	os.Exit(1)
}

// timerState holds the mutable timer state accessed by command handlers.
// All fields must be accessed with mu held.
type timerState struct {
	mu       sync.Mutex
	timer    *time.Timer
	deadline time.Time         // when the timer fires; valid when not paused
	paused   bool
	remaining time.Duration    // time left at the moment of pause; valid when paused
}

func (s *timerState) remaining_() time.Duration {
	if s.paused {
		return s.remaining
	}
	rem := time.Until(s.deadline)
	if rem < 0 {
		rem = 0
	}
	return rem
}

// handleCommand processes one parsed command line from a client connection.
// Returns true if the connection should be closed after this command.
func handleCommand(c net.Conn, fields []string, s *timerState, signal func(string)) bool {
	s.mu.Lock()

	switch fields[0] {
	case "REMAINING":
		rem := s.remaining_()
		isPaused := s.paused
		s.mu.Unlock()
		mins := int(rem.Minutes())
		secs := int(rem.Seconds()) % 60
		if isPaused {
			fmt.Fprintf(c, "P%02d:%02d\n", mins, secs)
		} else {
			fmt.Fprintf(c, "%02d:%02d\n", mins, secs)
		}

	case "STOP":
		s.mu.Unlock()
		fmt.Fprintf(c, "OK\n")
		signal("CANCELED")
		return true

	case "ADD":
		if len(fields) != 2 {
			s.mu.Unlock()
			fmt.Fprintf(c, "ERROR\n")
			return true
		}
		n, err := strconv.Atoi(fields[1])
		if err != nil || n <= 0 {
			s.mu.Unlock()
			fmt.Fprintf(c, "ERROR\n")
			return true
		}
		extra := time.Duration(n) * time.Minute
		if s.paused {
			s.remaining += extra
		} else {
			if !s.timer.Stop() {
				s.mu.Unlock()
				fmt.Fprintf(c, "ERROR\n")
				return true
			}
			s.deadline = s.deadline.Add(extra)
			s.timer.Reset(time.Until(s.deadline))
		}
		s.mu.Unlock()
		fmt.Fprintf(c, "OK\n")

	case "PAUSE":
		if s.paused {
			s.mu.Unlock()
			fmt.Fprintf(c, "ERROR\n")
			return true
		}
		if !s.timer.Stop() {
			// Timer already fired; too late to pause
			s.mu.Unlock()
			fmt.Fprintf(c, "ERROR\n")
			return true
		}
		s.remaining = time.Until(s.deadline)
		if s.remaining < 0 {
			s.remaining = 0
		}
		s.paused = true
		s.mu.Unlock()
		fmt.Fprintf(c, "OK\n")

	case "CONT":
		if !s.paused {
			s.mu.Unlock()
			fmt.Fprintf(c, "ERROR\n")
			return true
		}
		s.deadline = time.Now().Add(s.remaining)
		s.timer.Reset(s.remaining)
		s.remaining = 0
		s.paused = false
		s.mu.Unlock()
		fmt.Fprintf(c, "OK\n")

	default:
		s.mu.Unlock()
	}

	return false
}

func runDaemon(args []string) {
	if len(args) < 1 {
		os.Exit(1)
	}
	minutes, err := strconv.Atoi(args[0])
	if err != nil || minutes <= 0 {
		os.Exit(1)
	}
	comment := ""
	if len(args) > 1 {
		comment = args[1]
	}

	cfg := loadConfig()

	sockDir := filepath.Join(pomoDir(), "daemon")
	if err := os.MkdirAll(sockDir, 0700); err != nil {
		os.Exit(1)
	}

	sock := socketPath()
	os.Remove(sock)

	listener, err := net.Listen("unix", sock)
	if err != nil {
		os.Exit(1)
	}

	startTime := time.Now()
	duration := time.Duration(minutes) * time.Minute

	s := &timerState{
		timer:    time.NewTimer(duration),
		deadline: startTime.Add(duration),
	}

	doneCh := make(chan string, 1)
	var once sync.Once
	signal := func(reason string) {
		once.Do(func() { doneCh <- reason })
	}

	go func() {
		<-s.timer.C
		signal("COMPLETE")
	}()

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				c.SetDeadline(time.Now().Add(5 * time.Second))
				scanner := bufio.NewScanner(c)
				for scanner.Scan() {
					fields := strings.Fields(scanner.Text())
					if len(fields) == 0 {
						continue
					}
					if handleCommand(c, fields, s, signal) {
						return
					}
				}
			}(conn)
		}
	}()

	reason := <-doneCh
	s.timer.Stop()
	listener.Close()
	os.Remove(sock)

	elapsed := time.Since(startTime)
	writeJournal(startTime, elapsed, reason, comment)

	if reason == "COMPLETE" {
		runNotification(cfg, comment, minutes, elapsed)
	}
}
