package provider

import (
	"context"
	"errors"
)

// ErrRunFailed marks a request that reached the provider and ended
// without an answer (e.g. the CLI hit its turn limit or aborted
// mid-execution). The session itself is intact, so callers should
// surface the failure rather than rebuilding the session from history.
var ErrRunFailed = errors.New("provider run failed")

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type ContentType string

const (
	ContentText  ContentType = "text"
	ContentImage ContentType = "image"
)

type ContentPart struct {
	Type      ContentType
	Text      string
	ImageData []byte // base64-decoded image bytes
	MimeType  string // e.g. "image/png"
}

type Message struct {
	Role       Role
	Content    string        // simple text content
	Parts      []ContentPart // multimodal content (used when non-empty)
	ToolCallID string        // for tool result messages
}

type Tool struct {
	Name        string
	Description string
	Parameters  []ToolParameter
}

type ToolParameter struct {
	Name        string
	Type        string // "string", "integer", etc.
	Description string
	Required    bool
}

type ToolCall struct {
	ID        string
	Name      string
	Arguments map[string]string
}

type TokenUsage struct {
	PromptTokens int
	OutputTokens int
	TotalTokens  int
}

// ToolUse counts how often one tool ran during a request. Used to tell the
// user what work actually happened, not just that something did.
type ToolUse struct {
	Name  string
	Count int
}

type Response struct {
	Content   string
	ToolCalls []ToolCall
	Usage     TokenUsage
	// Tools the provider ran itself, in first-use order. Providers that
	// execute tools on our side (see Executor) leave this empty.
	ToolsRun []ToolUse
	// Model that actually produced this response, when the provider
	// reports one. It can differ from the configured model: nothing is
	// pinned by default, and a run may fall back mid-flight.
	Model string
}

// StatusCallback is called during processing to report intermediate status
type StatusCallback func(status string)

type Provider interface {
	Name() string
	Model() string
	SetModel(model string)
	Send(ctx context.Context, systemPrompt string, messages []Message, tools []Tool) (*Response, error)
	SendWithStatus(ctx context.Context, systemPrompt string, messages []Message, tools []Tool, onStatus StatusCallback) (*Response, error)
	SupportsImages() bool
}

// SessionAware is an optional interface for providers that support
// persistent sessions (e.g., Claude CLI with --session-id/--resume).
// When implemented, the router will pass session IDs to avoid resending
// full conversation history on every turn.
type SessionAware interface {
	SendWithSession(ctx context.Context, systemPrompt string, message string, images []ContentPart, tools []Tool, sessionID string, resume bool, onStatus StatusCallback) (*Response, error)
}

// Steerable is an optional interface for providers that can inject
// additional user messages into an already-running session request
// (steering), the way Claude Code accepts new input mid-task.
// Steer returns false when there is no live run for the session —
// the caller should fall back to its busy handling.
type Steerable interface {
	Steer(sessionID string, message string) bool
}
