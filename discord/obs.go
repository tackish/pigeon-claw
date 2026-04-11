package discord

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

// getOBSSourceDir reads OBS config to find the configured recording directory.
// Both the source (where OBS writes) and target base (where we move files to)
// use this same path. Falls back to ~/Movies if not found.
func getOBSSourceDir() string {
	home, _ := os.UserHomeDir()
	profilesDir := filepath.Join(home, "Library", "Application Support", "obs-studio", "basic", "profiles")

	entries, err := os.ReadDir(profilesDir)
	if err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			iniPath := filepath.Join(profilesDir, e.Name(), "basic.ini")
			data, err := os.ReadFile(iniPath)
			if err != nil {
				continue
			}
			for _, line := range strings.Split(string(data), "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "FilePath=") {
					return strings.TrimPrefix(line, "FilePath=")
				}
			}
		}
	}

	return filepath.Join(home, "Movies")
}

// findLatestRecording returns the most recently modified video file in dir.
func findLatestRecording(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}

	videoExt := map[string]bool{
		".mkv": true, ".mp4": true, ".mov": true, ".flv": true, ".ts": true, ".m4v": true,
	}

	var latestPath string
	var latestTime int64

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if !videoExt[ext] {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		mt := info.ModTime().Unix()
		if mt > latestTime {
			latestTime = mt
			latestPath = filepath.Join(dir, e.Name())
		}
	}

	if latestPath == "" {
		return "", fmt.Errorf("no recording files in %s", dir)
	}
	return latestPath, nil
}

// obsClickButton clicks a button in OBS main window via AppleScript.
// Activates OBS first and searches all windows for one containing the button,
// so it works even if OBS was minimized or in another space.
func obsClickButton(buttonName string) error {
	script := fmt.Sprintf(`
tell application "OBS" to activate
delay 0.2
tell application "System Events"
	tell process "OBS"
		set frontmost to true
		repeat with w in windows
			try
				click button "%s" of w
				return "ok"
			end try
		end repeat
		error "button '%s' not found in any OBS window"
	end tell
end tell`, buttonName, buttonName)
	cmd := exec.Command("osascript", "-e", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// handleStopRecording stops OBS recording and moves the file to the target
// directory. With no arg, moves to obsTargetBaseDir directly. With an arg,
// moves to obsTargetBaseDir/{arg}/ (creating the folder if needed).
func (h *Handler) handleStopRecording(s *discordgo.Session, channelID, folderArg string) {
	if err := obsClickButton("Stop Recording"); err != nil {
		s.ChannelMessageSend(channelID, fmt.Sprintf("-# ❌ OBS 녹화 중지 실패: %s", err))
		return
	}
	s.ChannelMessageSend(channelID, "-# ⏹ 녹화 중지, 파일 저장 대기...")

	// Wait for OBS to finalize the file
	sourceDir := getOBSSourceDir()
	var latestPath string
	for i := 0; i < 10; i++ {
		time.Sleep(500 * time.Millisecond)
		p, err := findLatestRecording(sourceDir)
		if err != nil {
			continue
		}
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		// File is ready if it hasn't been modified in the last 500ms
		if time.Since(info.ModTime()) > 500*time.Millisecond {
			latestPath = p
			break
		}
	}

	if latestPath == "" {
		s.ChannelMessageSend(channelID, fmt.Sprintf("-# ❌ 녹화 파일을 찾을 수 없습니다 (%s)", sourceDir))
		return
	}

	// No folder arg: file is already in the right place
	if strings.TrimSpace(folderArg) == "" {
		s.ChannelMessageSend(channelID, fmt.Sprintf("-# ✅ 녹화 완료: `%s`", latestPath))
		return
	}

	h.moveRecording(s, channelID, latestPath, folderArg)
}

// moveRecording moves the file into a subfolder of the OBS recording dir.
func (h *Handler) moveRecording(s *discordgo.Session, channelID, filePath, folder string) {
	// Sanitize folder name (no path traversal, no absolute paths)
	folder = strings.TrimSpace(folder)
	folder = strings.ReplaceAll(folder, "..", "")
	folder = strings.TrimPrefix(folder, "/")

	baseDir := getOBSSourceDir()
	targetDir := baseDir
	if folder != "" {
		targetDir = filepath.Join(baseDir, folder)
	}

	if err := os.MkdirAll(targetDir, 0755); err != nil {
		s.ChannelMessageSend(channelID, fmt.Sprintf("-# ❌ 폴더 생성 실패: %s", err))
		return
	}

	targetPath := filepath.Join(targetDir, filepath.Base(filePath))
	if err := os.Rename(filePath, targetPath); err != nil {
		s.ChannelMessageSend(channelID, fmt.Sprintf("-# ❌ 파일 이동 실패: %s", err))
		return
	}

	s.ChannelMessageSend(channelID, fmt.Sprintf("-# 📁 이동 완료: `%s`", targetPath))
}
