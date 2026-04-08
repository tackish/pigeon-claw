package session

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/tackish/pigeon-claw/provider"
)

type Session struct {
	mu             sync.Mutex
	ChannelID      string             `json:"channel_id"`
	Messages       []provider.Message `json:"messages"`
	ActiveProvider string             `json:"active_provider"`
	CLISessionID   string             `json:"cli_session_id,omitempty"`
	maxMessages    int
	sessionDir     string
}

type Store struct {
	sessions    sync.Map
	maxMessages int
	sessionDir  string
}

func NewStore(maxMessages int, sessionDir string) *Store {
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		slog.Error("failed to create session directory", "path", sessionDir, "error", err)
	}

	s := &Store{
		maxMessages: maxMessages,
		sessionDir:  sessionDir,
	}
	s.loadAll()
	return s
}

func (s *Store) GetOrCreate(channelID string) *Session {
	if v, ok := s.sessions.Load(channelID); ok {
		return v.(*Session)
	}

	// Try loading from disk before creating empty session
	if sess := s.loadFromDisk(channelID); sess != nil {
		actual, _ := s.sessions.LoadOrStore(channelID, sess)
		return actual.(*Session)
	}

	sess := &Session{
		ChannelID:   channelID,
		Messages:    make([]provider.Message, 0),
		maxMessages: s.maxMessages,
		sessionDir:  s.sessionDir,
	}
	actual, _ := s.sessions.LoadOrStore(channelID, sess)
	return actual.(*Session)
}

func (s *Store) loadFromDisk(channelID string) *Session {
	path := filepath.Join(s.sessionDir, fmt.Sprintf("channel_%s.json", channelID))
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var sess Session
	if err := json.Unmarshal(data, &sess); err != nil {
		slog.Warn("failed to parse session file", "channel", channelID, "error", err)
		return nil
	}

	sess.maxMessages = s.maxMessages
	sess.sessionDir = s.sessionDir
	slog.Info("loaded session from disk", "channel", channelID, "messages", len(sess.Messages), "cli_session_id", sess.CLISessionID)
	return &sess
}

func (s *Store) Reset(channelID string) {
	if v, ok := s.sessions.Load(channelID); ok {
		sess := v.(*Session)
		sess.mu.Lock()
		sess.Messages = make([]provider.Message, 0)
		sess.ActiveProvider = ""
		sess.CLISessionID = ""
		sess.mu.Unlock()
		sess.save()
	}
	s.sessions.Delete(channelID)
	os.Remove(filepath.Join(s.sessionDir, fmt.Sprintf("channel_%s.json", channelID)))
}

func (sess *Session) Append(msg provider.Message) {
	sess.mu.Lock()
	sess.Messages = append(sess.Messages, msg)
	sess.truncate()
	sess.mu.Unlock()
	sess.save()
}

func (sess *Session) History() []provider.Message {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	msgs := make([]provider.Message, len(sess.Messages))
	copy(msgs, sess.Messages)
	return msgs
}

func (sess *Session) MessageCount() int {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	return len(sess.Messages)
}

func (sess *Session) GetActiveProvider() string {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	return sess.ActiveProvider
}

func (sess *Session) SetActiveProvider(name string) {
	sess.mu.Lock()
	sess.ActiveProvider = name
	sess.mu.Unlock()
	sess.save()
}

func (sess *Session) GetCLISessionID() string {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	return sess.CLISessionID
}

func (sess *Session) SetCLISessionID(id string) {
	sess.mu.Lock()
	sess.CLISessionID = id
	sess.mu.Unlock()
	sess.save()
}

func (sess *Session) ExportContext() string {
	sess.mu.Lock()
	defer sess.mu.Unlock()

	var sb strings.Builder
	sb.WriteString("## Conversation Context (transferred from previous provider)\n\n")
	sb.WriteString("The following is the conversation history. Continue from where it left off.\n\n")
	sb.WriteString("### Message History\n")

	for _, msg := range sess.Messages {
		switch msg.Role {
		case provider.RoleUser:
			sb.WriteString(fmt.Sprintf("- **User**: %s\n", msg.Content))
		case provider.RoleAssistant:
			sb.WriteString(fmt.Sprintf("- **Assistant**: %s\n", msg.Content))
		case provider.RoleTool:
			sb.WriteString(fmt.Sprintf("- **Tool Result** (call_id: %s): %s\n", msg.ToolCallID, msg.Content))
		}
	}

	sb.WriteString("\nPlease continue this conversation.\n")
	return sb.String()
}

func (sess *Session) truncate() {
	if len(sess.Messages) <= sess.maxMessages {
		return
	}
	excess := len(sess.Messages) - sess.maxMessages
	sess.Messages = sess.Messages[excess:]
}

func (sess *Session) save() {
	sess.mu.Lock()
	data, err := json.MarshalIndent(sess, "", "  ")
	sess.mu.Unlock()

	if err != nil {
		slog.Error("failed to marshal session", "channel", sess.ChannelID, "error", err)
		return
	}

	path := filepath.Join(sess.sessionDir, fmt.Sprintf("channel_%s.json", sess.ChannelID))
	if err := os.WriteFile(path, data, 0644); err != nil {
		slog.Error("failed to save session", "path", path, "error", err)
	}
}

func (s *Store) loadAll() {
	entries, err := os.ReadDir(s.sessionDir)
	if err != nil {
		slog.Warn("failed to read session directory", "path", s.sessionDir, "error", err)
		return
	}

	restored := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "channel_") || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		data, err := os.ReadFile(filepath.Join(s.sessionDir, entry.Name()))
		if err != nil {
			slog.Warn("failed to read session file", "file", entry.Name(), "error", err)
			continue
		}

		var sess Session
		if err := json.Unmarshal(data, &sess); err != nil {
			slog.Warn("failed to parse session file", "file", entry.Name(), "error", err)
			continue
		}

		sess.maxMessages = s.maxMessages
		sess.sessionDir = s.sessionDir
		s.sessions.Store(sess.ChannelID, &sess)
		restored++
		slog.Info("restored session",
			"channel", sess.ChannelID,
			"messages", len(sess.Messages),
			"cli_session_id", sess.CLISessionID,
			"provider", sess.ActiveProvider,
		)
	}
	slog.Info("session restore complete", "total", restored)
}
