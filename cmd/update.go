package cmd

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/tackish/pigeon-claw/update"
)

// checkUpdate runs at startup: if a newer release exists, upgrade and
// re-exec into it. The version comparison and brew invocation live in the
// update package so the Discord /update command shares them.
func checkUpdate() {
	update.SetCurrent(version)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	latest, err := update.Latest(ctx)
	cancel()
	if err != nil || !update.IsNewer(latest, version) {
		return
	}

	slog.Warn("new version available", "current", version, "latest", latest)
	fmt.Printf("\n  ⬆ New version available: %s → %s\n\n", version, latest)

	// Non-interactive (launchd daemon): no one is there to answer.
	if !isTerminal(os.Stdin) {
		slog.Info("non-interactive environment, auto-updating")
		runBrewUpdate()
		return
	}

	answer := promptWithTimeout("  Update now? [Y/n] (auto-update in 10s): ", 10*time.Second)
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "", "y", "yes":
		runBrewUpdate()
	default:
		fmt.Println("  Skipped. Run manually: brew update && brew upgrade pigeon-claw")
		fmt.Println()
	}
}

// isTerminal reports whether the given file is a terminal (tty).
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

func promptWithTimeout(prompt string, timeout time.Duration) string {
	fmt.Print(prompt)

	ch := make(chan string, 1)
	go func() {
		reader := bufio.NewReader(os.Stdin)
		line, _ := reader.ReadString('\n')
		ch <- strings.TrimSpace(line)
	}()

	select {
	case answer := <-ch:
		return answer
	case <-time.After(timeout):
		fmt.Println()
		fmt.Println("  No response, auto-updating...")
		return "y"
	}
}

func runBrewUpdate() {
	fmt.Println("  Updating...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	if err := update.Upgrade(ctx); err != nil {
		fmt.Printf("  ✗ %s\n", err)
		return
	}

	fmt.Println("  ✓ Updated! Restarting...")
	fmt.Println()

	// The instance lock is an flock on an O_CLOEXEC descriptor: exec
	// releases it and the new image re-acquires it. Deleting the file here
	// would create a second lockable inode and let a duplicate start.

	// Use the symlink path (not resolved) so we always run the upgraded binary.
	exe, err := exec.LookPath("pigeon-claw")
	if err != nil {
		exe, _ = os.Executable()
	}

	// Replace current process with new binary
	syscall.Exec(exe, []string{exe, "serve"}, os.Environ())
}
