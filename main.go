package main

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const exitNoTimer = 127

func pomoDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "pomo: cannot determine home directory:", err)
		os.Exit(1)
	}
	return filepath.Join(home, ".pomo")
}

func socketPath() string {
	return filepath.Join(pomoDir(), "daemon", "socket")
}

func journalPath() string {
	return filepath.Join(pomoDir(), "journal")
}

func configPath() string {
	return filepath.Join(pomoDir(), "config.toml")
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--daemon" {
		runDaemon(os.Args[2:])
		return
	}

	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: pomo <minutes> [comment] | remaining | stop | pause | cont | list | add <minutes>")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "remaining":
		cmdRemaining()
	case "stop":
		cmdStop()
	case "pause":
		cmdPause()
	case "cont":
		cmdCont()
	case "list":
		cmdList()
	case "add":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "usage: pomo add <minutes>")
			os.Exit(1)
		}
		n, err := strconv.Atoi(os.Args[2])
		if err != nil || n <= 0 {
			fmt.Fprintln(os.Stderr, "usage: pomo add <minutes>")
			os.Exit(1)
		}
		cmdAdd(n)
	default:
		minutes, err := strconv.Atoi(os.Args[1])
		if err != nil || minutes <= 0 {
			fmt.Fprintln(os.Stderr, "usage: pomo <minutes> [comment] | remaining | stop | pause | cont | list | add <minutes>")
			os.Exit(1)
		}
		comment := ""
		if len(os.Args) > 2 {
			comment = strings.Join(os.Args[2:], " ")
		}
		cmdStart(minutes, comment)
	}
}

func dial() (net.Conn, error) {
	return net.DialTimeout("unix", socketPath(), 2*time.Second)
}

func isDaemonRunning() bool {
	conn, err := dial()
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func sendCommand(cmd string) (string, error) {
	conn, err := dial()
	if err != nil {
		return "", err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	fmt.Fprintf(conn, "%s\n", cmd)
	buf := make([]byte, 256)
	n, err := conn.Read(buf)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(buf[:n])), nil
}

func cmdRemaining() {
	resp, err := sendCommand("REMAINING")
	if err != nil || strings.HasPrefix(resp, "ERROR") {
		os.Exit(exitNoTimer)
	}
	fmt.Println(resp)
}

func cmdStop() {
	sendCommand("STOP")
}

func cmdPause() {
	resp, err := sendCommand("PAUSE")
	if err != nil || strings.HasPrefix(resp, "ERROR") {
		fmt.Fprintln(os.Stderr, "pomo: no running timer to pause")
		os.Exit(exitNoTimer)
	}
}

func cmdCont() {
	resp, err := sendCommand("CONT")
	if err != nil || strings.HasPrefix(resp, "ERROR") {
		fmt.Fprintln(os.Stderr, "pomo: no paused timer to continue")
		os.Exit(exitNoTimer)
	}
}

func cmdStart(minutes int, comment string) {
	if isDaemonRunning() {
		sendCommand("STOP")
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			time.Sleep(50 * time.Millisecond)
			if !isDaemonRunning() {
				break
			}
		}
	}
	startDaemon(minutes, comment)
}

func cmdAdd(minutes int) {
	resp, err := sendCommand(fmt.Sprintf("ADD %d", minutes))
	if err != nil || strings.HasPrefix(resp, "ERROR") {
		fmt.Fprintln(os.Stderr, "pomo: no timer running")
		os.Exit(exitNoTimer)
	}
}

func cmdList() {
	data, err := os.ReadFile(journalPath())
	if err != nil {
		return
	}
	today := time.Now().Format("2006-01-02")
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, today) {
			fmt.Println(line)
		}
	}
}
