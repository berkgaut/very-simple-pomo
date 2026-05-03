package main

import (
	"fmt"
	"os"
	"time"
)

func writeJournal(start time.Time, elapsed time.Duration, status, comment string) {
	if err := os.MkdirAll(pomoDir(), 0700); err != nil {
		return
	}
	f, err := os.OpenFile(journalPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()

	mins := int(elapsed.Minutes())
	secs := int(elapsed.Seconds()) % 60

	entry := fmt.Sprintf("%s %02d:%02d %s", start.Format("2006-01-02T15:04:05"), mins, secs, status)
	if comment != "" {
		entry += " " + comment
	}
	fmt.Fprintln(f, entry)
}
