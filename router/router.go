package router

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/tackish/pigeon-claw/executor"
	"github.com/tackish/pigeon-claw/prompt"
	"github.com/tackish/pigeon-claw/provider"
	"github.com/tackish/pigeon-claw/session"
	"github.com/tackish/pigeon-claw/tools"
)

type HandleResult struct {
	Text        string
	ImageData   []byte
	ImagePath   string
	TotalTokens int
	Provider    string
	ToolsUsed   int
	IsFallback  bool
	Error       bool
}

// DebugInfo holds diagnostic info for the !debug command.
type DebugInfo struct {
	LastError    string
	LastErrorAt  time.Time
	LastProvider string
	ChannelID    string
}

type Router struct {
	providers     []provider.Provider
	sessions      *session.Store
	promptBuilder *prompt.Builder
	executor      *executor.Executor
	maxIterations int
	timeout       time.Duration

	debugMu   sync.Mutex
	debugInfo map[string]*DebugInfo // per channel
}

func New(
	providers []provider.Provider,
	sessions *session.Store,
	promptBuilder *prompt.Builder,
	exec *executor.Executor,
	maxIterations int,
	timeout time.Duration,
) *Router {
	return &Router{
		providers:     providers,
		sessions:      sessions,
		promptBuilder: promptBuilder,
		executor:      exec,
		maxIterations: maxIterations,
		timeout:       timeout,
		debugInfo:     make(map[string]*DebugInfo),
	}
}

func (r *Router) Handle(channelID, content string) *HandleResult {
	return r.HandleWithAttachments(channelID, content, nil, nil)
}

func (r *Router) HandleWithAttachments(channelID, content string, attachments []provider.ContentPart, onStatus provider.StatusCallback) *HandleResult {
	sess := r.sessions.GetOrCreate(channelID)

	msg := provider.Message{Role: provider.RoleUser, Content: content}
	if len(attachments) > 0 {
		// Build multimodal message: text + images
		parts := []provider.ContentPart{}
		if content != "" {
			parts = append(parts, provider.ContentPart{Type: provider.ContentText, Text: content})
		}
		parts = append(parts, attachments...)
		msg.Parts = parts
	}
	sess.Append(msg)

	systemPrompt := r.promptBuilder.Build()
	toolDefs := tools.Definitions()

	// Determine provider order: if session has an active provider, try it first
	providers := r.orderedProviders(sess.GetActiveProvider())

	for i, p := range providers {
		slog.Info("trying provider", "provider", p.Name(), "attempt", i+1)

		result, err := r.tryProvider(channelID, p, systemPrompt, toolDefs, sess, i > 0, onStatus)
		if err != nil {
			slog.Warn("provider failed", "provider", p.Name(), "error", err)
			r.setDebug(channelID, p.Name(), err)
			continue
		}

		result.IsFallback = i > 0
		sess.SetActiveProvider(p.Name())
		return result
	}

	return &HandleResult{Error: true}
}

func generateSessionID() string {
	b := make([]byte, 16)
	rand.Read(b)
	// UUID v4 format
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func (r *Router) tryProvider(
	channelID string,
	p provider.Provider,
	systemPrompt string,
	toolDefs []provider.Tool,
	sess *session.Session,
	isFallback bool,
	onStatus provider.StatusCallback,
) (*HandleResult, error) {
	// Check if provider supports session-based calls (e.g., Claude CLI)
	if sa, ok := p.(provider.SessionAware); ok && !isFallback {
		return r.trySessionAwareProvider(sa, p, systemPrompt, toolDefs, sess, onStatus)
	}

	var messages []provider.Message

	if isFallback {
		// Inject context summary for fallback providers
		contextSummary := sess.ExportContext()
		messages = []provider.Message{
			{Role: provider.RoleUser, Content: contextSummary},
		}
	} else {
		messages = sess.History()
	}

	var lastImageData []byte
	var lastImagePath string
	totalTokens := 0
	toolsUsed := 0

	for iteration := 0; iteration < r.maxIterations; iteration++ {
		ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
		resp, err := p.SendWithStatus(ctx, systemPrompt, messages, toolDefs, onStatus)
		cancel()

		if err != nil {
			return nil, fmt.Errorf("send failed: %w", err)
		}

		totalTokens += resp.Usage.TotalTokens
		slog.Debug("token usage", "provider", p.Name(), "iteration", iteration,
			"prompt", resp.Usage.PromptTokens, "output", resp.Usage.OutputTokens,
			"total_this_call", resp.Usage.TotalTokens, "total_cumulative", totalTokens)

		// No tool calls — final text response
		if len(resp.ToolCalls) == 0 {
			if resp.Content != "" {
				sess.Append(provider.Message{Role: provider.RoleAssistant, Content: resp.Content})
			}
			slog.Info("request complete", "provider", p.Name(), "total_tokens", totalTokens, "tools_used", toolsUsed)
			return &HandleResult{
				Text:        resp.Content,
				ImageData:   lastImageData,
				ImagePath:   lastImagePath,
				TotalTokens: totalTokens,
				Provider:    p.Name(),
				ToolsUsed:   toolsUsed,
			}, nil
		}

		// Process tool calls
		toolsUsed += len(resp.ToolCalls)

		// First append the assistant message with tool calls info
		toolCallSummary := resp.Content
		if toolCallSummary == "" {
			toolCallSummary = "[tool calls]"
		}
		sess.Append(provider.Message{Role: provider.RoleAssistant, Content: toolCallSummary})

		// Add assistant message with tool calls to conversation
		messages = append(messages, provider.Message{Role: provider.RoleAssistant, Content: toolCallSummary})

		for _, tc := range resp.ToolCalls {
			slog.Info("executing tool", "tool", tc.Name, "args", tc.Arguments)

			result := r.executor.Execute(tc)

			// Track screenshots for Discord upload
			if tc.Name == "screenshot" && result.ImageData != nil {
				lastImageData = result.ImageData
				lastImagePath = result.ImagePath

				// For providers that support images, add as image content
				if p.SupportsImages() {
					sess.Append(provider.Message{
						Role:       provider.RoleTool,
						Content:    result.Output,
						ToolCallID: tc.ID,
						Parts: []provider.ContentPart{
							{Type: provider.ContentImage, ImageData: result.ImageData, MimeType: "image/png"},
						},
					})
					messages = append(messages, provider.Message{
						Role:       provider.RoleTool,
						Content:    result.Output,
						ToolCallID: tc.ID,
						Parts: []provider.ContentPart{
							{Type: provider.ContentImage, ImageData: result.ImageData, MimeType: "image/png"},
						},
					})
				} else {
					sess.Append(provider.Message{
						Role:       provider.RoleTool,
						Content:    "[Screenshot taken and sent to Discord]",
						ToolCallID: tc.ID,
					})
					messages = append(messages, provider.Message{
						Role:       provider.RoleTool,
						Content:    "[Screenshot taken and sent to Discord]",
						ToolCallID: tc.ID,
					})
				}
			} else {
				sess.Append(provider.Message{
					Role:       provider.RoleTool,
					Content:    result.Output,
					ToolCallID: tc.ID,
				})
				messages = append(messages, provider.Message{
					Role:       provider.RoleTool,
					Content:    result.Output,
					ToolCallID: tc.ID,
				})
			}
		}
	}

	return nil, fmt.Errorf("max tool iterations (%d) reached", r.maxIterations)
}

func (r *Router) orderedProviders(activeProvider string) []provider.Provider {
	// Always use config priority order. ActiveProvider is only used
	// to avoid re-exporting context when the same provider is still first.
	return r.providers
}

func (r *Router) UpdateProviders(providers []provider.Provider) {
	r.providers = providers
}

func (r *Router) GetProviders() []provider.Provider {
	return r.providers
}

func (r *Router) GetSessions() *session.Store {
	return r.sessions
}

func (r *Router) setDebug(channelID, providerName string, err error) {
	r.debugMu.Lock()
	defer r.debugMu.Unlock()
	r.debugInfo[channelID] = &DebugInfo{
		LastError:    err.Error(),
		LastErrorAt:  time.Now(),
		LastProvider: providerName,
		ChannelID:    channelID,
	}
}

func (r *Router) GetDebug(channelID string) *DebugInfo {
	r.debugMu.Lock()
	defer r.debugMu.Unlock()
	return r.debugInfo[channelID]
}

// buildContextMessage constructs a single message containing conversation
// history from JSON, used when CLI session resume fails and we need to
// start a fresh session without losing context.
func buildContextMessage(history []provider.Message) string {
	var sb strings.Builder
	sb.WriteString("Here is the previous conversation history:\n\n")
	for _, msg := range history {
		switch msg.Role {
		case provider.RoleUser:
			sb.WriteString(fmt.Sprintf("User: %s\n", msg.Content))
		case provider.RoleAssistant:
			sb.WriteString(fmt.Sprintf("Assistant: %s\n", msg.Content))
		}
	}
	sb.WriteString("\nContinue from the last user message above.")
	return sb.String()
}

func (r *Router) trySessionAwareProvider(
	sa provider.SessionAware,
	p provider.Provider,
	systemPrompt string,
	toolDefs []provider.Tool,
	sess *session.Session,
	onStatus provider.StatusCallback,
) (*HandleResult, error) {
	// Determine session ID and whether to resume
	sessionID := sess.GetCLISessionID()
	resume := sessionID != ""
	if !resume {
		sessionID = generateSessionID()
		sess.SetCLISessionID(sessionID)
	}

	// Get the latest user message only (already appended to session)
	history := sess.History()
	if len(history) == 0 {
		return nil, fmt.Errorf("no messages in session")
	}
	lastMsg := history[len(history)-1]

	slog.Info("session-aware call", "provider", p.Name(), "session_id", sessionID, "resume", resume)

	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()

	resp, err := sa.SendWithSession(ctx, systemPrompt, lastMsg.Content, toolDefs, sessionID, resume, onStatus)
	if err != nil && resume {
		// Resume failed — CLI session expired or lost.
		// Retry as new session with conversation history as context.
		slog.Warn("resume failed, retrying with conversation history", "error", err)
		sessionID = generateSessionID()
		sess.SetCLISessionID(sessionID)

		// Build context from JSON history so LLM doesn't lose conversation
		contextMsg := buildContextMessage(history)

		ctx2, cancel2 := context.WithTimeout(context.Background(), r.timeout)
		defer cancel2()
		resp, err = sa.SendWithSession(ctx2, systemPrompt, contextMsg, toolDefs, sessionID, false, onStatus)
	}
	if err != nil {
		sess.SetCLISessionID("")
		return nil, fmt.Errorf("session-aware send failed: %w", err)
	}

	if resp.Content != "" {
		sess.Append(provider.Message{Role: provider.RoleAssistant, Content: resp.Content})
	}

	slog.Info("request complete", "provider", p.Name(), "total_tokens", resp.Usage.TotalTokens)

	return &HandleResult{
		Text:        resp.Content,
		TotalTokens: resp.Usage.TotalTokens,
		Provider:    p.Name(),
	}, nil
}
