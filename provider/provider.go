package provider

import "context"

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

type Response struct {
	Content   string
	ToolCalls []ToolCall
	Usage     TokenUsage
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
	SendWithSession(ctx context.Context, systemPrompt string, message string, tools []Tool, sessionID string, resume bool, onStatus StatusCallback) (*Response, error)
}
