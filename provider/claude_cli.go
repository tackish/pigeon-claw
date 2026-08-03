package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
)

type ClaudeCLI struct {
	modelSetting
	fallback string

	steerMu  sync.Mutex
	steering map[string]*steerSession // sessionID → live run accepting stdin input
}

// NewClaudeCLI builds the provider. An empty model means "whatever the CLI
// is configured to run" — no --model flag is passed, so the choice follows
// the user's Claude Code settings instead of being pinned here.
func NewClaudeCLI(model, fallback string) *ClaudeCLI {
	return &ClaudeCLI{modelSetting: newModelSetting(model), fallback: fallback, steering: make(map[string]*steerSession)}
}

// steerSession tracks a live CLI run started with --input-format
// stream-json, whose stdin stays open so additional user messages can
// be injected while it works.
//
// stdin closes as soon as the first result event arrives: a response
// means the request is complete. The CLI never exits on its own while
// stdin is open, and idle-stream events (hook_started, rate_limit_event,
// task_notification, ...) keep arriving after a result, so any
// "wait for quiet" heuristic ends up cancelled forever. Closing eagerly
// is safe for messages steered before the result: they were either
// merged into the running turn and answered by it, or are still
// buffered in the pipe — the CLI drains and answers them before
// exiting on EOF (verified against claude CLI 2.1.172). Messages
// arriving after the close start a fresh request via --resume.
type steerSession struct {
	mu     sync.Mutex
	stdin  io.WriteCloser
	closed bool
}

// writeUserMessage writes one stream-json user message line. Callers
// must hold ss.mu.
func (ss *steerSession) writeUserMessage(text string) error {
	payload := map[string]any{
		"type": "user",
		"message": map[string]any{
			"role":    "user",
			"content": []map[string]any{{"type": "text", "text": text}},
		},
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = ss.stdin.Write(append(b, '\n'))
	return err
}

// steer injects an extra user message into the running CLI. Returns
// false if the run already finished or is shutting down.
func (ss *steerSession) steer(text string) bool {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if ss.closed {
		return false
	}
	if err := ss.writeUserMessage(text); err != nil {
		return false
	}
	return true
}

// shutdown closes stdin so the CLI exits once it has drained any
// remaining buffered input. Idempotent.
func (ss *steerSession) shutdown() {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if ss.closed {
		return
	}
	ss.closed = true
	ss.stdin.Close()
}

func (c *ClaudeCLI) registerSteer(sessionID string, ss *steerSession) {
	c.steerMu.Lock()
	c.steering[sessionID] = ss
	c.steerMu.Unlock()
}

func (c *ClaudeCLI) unregisterSteer(sessionID string, ss *steerSession) {
	c.steerMu.Lock()
	if c.steering[sessionID] == ss {
		delete(c.steering, sessionID)
	}
	c.steerMu.Unlock()
	ss.shutdown()
}

// Steer implements provider.Steerable: delivers an additional user
// message to the live run for sessionID, if one exists.
func (c *ClaudeCLI) Steer(sessionID, message string) bool {
	c.steerMu.Lock()
	ss := c.steering[sessionID]
	c.steerMu.Unlock()
	if ss == nil {
		return false
	}
	return ss.steer(message)
}

// modelArgs returns the model flags for a run. Both are omitted when unset:
// passing no --model is what lets the CLI run its own configured model, and
// pinning a fallback here would override that choice on the retry too.
func (c *ClaudeCLI) modelArgs() []string {
	var args []string
	if model := c.Model(); model != "" {
		args = append(args, "--model", model)
	}
	if c.fallback != "" {
		args = append(args, "--fallback-model", c.fallback)
	}
	return args
}

func (c *ClaudeCLI) Name() string         { return "claude-cli" }
func (c *ClaudeCLI) SupportsImages() bool { return true }

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

	// stream-json input mode: the prompt goes through stdin instead of
	// argv, and stdin stays open so follow-up messages can be steered
	// into the run while it works (see Steer).
	args := []string{
		"-p",
		"--dangerously-skip-permissions",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose",
	}
	args = append(args, c.modelArgs()...)

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

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	ss := &steerSession{stdin: stdin}
	c.registerSteer(sessionID, ss)
	defer c.unregisterSteer(sessionID, ss)

	// Write the first message asynchronously — a large message (e.g.
	// rebuilt conversation history) could exceed the pipe buffer and
	// block until the CLI starts reading.
	go func() {
		ss.mu.Lock()
		defer ss.mu.Unlock()
		ss.writeUserMessage(finalMessage)
	}()

	resp, err := c.executeCmd(ctx, cmd, onStatus, ss)

	// Cleanup temp image files after CLI is done
	for _, f := range tmpFiles {
		os.Remove(f)
	}

	return resp, err
}

func (c *ClaudeCLI) SendWithStatus(ctx context.Context, systemPrompt string, messages []Message, tools []Tool, onStatus StatusCallback) (*Response, error) {
	prompt := c.buildPrompt(systemPrompt, messages)
	claudeBin := findClaudeBin()

	args := []string{
		"-p", prompt,
		"--dangerously-skip-permissions",
		"--output-format", "stream-json",
		"--verbose",
	}
	args = append(args, c.modelArgs()...)

	cmd := exec.CommandContext(ctx, claudeBin, args...)

	return c.executeCmd(ctx, cmd, onStatus, nil)
}

// executeCmd runs the CLI and streams its stream-json output. When ss
// is non-nil the run accepts steered messages: each result event is
// pushed to the caller immediately via a TURN_RESULT status, and the
// first result closes stdin so the CLI exits after draining any
// buffered steered messages.
func (c *ClaudeCLI) executeCmd(ctx context.Context, cmd *exec.Cmd, onStatus StatusCallback, ss *steerSession) (*Response, error) {
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
	var errSubtype string // set by a result event that failed without text
	var runModel string   // model reported by the CLI for this run

	// Tool usage, in first-use order — the caller reports it so the user
	// can see what the run actually did.
	var toolsRun []ToolUse
	toolIdx := map[string]int{}
	countTool := func(name string) {
		if i, ok := toolIdx[name]; ok {
			toolsRun[i].Count++
			return
		}
		toolIdx[name] = len(toolsRun)
		toolsRun = append(toolsRun, ToolUse{Name: name, Count: 1})
	}

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
				Model   string `json:"model"`
				Content []struct {
					Type  string          `json:"type"`
					Text  string          `json:"text"`
					Name  string          `json:"name"`
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
			Subtype    string  `json:"subtype"`
			IsError    bool    `json:"is_error"`
		}

		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}

		switch event.Type {
		case "assistant":
			// The model that actually answered — which is not necessarily
			// the configured one, since the CLI picks its own default when
			// none is pinned and may switch to the fallback mid-run.
			if event.Message.Model != "" {
				runModel = event.Message.Model
			}

			// Parse tool_use from message.content array
			reported := false
			for _, block := range event.Message.Content {
				if block.Type == "tool_use" && block.Name != "" {
					countTool(block.Name)
					if onStatus == nil {
						continue
					}
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
				} else if block.Type == "text" && block.Text != "" && onStatus != nil {
					onStatus("✍ writing...")
					reported = true
				}
			}
			if !reported && onStatus != nil {
				onStatus("💭 thinking...")
			}
		case "user":
			// Tool result returned — signal handler to resume idle checks.
			if onStatus != nil {
				onStatus("TOOL_END:⚙ tool 완료, 다음 단계...")
			}
		case "result":
			// One user turn completed. Multiple result events arrive
			// when messages were steered into the run.
			//
			// A failed run (error_max_turns, error_during_execution, ...)
			// omits the result field entirely, so an empty text here means
			// there is no answer to deliver — remember why, and never
			// report it as a completed turn.
			totalInput += event.Usage.InputTokens
			totalOutput += event.Usage.OutputTokens
			if event.ResultText == "" {
				if event.IsError && errSubtype == "" {
					errSubtype = event.Subtype
					if errSubtype == "" {
						errSubtype = "unknown error"
					}
				}
			} else {
				if finalText.Len() > 0 {
					finalText.WriteString("\n\n")
				}
				finalText.WriteString(event.ResultText)
				if ss != nil && onStatus != nil {
					onStatus("TURN_RESULT:" + event.ResultText)
				}
			}
			if ss != nil {
				// A response is out — the request is complete. Close
				// stdin now; buffered steered messages still drain and
				// stream their own results before the CLI exits.
				ss.shutdown()
			}
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
			return &Response{Content: text, ToolsRun: toolsRun, Model: runModel}, nil
		}
		stderr := strings.TrimSpace(stderrBuf.String())
		if stderr != "" {
			return nil, fmt.Errorf("claude cli error: %w, stderr: %s", err, stderr)
		}
		return nil, fmt.Errorf("claude cli error: %w", err)
	}

	// Exited cleanly but produced no answer: report the failure instead
	// of returning an empty response the caller would silently drop.
	if finalText.Len() == 0 {
		if errSubtype != "" {
			return nil, fmt.Errorf("%w: %s", ErrRunFailed, errSubtype)
		}
		if stderr := strings.TrimSpace(stderrBuf.String()); stderr != "" {
			return nil, fmt.Errorf("claude cli produced no result, stderr: %s", stderr)
		}
		return nil, fmt.Errorf("claude cli produced no result")
	}

	return &Response{
		Content: finalText.String(),
		Usage: TokenUsage{
			PromptTokens: totalInput,
			OutputTokens: totalOutput,
			TotalTokens:  totalInput + totalOutput,
		},
		ToolsRun: toolsRun,
		Model:    runModel,
	}, nil
}

// FindClaudeBin returns the path to the claude CLI binary, checking common
// install locations before falling back to PATH lookup. Exported so other
// packages can locate the same binary this provider runs.
func FindClaudeBin() string { return findClaudeBin() }

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
