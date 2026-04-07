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
	"strings"
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

	if latest != current && latest > current {
		slog.Warn("new version available", "current", current, "latest", latest)
		fmt.Printf("\n  ⬆ New version available: %s → %s\n", current, latest)
		fmt.Println()

		// Ask user with 10-second timeout (auto-skip for daemon mode)
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

	// Re-exec the new binary
	exe, err := os.Executable()
	if err != nil {
		fmt.Printf("  ✗ Cannot find binary: %s\n", err)
		fmt.Println("  Please restart manually: pigeon-claw serve")
		return
	}
	execErr := exec.Command(exe, "serve").Start()
	if execErr != nil {
		fmt.Printf("  ✗ Restart failed: %s\n", execErr)
		fmt.Println("  Please restart manually: pigeon-claw serve")
		return
	}
	os.Exit(0)
}
