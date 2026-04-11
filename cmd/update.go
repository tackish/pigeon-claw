package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func checkUpdate() {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("https://api.github.com/repos/tackish/pigeon-claw/releases/latest")
	if err != nil {
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return
	}

	var release struct {
		TagName string `json:"tag_name"`
	}
	if json.Unmarshal(body, &release) != nil {
		return
	}

	latest := strings.TrimPrefix(release.TagName, "v")
	current := version
	if current == "dev" || current == "" {
		return
	}

	if isNewer(latest, current) {
		slog.Warn("new version available", "current", current, "latest", latest)
		fmt.Printf("\n  ⬆ New version available: %s → %s\n", current, latest)
		fmt.Println()

		// If not running in a terminal (e.g., launchd daemon), skip the prompt
		// and auto-update immediately.
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
}

// isTerminal reports whether the given file is a terminal (tty).
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

// isNewer compares semver strings (e.g., "0.0.10" > "0.0.6")
func isNewer(latest, current string) bool {
	parse := func(v string) [3]int {
		var parts [3]int
		for i, s := range strings.SplitN(v, ".", 3) {
			parts[i], _ = strconv.Atoi(s)
		}
		return parts
	}
	l, c := parse(latest), parse(current)
	if l[0] != c[0] {
		return l[0] > c[0]
	}
	if l[1] != c[1] {
		return l[1] > c[1]
	}
	return l[2] > c[2]
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
	cmd := exec.Command("brew", "update")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Printf("  ✗ brew update failed: %s\n", err)
		return
	}

	cmd = exec.Command("brew", "upgrade", "pigeon-claw")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Printf("  ✗ brew upgrade failed: %s\n", err)
		return
	}

	fmt.Println("  ✓ Updated! Restarting...")
	fmt.Println()

	// Release PID lock before re-exec
	home, _ := os.UserHomeDir()
	os.Remove(home + "/.pigeon-claw/pigeon-claw.pid")

	// Use the symlink path (not resolved) so we always run the upgraded binary.
	exe, err := exec.LookPath("pigeon-claw")
	if err != nil {
		exe, _ = os.Executable()
	}

	// Replace current process with new binary
	syscall.Exec(exe, []string{exe, "serve"}, os.Environ())
}
