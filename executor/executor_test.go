package executor

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tackish/pigeon-claw/provider"
)

func newTestExecutor() *Executor {
	return New(10*time.Second, 4000)
}

func TestShellExec(t *testing.T) {
	e := newTestExecutor()
	result := e.Execute(provider.ToolCall{
		Name:      "shell_exec",
		Arguments: map[string]string{"command": "echo hello"},
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Output)
	}
	if result.Output != "hello\n" {
		t.Fatalf("expected 'hello\\n', got '%s'", result.Output)
	}
}

func TestShellExecTimeout(t *testing.T) {
	e := New(1*time.Second, 4000)
	result := e.Execute(provider.ToolCall{
		Name:      "shell_exec",
		Arguments: map[string]string{"command": "sleep 10"},
	})
	if !result.IsError {
		t.Fatal("expected timeout error")
	}
}

func TestShellExecErrorStatus(t *testing.T) {
	e := newTestExecutor()
	result := e.Execute(provider.ToolCall{
		Name:      "shell_exec",
		Arguments: map[string]string{"command": "exit 1"},
	})
	if !result.IsError {
		t.Fatal("expected error for exit 1")
	}
}

func TestReadFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("file content"), 0644)

	e := newTestExecutor()
	result := e.Execute(provider.ToolCall{
		Name:      "read_file",
		Arguments: map[string]string{"path": path},
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Output)
	}
	if result.Output != "file content" {
		t.Fatalf("expected 'file content', got '%s'", result.Output)
	}
}

func TestReadFileNotFound(t *testing.T) {
	e := newTestExecutor()
	result := e.Execute(provider.ToolCall{
		Name:      "read_file",
		Arguments: map[string]string{"path": "/nonexistent/file"},
	})
	if !result.IsError {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestWriteFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "out.txt")

	e := newTestExecutor()
	result := e.Execute(provider.ToolCall{
		Name:      "write_file",
		Arguments: map[string]string{"path": path, "content": "written"},
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Output)
	}

	data, _ := os.ReadFile(path)
	if string(data) != "written" {
		t.Fatalf("expected 'written', got '%s'", string(data))
	}
}

func TestListDir(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0644)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("bb"), 0644)

	e := newTestExecutor()
	result := e.Execute(provider.ToolCall{
		Name:      "list_dir",
		Arguments: map[string]string{"path": dir},
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Output)
	}
	if !containsStr(result.Output, "a.txt") || !containsStr(result.Output, "b.txt") {
		t.Fatalf("missing files in output: %s", result.Output)
	}
}

func TestTruncation(t *testing.T) {
	e := New(10*time.Second, 100) // max 100 chars
	result := e.Execute(provider.ToolCall{
		Name:      "shell_exec",
		Arguments: map[string]string{"command": "python3 -c \"print('x'*500)\""},
	})
	if !containsStr(result.Output, "[output truncated") {
		t.Fatalf("expected truncation message, got: %s", result.Output)
	}
}

func TestUnknownTool(t *testing.T) {
	e := newTestExecutor()
	result := e.Execute(provider.ToolCall{
		Name:      "unknown_tool",
		Arguments: map[string]string{},
	})
	if !result.IsError {
		t.Fatal("expected error for unknown tool")
	}
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
