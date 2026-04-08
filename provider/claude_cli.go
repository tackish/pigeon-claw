package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
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
func (c *ClaudeCLI) SupportsImages() bool  { return false }

func (c *ClaudeCLI) Send(ctx context.Context, systemPrompt string, messages []Message, tools []Tool) (*Response, error) {
	return c.SendWithStatus(ctx, systemPrompt, messages, tools, nil)
}

func (c *ClaudeCLI) SendWithSession(ctx context.Context, systemPrompt string, message string, tools []Tool, sessionID string, resume bool, onStatus StatusCallback) (*Response, error) {
	claudeBin := findClaudeBin()

	args := []string{
		"-p", message,
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
	return c.executeCmd(ctx, cmd, onStatus)
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
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	var stderrBuf strings.Builder
	cmd.Stderr = &stderrBuf

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start claude cli: %w", err)
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
				Content string `json:"content"`
			} `json:"message"`
			Tool struct {
				Name string `json:"name"`
			} `json:"tool"`
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
			if onStatus != nil {
				onStatus("💭 thinking...")
			}
		case "tool_use":
			if onStatus != nil && event.Tool.Name != "" {
				onStatus(fmt.Sprintf("🔧 %s", event.Tool.Name))
			}
		case "tool_result":
			if onStatus != nil {
				onStatus("⚙ processing result...")
			}
		case "content_block_delta":
			// Text streaming — CLI is actively generating
			if onStatus != nil {
				onStatus("✍ writing...")
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
