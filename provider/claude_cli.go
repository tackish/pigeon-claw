package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

type ClaudeCLI struct {
	model string
}

func NewClaudeCLI(model string) *ClaudeCLI {
	if model == "" {
		// Detect default model from claude CLI
		model = detectClaudeModel()
	}
	return &ClaudeCLI{model: model}
}

func detectClaudeModel() string {
	claudeBin := findClaudeBin()
	cmd := exec.Command(claudeBin, "-p", "hi", "--output-format", "json", "--max-turns", "1", "--dangerously-skip-permissions")
	out, err := cmd.Output()
	if err == nil {
		var resp struct {
			ModelUsage map[string]json.RawMessage `json:"modelUsage"`
		}
		if json.Unmarshal(out, &resp) == nil && len(resp.ModelUsage) > 0 {
			for key := range resp.ModelUsage {
				// key is like "claude-opus-4-6[1m]" — strip the context window suffix
				model := key
				if idx := strings.Index(model, "["); idx != -1 {
					model = model[:idx]
				}
				return model
			}
		}
	}
	return "sonnet"
}

func (c *ClaudeCLI) Name() string          { return "claude-cli" }
func (c *ClaudeCLI) Model() string         { return c.model }
func (c *ClaudeCLI) SetModel(model string) { c.model = model }
func (c *ClaudeCLI) SupportsImages() bool  { return true }

func (c *ClaudeCLI) Send(ctx context.Context, systemPrompt string, messages []Message, tools []Tool) (*Response, error) {
	return c.SendWithStatus(ctx, systemPrompt, messages, tools, nil)
}

func (c *ClaudeCLI) SendWithSession(ctx context.Context, systemPrompt string, message string, images []ContentPart, tools []Tool, sessionID string, resume bool, onStatus StatusCallback) (*Response, error) {
	claudeBin := findClaudeBin()

	// Save image attachments to temp files and prepend paths to the message
	// Claude CLI can read images via its Read tool (multimodal)
	var tmpFiles []string
	for i, img := range images {
		if img.Type != ContentImage || len(img.ImageData) == 0 {
			continue
		}
		ext := ".png"
		switch img.MimeType {
		case "image/jpeg":
			ext = ".jpg"
		case "image/gif":
			ext = ".gif"
		case "image/webp":
			ext = ".webp"
		}
		tmpFile, err := os.CreateTemp("", fmt.Sprintf("pigeon-img-%d-*%s", i, ext))
		if err != nil {
			continue
		}
		if _, err := tmpFile.Write(img.ImageData); err != nil {
			tmpFile.Close()
			os.Remove(tmpFile.Name())
			continue
		}
		tmpFile.Close()
		tmpFiles = append(tmpFiles, tmpFile.Name())
	}

	// Prepend image file paths to the message so Claude CLI reads them
	finalMessage := message
	if len(tmpFiles) > 0 {
		var sb strings.Builder
		sb.WriteString("[첨부된 이미지 파일 — Read 도구로 확인하세요]\n")
		for _, f := range tmpFiles {
			sb.WriteString(fmt.Sprintf("- %s\n", f))
		}
		sb.WriteString("\n")
		sb.WriteString(message)
		finalMessage = sb.String()
	}

	args := []string{
		"-p", finalMessage,
		"--model", c.model,
		"--dangerously-skip-permissions",
		"--output-format", "stream-json",
		"--verbose",
	}

	if resume {
		// Resume existing session: --resume <session-id>
		args = append(args, "--resume", sessionID)
	} else {
		// First turn: create session with UUID + system prompt
		args = append(args, "--session-id", sessionID, "--system-prompt", systemPrompt)
	}

	cmd := exec.CommandContext(ctx, claudeBin, args...)
	// Fix working directory so Claude CLI always finds its sessions
	// in the same project path (~/.claude/projects/-Users-{user}/)
	home, _ := os.UserHomeDir()
	cmd.Dir = home
	resp, err := c.executeCmd(ctx, cmd, onStatus)

	// Cleanup temp image files after CLI is done
	for _, f := range tmpFiles {
		os.Remove(f)
	}

	return resp, err
}

func (c *ClaudeCLI) SendWithStatus(ctx context.Context, systemPrompt string, messages []Message, tools []Tool, onStatus StatusCallback) (*Response, error) {
	prompt := c.buildPrompt(systemPrompt, messages)
	claudeBin := findClaudeBin()

	cmd := exec.CommandContext(ctx, claudeBin,
		"-p", prompt,
		"--model", c.model,
		"--dangerously-skip-permissions",
		"--output-format", "stream-json",
		"--verbose",
	)

	return c.executeCmd(ctx, cmd, onStatus)
}

func (c *ClaudeCLI) executeCmd(ctx context.Context, cmd *exec.Cmd, onStatus StatusCallback) (*Response, error) {
	// Put claude-cli in its own process group so we can kill JUST it on
	// timeout without reaping legitimate long-running child processes
	// (ffmpeg, python scripts, etc.) that it spawned. They become
	// orphans and get re-parented to init — still alive for the user
	// to check on later via PID.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	var stderrBuf strings.Builder
	cmd.Stderr = &stderrBuf

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start claude cli: %w", err)
	}

	cliPID := cmd.Process.Pid

	// When ctx is cancelled, send SIGTERM to ONLY the claude-cli process
	// (not the whole process group). Children stay alive.
	ctxDone := make(chan struct{})
	defer close(ctxDone)
	go func() {
		select {
		case <-ctx.Done():
			_ = syscall.Kill(cliPID, syscall.SIGTERM)
		case <-ctxDone:
		}
	}()

	if onStatus != nil {
		onStatus(fmt.Sprintf("🚀 CLI started (PID %d)", cliPID))
	}

	var finalText strings.Builder
	var totalInput, totalOutput int

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MB buffer

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var event struct {
			Type    string `json:"type"`
			Content string `json:"content"`
			Message struct {
				Content []struct {
					Type  string `json:"type"`
					Text  string `json:"text"`
					Name  string `json:"name"`
					Input json.RawMessage `json:"input"`
				} `json:"content"`
				StopReason *string `json:"stop_reason"`
			} `json:"message"`
			ToolUseResult struct {
				Content string `json:"content"`
			} `json:"tool_use_result"`
			Usage struct {
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
			ResultText string  `json:"result"`
			CostUSD    float64 `json:"cost_usd"`
		}

		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}

		switch event.Type {
		case "assistant":
			// Parse tool_use from message.content array
			if onStatus != nil {
				reported := false
				for _, block := range event.Message.Content {
					if block.Type == "tool_use" && block.Name != "" {
						// Extract short description from input
						var input map[string]interface{}
						json.Unmarshal(block.Input, &input)
						detail := block.Name
						if cmd, ok := input["command"].(string); ok {
							if len(cmd) > 60 {
								cmd = cmd[:60] + "..."
							}
							detail += ": " + cmd
						} else if pattern, ok := input["pattern"].(string); ok {
							detail += ": " + pattern
						} else if path, ok := input["file_path"].(string); ok {
							detail += ": " + path
						}
						// Prefix with TOOL_START: so handler knows a tool is running
						// and can pause idle-timeout checks.
						onStatus(fmt.Sprintf("TOOL_START:🔧 %s", detail))
						reported = true
					} else if block.Type == "text" && block.Text != "" {
						onStatus("✍ writing...")
						reported = true
					}
				}
				if !reported {
					onStatus("💭 thinking...")
				}
			}
		case "user":
			// Tool result returned — signal handler to resume idle checks.
			if onStatus != nil {
				onStatus("TOOL_END:⚙ tool 완료, 다음 단계...")
			}
		case "result":
			finalText.WriteString(event.ResultText)
			totalInput = event.Usage.InputTokens
			totalOutput = event.Usage.OutputTokens
		default:
			if event.Content != "" {
				finalText.WriteString(event.Content)
			}
		}
	}

	if err := cmd.Wait(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("claude cli timed out")
		}
		text := finalText.String()
		if text != "" {
			return &Response{Content: text}, nil
		}
		stderr := strings.TrimSpace(stderrBuf.String())
		if stderr != "" {
			return nil, fmt.Errorf("claude cli error: %w, stderr: %s", err, stderr)
		}
		return nil, fmt.Errorf("claude cli error: %w", err)
	}

	return &Response{
		Content: finalText.String(),
		Usage: TokenUsage{
			PromptTokens: totalInput,
			OutputTokens: totalOutput,
			TotalTokens:  totalInput + totalOutput,
		},
	}, nil
}

func findClaudeBin() string {
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
	return "claude"
}

func (c *ClaudeCLI) buildPrompt(systemPrompt string, messages []Message) string {
	var sb strings.Builder

	sb.WriteString(systemPrompt)
	sb.WriteString("\n\n---\n\n")

	for _, msg := range messages {
		switch msg.Role {
		case RoleUser:
			sb.WriteString(fmt.Sprintf("User: %s\n", msg.Content))
		case RoleAssistant:
			sb.WriteString(fmt.Sprintf("Assistant: %s\n", msg.Content))
		}
	}

	return sb.String()
}
