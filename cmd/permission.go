package cmd

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

type permCheck struct {
	name     string
	urlParam string
	testFn   func() bool
}

func runPermission() {
	if runtime.GOOS != "darwin" {
		fmt.Println("permission command is only available on macOS")
		os.Exit(1)
	}

	reader := bufio.NewReader(os.Stdin)

	// 1. Claude CLI check
	fmt.Println("=== pigeon-claw Setup ===")
	fmt.Println()
	fmt.Println("[1/2] Claude CLI")
	checkClaudeCLI(reader)

	// 2. macOS permissions
	fmt.Println("[2/2] macOS Permissions")
	fmt.Println()
	checkMacPermissions(reader)
}

func checkClaudeCLI(reader *bufio.Reader) {
	fmt.Println()

	// Check if claude binary exists
	claudePath := findClaude()
	if claudePath == "" {
		fmt.Println("  ✗ Claude CLI not found")
		fmt.Println()
		fmt.Println("  Install:")
		fmt.Println("    npm install -g @anthropic-ai/claude-code")
		fmt.Println()
		fmt.Println("  Or:")
		fmt.Println("    brew install claude")
		fmt.Println()
		fmt.Print("  Install now and press Enter to retry: ")
		reader.ReadString('\n')

		claudePath = findClaude()
		if claudePath == "" {
			fmt.Println("  ✗ Still not found. Skipping Claude CLI setup.")
			fmt.Println()
			return
		}
	}
	fmt.Printf("  ✓ Found: %s\n", claudePath)

	// Check version
	cmd := exec.Command(claudePath, "--version")
	out, err := cmd.CombinedOutput()
	if err == nil {
		fmt.Printf("  ✓ Version: %s\n", strings.TrimSpace(string(out)))
	}

	// Check login status
	fmt.Println()
	fmt.Println("  Checking login status...")
	cmd = exec.Command(claudePath, "-p", "respond with only: ok", "--max-turns", "1")
	out, err = cmd.CombinedOutput()
	output := strings.TrimSpace(string(out))

	if err != nil || strings.Contains(strings.ToLower(output), "auth") || strings.Contains(strings.ToLower(output), "login") || strings.Contains(strings.ToLower(output), "api key") {
		fmt.Println("  ✗ Not logged in")
		fmt.Println()
		fmt.Println("  Run this in a separate terminal:")
		fmt.Println("    claude login")
		fmt.Println()
		fmt.Print("  Login complete? Press Enter to verify: ")
		reader.ReadString('\n')

		cmd = exec.Command(claudePath, "-p", "respond with only: ok", "--max-turns", "1")
		out, _ = cmd.CombinedOutput()
		output = strings.TrimSpace(string(out))
		if strings.Contains(strings.ToLower(output), "ok") {
			fmt.Println("  ✓ Login verified")
		} else {
			fmt.Println("  ⚠ Could not verify login. You can try again later.")
		}
	} else {
		fmt.Println("  ✓ Logged in")
	}
	fmt.Println()
}

func findClaude() string {
	home, _ := os.UserHomeDir()
	paths := []string{
		home + "/.local/bin/claude",
		"/usr/local/bin/claude",
		"/opt/homebrew/bin/claude",
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	// Try PATH
	if p, err := exec.LookPath("claude"); err == nil {
		return p
	}
	return ""
}

func checkMacPermissions(reader *bufio.Reader) {
	checks := []permCheck{
		{
			name:     "Full Disk Access",
			urlParam: "Privacy_AllFiles",
			testFn:   testFullDiskAccess,
		},
		{
			name:     "Accessibility",
			urlParam: "Privacy_Accessibility",
			testFn:   testAccessibility,
		},
		{
			name:     "Screen Recording",
			urlParam: "Privacy_ScreenCapture",
			testFn:   testScreenRecording,
		},
		{
			name:     "Automation",
			urlParam: "Privacy_Automation",
			testFn:   testAutomation,
		},
	}

	allPassed := true

	for _, check := range checks {
		fmt.Printf("  [%s] Checking...\n", check.name)

		if check.testFn() {
			fmt.Printf("    ✓ Granted\n")
			continue
		}

		fmt.Printf("    ✗ Not granted. Opening System Settings...\n")
		openPrivacyPane(check.urlParam)
		fmt.Printf("    Add this binary to the list, then press Enter: ")
		reader.ReadString('\n')

		if check.testFn() {
			fmt.Printf("    ✓ Granted\n")
		} else {
			fmt.Printf("    ✗ Still not granted. You may need to restart the binary.\n")
			allPassed = false
		}
	}

	fmt.Println()
	if allPassed {
		fmt.Println("✓ All checks passed. Ready to run!")
		fmt.Println()
		fmt.Println("  Start the bot:")
		fmt.Println("    pigeon-claw serve")
	} else {
		fmt.Println("⚠ Some permissions are missing. The bot may not function fully.")
		fmt.Println("  Re-run 'pigeon-claw permission' after granting them.")
	}
}

func openPrivacyPane(param string) {
	url := fmt.Sprintf("x-apple.systempreferences:com.apple.preference.security?%s", param)
	exec.Command("open", url).Run()
}

func testFullDiskAccess() bool {
	_, err := os.ReadFile("/Library/Application Support/com.apple.TCC/TCC.db")
	return err == nil
}

func testAccessibility() bool {
	cmd := exec.Command("osascript", "-e", `tell application "System Events" to get name of first process`)
	out, err := cmd.CombinedOutput()
	return err == nil && len(strings.TrimSpace(string(out))) > 0
}

func testScreenRecording() bool {
	tmpFile := os.TempDir() + "/pigeon-claw-test.png"
	defer os.Remove(tmpFile)
	cmd := exec.Command("screencapture", "-x", "-C", tmpFile)
	err := cmd.Run()
	if err != nil {
		return false
	}
	info, err := os.Stat(tmpFile)
	return err == nil && info.Size() > 0
}

func testAutomation() bool {
	cmd := exec.Command("osascript", "-e", `tell application "Finder" to get name of front window`)
	err := cmd.Run()
	return err == nil || !strings.Contains(err.Error(), "not allowed")
}
