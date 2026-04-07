package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

const plistName = "com.pigeon-claw"

func runDaemon(action string) {
	home, _ := os.UserHomeDir()
	plistPath := filepath.Join(home, "Library", "LaunchAgents", plistName+".plist")
	logPath := filepath.Join(home, ".pigeon-claw", "stderr.log")

	switch action {
	case "start":
		daemonStart(plistPath)
	case "stop":
		daemonStop(plistPath)
	case "restart":
		daemonStop(plistPath)
		daemonStart(plistPath)
	case "reload":
		daemonReload()
	case "status":
		daemonStatus()
	case "logs":
		daemonLogs(logPath)
	}
}

func daemonStart(plistPath string) {
	if _, err := os.Stat(plistPath); os.IsNotExist(err) {
		// Auto-generate plist
		binaryPath, _ := os.Executable()
		if binaryPath == "" {
			binaryPath, _ = exec.LookPath("pigeon-claw")
		}
		if binaryPath == "" {
			fmt.Fprintf(os.Stderr, "cannot find pigeon-claw binary path\n")
			os.Exit(1)
		}

		home, _ := os.UserHomeDir()
		plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>%s</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
        <string>serve</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>%s/.pigeon-claw/stdout.log</string>
    <key>StandardErrorPath</key>
    <string>%s/.pigeon-claw/stderr.log</string>
    <key>EnvironmentVariables</key>
    <dict>
        <key>HOME</key>
        <string>%s</string>
        <key>PATH</key>
        <string>/usr/local/bin:/usr/bin:/bin:/opt/homebrew/bin:%s/.local/bin</string>
    </dict>
</dict>
</plist>`, plistName, binaryPath, home, home, home, home)

		os.MkdirAll(filepath.Dir(plistPath), 0755)
		if err := os.WriteFile(plistPath, []byte(plist), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "failed to create plist: %s\n", err)
			os.Exit(1)
		}
		fmt.Printf("Created %s\n", plistPath)
	}

	out, err := exec.Command("launchctl", "load", plistPath).CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to start: %s %s\n", err, string(out))
		os.Exit(1)
	}
	fmt.Println("pigeon-claw started")
	daemonStatus()
}

func daemonStop(plistPath string) {
	if _, err := os.Stat(plistPath); os.IsNotExist(err) {
		fmt.Println("not installed (no plist found)")
		return
	}

	out, err := exec.Command("launchctl", "unload", plistPath).CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to stop: %s %s\n", err, string(out))
		return
	}
	fmt.Println("pigeon-claw stopped")
}

func daemonReload() {
	pid := findPID()
	if pid == 0 {
		fmt.Println("pigeon-claw is not running")
		return
	}
	if err := syscall.Kill(pid, syscall.SIGHUP); err != nil {
		fmt.Fprintf(os.Stderr, "failed to reload: %s\n", err)
		os.Exit(1)
	}
	fmt.Printf("config reloaded (PID %d)\n", pid)
}

func daemonStatus() {
	out, _ := exec.Command("launchctl", "list").CombinedOutput()
	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		if strings.Contains(line, plistName) {
			parts := strings.Fields(line)
			if len(parts) >= 3 {
				pid := parts[0]
				status := parts[1]
				fmt.Printf("Status: running (PID %s", pid)
				if status != "0" && status != "-" {
					fmt.Printf(", last exit: %s", status)
				}
				fmt.Println(")")
			}
			return
		}
	}
	fmt.Println("Status: not running")
}

func daemonLogs(logPath string) {
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "log file not found: %s\n", logPath)
		os.Exit(1)
	}
	cmd := exec.Command("tail", "-f", logPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	fmt.Printf("Following %s (Ctrl+C to stop)\n\n", logPath)
	cmd.Run()
}

func findPID() int {
	out, _ := exec.Command("launchctl", "list").CombinedOutput()
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, plistName) {
			parts := strings.Fields(line)
			if len(parts) >= 1 {
				pid, err := strconv.Atoi(parts[0])
				if err == nil {
					return pid
				}
			}
		}
	}
	return 0
}
