package cmd

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// acquireLock creates a PID lock file. Returns error if another instance is running.
func acquireLock(path string) error {
	data, err := os.ReadFile(path)
	if err == nil {
		pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err == nil && pid > 0 {
			// Check if process is still alive
			if err := syscall.Kill(pid, 0); err == nil {
				return fmt.Errorf("another instance is running (PID %d). Stop it first or delete %s", pid, path)
			}
		}
		// Stale lock file — process is dead
	}

	return os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0644)
}

func releaseLock(path string) {
	os.Remove(path)
}
