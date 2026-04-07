package cmd

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func runDoctor() {
	fmt.Println("=== pigeon-claw doctor ===")
	fmt.Println()
	passed := 0
	failed := 0

	// 1. Config
	fmt.Println("[Config]")
	home, _ := os.UserHomeDir()
	configPath := filepath.Join(home, ".pigeon-claw", "config")
	if _, err := os.Stat(configPath); err == nil {
		fmt.Printf("  ✓ Config found: %s\n", configPath)
		passed++

		data, _ := os.ReadFile(configPath)
		lines := strings.Split(string(data), "\n")
		hasToken := false
		hasPriority := false
		for _, l := range lines {
			if strings.HasPrefix(l, "DISCORD_TOKEN=") && len(l) > 15 {
				hasToken = true
			}
			if strings.HasPrefix(l, "PROVIDER_PRIORITY=") {
				hasPriority = true
			}
		}
		if hasToken {
			fmt.Println("  ✓ DISCORD_TOKEN is set")
			passed++
		} else {
			fmt.Println("  ✗ DISCORD_TOKEN is missing or empty")
			failed++
		}
		if hasPriority {
			fmt.Println("  ✓ PROVIDER_PRIORITY is set")
			passed++
		} else {
			fmt.Println("  ⚠ PROVIDER_PRIORITY not set (using default)")
		}
	} else {
		fmt.Println("  ✗ Config not found. Run: pigeon-claw init")
		failed++
	}
	fmt.Println()

	// 2. Custom prompt
	fmt.Println("[Prompt]")
	promptPath := filepath.Join(home, ".pigeon-claw", "prompt.md")
	if info, err := os.Stat(promptPath); err == nil {
		fmt.Printf("  ✓ Custom prompt: %s (%d bytes)\n", promptPath, info.Size())
		passed++
	} else {
		fmt.Println("  - No custom prompt (using built-in default)")
	}
	fmt.Println()

	// 3. Sessions
	fmt.Println("[Sessions]")
	sessDir := filepath.Join(home, ".pigeon-claw", "sessions")
	if entries, err := os.ReadDir(sessDir); err == nil {
		count := 0
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".json") {
				count++
			}
		}
		fmt.Printf("  ✓ Session directory: %s (%d channels)\n", sessDir, count)
		passed++
	} else {
		fmt.Println("  - No sessions yet")
	}
	fmt.Println()

	// 4. Claude CLI
	fmt.Println("[Claude CLI]")
	claudePath := findClaude()
	if claudePath != "" {
		fmt.Printf("  ✓ Binary: %s\n", claudePath)
		passed++
		out, err := exec.Command(claudePath, "--version").CombinedOutput()
		if err == nil {
			fmt.Printf("  ✓ Version: %s\n", strings.TrimSpace(string(out)))
		}
	} else {
		fmt.Println("  ✗ Not installed. Run: npm install -g @anthropic-ai/claude-code")
		failed++
	}
	fmt.Println()

	// 5. Ollama
	fmt.Println("[Ollama]")
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://localhost:11434/api/tags")
	if err == nil {
		resp.Body.Close()
		fmt.Println("  ✓ Ollama is running (localhost:11434)")
		passed++
		out, _ := exec.Command("ollama", "ps").CombinedOutput()
		if len(strings.TrimSpace(string(out))) > 0 {
			fmt.Printf("  ✓ Loaded models:\n")
			for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				fmt.Printf("    %s\n", line)
			}
		}
	} else {
		fmt.Println("  - Ollama not running (optional)")
	}
	fmt.Println()

	// 6. macOS Permissions
	fmt.Println("[macOS Permissions]")
	if testScreenRecording() {
		fmt.Println("  ✓ Screen Recording")
		passed++
	} else {
		fmt.Println("  ✗ Screen Recording — grant in System Settings > Privacy")
		failed++
	}
	if testAccessibility() {
		fmt.Println("  ✓ Accessibility")
		passed++
	} else {
		fmt.Println("  ✗ Accessibility — grant in System Settings > Privacy")
		failed++
	}
	fmt.Println()

	// 7. Daemon
	fmt.Println("[Daemon]")
	plistPath := filepath.Join(home, "Library", "LaunchAgents", "com.pigeon-claw.plist")
	if _, err := os.Stat(plistPath); err == nil {
		fmt.Printf("  ✓ LaunchAgent installed: %s\n", plistPath)
		passed++
	} else {
		fmt.Println("  - LaunchAgent not installed (optional, use 'pigeon-claw start')")
	}
	out, _ := exec.Command("launchctl", "list").CombinedOutput()
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "pigeon-claw") {
			parts := strings.Fields(line)
			if len(parts) >= 1 && parts[0] != "-" {
				fmt.Printf("  ✓ Running (PID %s)\n", parts[0])
				passed++
			}
			break
		}
	}
	fmt.Println()

	// 8. Network
	fmt.Println("[Network]")
	pmOut, _ := exec.Command("pmset", "-g").CombinedOutput()
	pmStr := string(pmOut)
	if strings.Contains(pmStr, "sleep\t\t0") || strings.Contains(pmStr, "sleep                0") {
		fmt.Println("  ✓ Sleep disabled")
		passed++
	} else {
		fmt.Println("  ⚠ Sleep is enabled — run: sudo pmset -a sleep 0")
	}
	fmt.Println()

	// Summary
	fmt.Println("---")
	fmt.Printf("✓ %d passed", passed)
	if failed > 0 {
		fmt.Printf("  ✗ %d failed", failed)
	}
	fmt.Println()
}
