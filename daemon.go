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
	timer := time.NewTimer(duration)

	doneCh := make(chan string, 1)
	var once sync.Once
	signal := func(reason string) {
		once.Do(func() { doneCh <- reason })
	}

	go func() {
		<-timer.C
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
					switch strings.TrimSpace(scanner.Text()) {
					case "REMAINING":
						remaining := duration - time.Since(startTime)
						if remaining < 0 {
							remaining = 0
						}
						fmt.Fprintf(c, "%02d:%02d\n", int(remaining.Minutes()), int(remaining.Seconds())%60)
					case "STOP":
						fmt.Fprintf(c, "OK\n")
						signal("CANCELED")
						return
					}
				}
			}(conn)
		}
	}()

	reason := <-doneCh
	timer.Stop()
	listener.Close()
	os.Remove(sock)

	elapsed := time.Since(startTime)
	writeJournal(startTime, elapsed, reason, comment)

	if reason == "COMPLETE" {
		runNotification(cfg, comment, minutes, elapsed)
	}
}
