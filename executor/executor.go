package executor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/tackish/pigeon-claw/provider"
)

type Result struct {
	Output    string
	ImageData []byte // non-nil for screenshot
	ImagePath string
	IsError   bool
}

type Executor struct {
	ExecTimeout   time.Duration
	MaxToolOutput int
}

func New(execTimeout time.Duration, maxToolOutput int) *Executor {
	return &Executor{
		ExecTimeout:   execTimeout,
		MaxToolOutput: maxToolOutput,
	}
}

func (e *Executor) Execute(toolCall provider.ToolCall) *Result {
	switch toolCall.Name {
	case "shell_exec":
		return e.shellExec(toolCall.Arguments["command"])
	case "read_file":
		return e.readFile(toolCall.Arguments["path"])
	case "write_file":
		return e.writeFile(toolCall.Arguments["path"], toolCall.Arguments["content"])
	case "screenshot":
		return e.screenshot()
	case "list_dir":
		return e.listDir(toolCall.Arguments["path"])
	case "osascript":
		return e.osascript(toolCall.Arguments["script"])
	default:
		return &Result{Output: fmt.Sprintf("unknown tool: %s", toolCall.Name), IsError: true}
	}
}

func (e *Executor) shellExec(command string) *Result {
	ctx, cancel := context.WithTimeout(context.Background(), e.ExecTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	out, err := cmd.CombinedOutput()
	output := e.truncate(string(out))

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return &Result{Output: fmt.Sprintf("command timed out after %s\n%s", e.ExecTimeout, output), IsError: true}
		}
		return &Result{Output: fmt.Sprintf("%s\nexit status: %s", output, err.Error()), IsError: true}
	}

	return &Result{Output: output}
}

func (e *Executor) readFile(path string) *Result {
	data, err := os.ReadFile(path)
	if err != nil {
		return &Result{Output: fmt.Sprintf("error reading file: %s", err), IsError: true}
	}
	return &Result{Output: e.truncate(string(data))}
}

func (e *Executor) writeFile(path, content string) *Result {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return &Result{Output: fmt.Sprintf("error creating directory: %s", err), IsError: true}
	}

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return &Result{Output: fmt.Sprintf("error writing file: %s", err), IsError: true}
	}

	return &Result{Output: fmt.Sprintf("file written: %s (%d bytes)", path, len(content))}
}

func (e *Executor) screenshot() *Result {
	tmpFile := filepath.Join(os.TempDir(), fmt.Sprintf("pigeon-claw-screenshot-%d.png", time.Now().UnixNano()))

	cmd := exec.Command("screencapture", "-x", "-C", tmpFile)
	if err := cmd.Run(); err != nil {
		return &Result{Output: fmt.Sprintf("screenshot failed: %s", err), IsError: true}
	}

	data, err := os.ReadFile(tmpFile)
	if err != nil {
		return &Result{Output: fmt.Sprintf("failed to read screenshot: %s", err), IsError: true}
	}

	return &Result{
		Output:    "[Screenshot captured]",
		ImageData: data,
		ImagePath: tmpFile,
	}
}

func (e *Executor) listDir(path string) *Result {
	entries, err := os.ReadDir(path)
	if err != nil {
		return &Result{Output: fmt.Sprintf("error listing directory: %s", err), IsError: true}
	}

	var sb strings.Builder
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			sb.WriteString(fmt.Sprintf("%s (error getting info)\n", entry.Name()))
			continue
		}
		prefix := " "
		if entry.IsDir() {
			prefix = "d"
		}
		sb.WriteString(fmt.Sprintf("%s %10d %s %s\n", prefix, info.Size(), info.ModTime().Format("2006-01-02 15:04"), entry.Name()))
	}

	return &Result{Output: e.truncate(sb.String())}
}

func (e *Executor) osascript(script string) *Result {
	ctx, cancel := context.WithTimeout(context.Background(), e.ExecTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "osascript", "-e", script)
	out, err := cmd.CombinedOutput()
	output := e.truncate(string(out))

	if err != nil {
		return &Result{Output: fmt.Sprintf("%s\nerror: %s", output, err.Error()), IsError: true}
	}

	return &Result{Output: output}
}

func (e *Executor) truncate(s string) string {
	if len(s) <= e.MaxToolOutput {
		return s
	}
	return fmt.Sprintf("%s\n\n[output truncated from %d to %d characters]", s[:e.MaxToolOutput], len(s), e.MaxToolOutput)
}
