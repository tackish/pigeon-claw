package discord

import (
	"fmt"
	"os"
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

// handleStopRecording stops OBS recording and moves the file to the target
// directory. With no arg, moves to obsTargetBaseDir directly. With an arg,
// moves to obsTargetBaseDir/{arg}/ (creating the folder if needed).
func (h *Handler) handleStopRecording(s *discordgo.Session, channelID, folderArg string) {
	outputPath, err := obsStopRecording()
	if err != nil {
		s.ChannelMessageSend(channelID, fmt.Sprintf("-# ❌ OBS 녹화 중지 실패: %s", err))
		return
	}
	s.ChannelMessageSend(channelID, "-# ⏹ 녹화 중지, 파일 저장 대기...")

	// Prefer outputPath from OBS response; fall back to scanning the dir.
	var latestPath string
	if outputPath != "" {
		// Wait until file stops growing
		for i := 0; i < 10; i++ {
			time.Sleep(500 * time.Millisecond)
			info, err := os.Stat(outputPath)
			if err != nil {
				continue
			}
			if time.Since(info.ModTime()) > 500*time.Millisecond {
				latestPath = outputPath
				break
			}
		}
	}
	if latestPath == "" {
		sourceDir := getOBSSourceDir()
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
			if time.Since(info.ModTime()) > 500*time.Millisecond {
				latestPath = p
				break
			}
		}
	}

	if latestPath == "" {
		s.ChannelMessageSend(channelID, "-# ❌ 녹화 파일을 찾을 수 없습니다")
		return
	}

	// Check if a custom name was set via !recording <name>
	var customName string
	if v, ok := h.recordingNames.LoadAndDelete(channelID); ok {
		customName = v.(string)
	}

	// Case 1: no folder, no custom name — file stays in place
	if strings.TrimSpace(folderArg) == "" && customName == "" {
		s.ChannelMessageSend(channelID, fmt.Sprintf("-# ✅ 녹화 완료: `%s`", latestPath))
		return
	}

	h.moveRecording(s, channelID, latestPath, folderArg, customName)
}

// moveRecording moves and/or renames the recording file.
// - folder: subfolder under OBS recording dir (empty = root)
// - customName: new base name without extension (empty = keep original)
func (h *Handler) moveRecording(s *discordgo.Session, channelID, filePath, folder, customName string) {
	// Sanitize folder name (no path traversal, no absolute paths)
	folder = strings.TrimSpace(folder)
	folder = strings.ReplaceAll(folder, "..", "")
	folder = strings.TrimPrefix(folder, "/")

	// Sanitize custom name (no path separators)
	customName = strings.TrimSpace(customName)
	customName = strings.ReplaceAll(customName, "/", "_")
	customName = strings.ReplaceAll(customName, "..", "")

	baseDir := getOBSSourceDir()
	targetDir := baseDir
	if folder != "" {
		targetDir = filepath.Join(baseDir, folder)
	}

	if err := os.MkdirAll(targetDir, 0755); err != nil {
		s.ChannelMessageSend(channelID, fmt.Sprintf("-# ❌ 폴더 생성 실패: %s", err))
		return
	}

	// Determine target filename
	ext := filepath.Ext(filePath)
	var targetName string
	if customName != "" {
		targetName = customName + ext
	} else {
		targetName = filepath.Base(filePath)
	}
	targetPath := filepath.Join(targetDir, targetName)

	if err := os.Rename(filePath, targetPath); err != nil {
		s.ChannelMessageSend(channelID, fmt.Sprintf("-# ❌ 파일 이동 실패: %s", err))
		return
	}

	s.ChannelMessageSend(channelID, fmt.Sprintf("-# 📁 저장 완료: `%s`", targetPath))
}
