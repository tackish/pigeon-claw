package session

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tackish/pigeon-claw/provider"
)

func TestSessionAppendAndHistory(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(50, dir)

	sess := store.GetOrCreate("ch1")
	sess.Append(provider.Message{Role: provider.RoleUser, Content: "hello"})
	sess.Append(provider.Message{Role: provider.RoleAssistant, Content: "hi"})

	history := sess.History()
	if len(history) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(history))
	}
	if history[0].Content != "hello" {
		t.Fatalf("expected 'hello', got '%s'", history[0].Content)
	}
}

func TestSlidingWindow(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(5, dir) // max 5 messages

	sess := store.GetOrCreate("ch2")
	for i := 0; i < 10; i++ {
		sess.Append(provider.Message{Role: provider.RoleUser, Content: "msg"})
	}

	if sess.MessageCount() != 5 {
		t.Fatalf("expected 5 messages after truncation, got %d", sess.MessageCount())
	}
}

func TestFilePersistence(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(50, dir)

	sess := store.GetOrCreate("ch3")
	sess.Append(provider.Message{Role: provider.RoleUser, Content: "persist me"})
	sess.Append(provider.Message{Role: provider.RoleAssistant, Content: "ok"})

	// Check file exists
	filePath := filepath.Join(dir, "channel_ch3.json")
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		t.Fatal("session file not created")
	}

	// Load from disk in new store
	store2 := NewStore(50, dir)
	sess2 := store2.GetOrCreate("ch3")
	history := sess2.History()

	if len(history) != 2 {
		t.Fatalf("expected 2 messages after restore, got %d", len(history))
	}
	if history[0].Content != "persist me" {
		t.Fatalf("expected 'persist me', got '%s'", history[0].Content)
	}
}

func TestReset(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(50, dir)

	sess := store.GetOrCreate("ch4")
	sess.Append(provider.Message{Role: provider.RoleUser, Content: "bye"})
	store.Reset("ch4")

	// File should be deleted
	filePath := filepath.Join(dir, "channel_ch4.json")
	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Fatal("session file not deleted after reset")
	}

	// New session should be empty
	sess2 := store.GetOrCreate("ch4")
	if sess2.MessageCount() != 0 {
		t.Fatalf("expected 0 messages after reset, got %d", sess2.MessageCount())
	}
}

func TestExportContext(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(50, dir)

	sess := store.GetOrCreate("ch5")
	sess.Append(provider.Message{Role: provider.RoleUser, Content: "what is go?"})
	sess.Append(provider.Message{Role: provider.RoleAssistant, Content: "Go is a programming language"})

	export := sess.ExportContext()
	if export == "" {
		t.Fatal("export is empty")
	}
	if !contains(export, "what is go?") {
		t.Fatal("export missing user message")
	}
	if !contains(export, "Go is a programming language") {
		t.Fatal("export missing assistant message")
	}
	if !contains(export, "continue this conversation") {
		t.Fatal("export missing continuation instruction")
	}
}

func TestActiveProvider(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(50, dir)

	sess := store.GetOrCreate("ch6")
	sess.SetActiveProvider("claude")
	if sess.GetActiveProvider() != "claude" {
		t.Fatal("active provider not set")
	}

	// Persist and restore
	store2 := NewStore(50, dir)
	sess2 := store2.GetOrCreate("ch6")
	if sess2.GetActiveProvider() != "claude" {
		t.Fatal("active provider not restored")
	}
}

func TestCLISessionIDPersistence(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(50, dir)

	sess := store.GetOrCreate("ch_cli")

	// Initially empty
	if id := sess.GetCLISessionID(); id != "" {
		t.Fatalf("expected empty CLI session ID, got '%s'", id)
	}

	// Set and verify
	sess.SetCLISessionID("test-uuid-1234")
	if id := sess.GetCLISessionID(); id != "test-uuid-1234" {
		t.Fatalf("expected 'test-uuid-1234', got '%s'", id)
	}

	// Restore from disk
	store2 := NewStore(50, dir)
	sess2 := store2.GetOrCreate("ch_cli")
	if id := sess2.GetCLISessionID(); id != "test-uuid-1234" {
		t.Fatalf("CLI session ID not restored from JSON, got '%s'", id)
	}

	// Reset should clear it
	store2.Reset("ch_cli")
	sess3 := store2.GetOrCreate("ch_cli")
	if id := sess3.GetCLISessionID(); id != "" {
		t.Fatalf("expected empty after reset, got '%s'", id)
	}
}

// Simulates pigeon-claw crash → restart → session restored from JSON
func TestRestartResumesSession(t *testing.T) {
	dir := t.TempDir()

	// === Phase 1: Bot running, conversation happens ===
	store1 := NewStore(50, dir)
	sess1 := store1.GetOrCreate("ch_restart")
	sess1.SetActiveProvider("claude-cli")
	sess1.SetCLISessionID("abc-123-uuid")
	sess1.Append(provider.Message{Role: provider.RoleUser, Content: "hello"})
	sess1.Append(provider.Message{Role: provider.RoleAssistant, Content: "hi there"})
	sess1.Append(provider.Message{Role: provider.RoleUser, Content: "do something"})

	// === Phase 2: Bot crashes (store1 goes away) ===
	store1 = nil

	// === Phase 3: Bot restarts, loads from disk ===
	store2 := NewStore(50, dir)
	sess2 := store2.GetOrCreate("ch_restart")

	// Verify everything is restored
	if sess2.GetCLISessionID() != "abc-123-uuid" {
		t.Fatalf("CLI session ID not restored, got '%s'", sess2.GetCLISessionID())
	}
	if sess2.GetActiveProvider() != "claude-cli" {
		t.Fatalf("active provider not restored, got '%s'", sess2.GetActiveProvider())
	}
	if sess2.MessageCount() != 3 {
		t.Fatalf("expected 3 messages, got %d", sess2.MessageCount())
	}

	// Verify resume logic: CLISessionID is not empty → resume=true
	resume := sess2.GetCLISessionID() != ""
	if !resume {
		t.Fatal("should resume existing session after restart")
	}

	// Simulate new message after restart
	sess2.Append(provider.Message{Role: provider.RoleUser, Content: "continue after restart"})
	history := sess2.History()
	lastMsg := history[len(history)-1]
	if lastMsg.Content != "continue after restart" {
		t.Fatalf("expected last message to be new one, got '%s'", lastMsg.Content)
	}

	// CLI session ID should still be the same
	if sess2.GetCLISessionID() != "abc-123-uuid" {
		t.Fatalf("CLI session ID changed after append, got '%s'", sess2.GetCLISessionID())
	}
}

func TestConcurrentAccess(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(100, dir)
	sess := store.GetOrCreate("ch7")

	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 50; j++ {
				sess.Append(provider.Message{Role: provider.RoleUser, Content: "concurrent"})
			}
			done <- true
		}()
	}
	for i := 0; i < 10; i++ {
		<-done
	}

	// Should have exactly 100 (max) due to sliding window
	count := sess.MessageCount()
	if count != 100 {
		t.Fatalf("expected 100 messages, got %d", count)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
