package discord

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/tackish/pigeon-claw/i18n"
	"github.com/tackish/pigeon-claw/provider"
	"github.com/tackish/pigeon-claw/router"
)

const (
	maxDiscordMessage   = 2000
	fileUploadThreshold = 10000
	typingInterval      = 10 * time.Second
	concurrencyTimeout  = 30 * time.Second
	maxImageDownload    = 20 * 1024 * 1024 // 20MB
)

var imageContentTypes = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/gif":  true,
	"image/webp": true,
}

type Handler struct {
	router          *router.Router
	channelLocks    sync.Map // map[channelID]*sync.Mutex
	mu              sync.RWMutex
	allowedChannels map[string]bool
	mentionChannels map[string]bool
	msgs            i18n.Messages
}

func (h *Handler) UpdateAllowedChannels(channels []string) {
	allowed := make(map[string]bool)
	for _, ch := range channels {
		allowed[ch] = true
	}
	h.mu.Lock()
	h.allowedChannels = allowed
	h.mu.Unlock()
}

func (h *Handler) UpdateMentionChannels(channels []string) {
	mention := make(map[string]bool)
	for _, ch := range channels {
		mention[ch] = true
	}
	h.mu.Lock()
	h.mentionChannels = mention
	h.mu.Unlock()
}

func NewHandler(r *router.Router, allowedChannels, mentionChannels []string, language string) *Handler {
	allowed := make(map[string]bool)
	for _, ch := range allowedChannels {
		allowed[ch] = true
	}
	mention := make(map[string]bool)
	for _, ch := range mentionChannels {
		mention[ch] = true
	}
	return &Handler{router: r, allowedChannels: allowed, mentionChannels: mention, msgs: i18n.Get(language)}
}

func (h *Handler) OnMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	// Ignore bot's own messages
	if m.Author.ID == s.State.User.ID {
		return
	}

	// Ignore channels not in allowed or mention list
	h.mu.RLock()
	isAllowed := h.allowedChannels[m.ChannelID]
	isMentionOnly := h.mentionChannels[m.ChannelID]
	hasFilter := len(h.allowedChannels) > 0 || len(h.mentionChannels) > 0
	h.mu.RUnlock()
	if hasFilter && !isAllowed && !isMentionOnly {
		return
	}

	// Mention-only channel: require @bot tag
	if isMentionOnly {
		mentioned := false
		for _, mention := range m.Mentions {
			if mention.ID == s.State.User.ID {
				mentioned = true
				break
			}
		}
		if !mentioned {
			return
		}
		// Strip the mention tag from content
		m.Content = strings.TrimSpace(
			strings.ReplaceAll(m.Content, "<@"+s.State.User.ID+">", ""),
		)
	}

	// Ignore messages with no text and no attachments
	if strings.TrimSpace(m.Content) == "" && len(m.Attachments) == 0 {
		return
	}

	// Handle built-in commands
	if h.handleBuiltinCommand(s, m) {
		return
	}

	// Per-channel concurrency control
	lockI, _ := h.channelLocks.LoadOrStore(m.ChannelID, &sync.Mutex{})
	lock := lockI.(*sync.Mutex)

	acquired := make(chan struct{})
	go func() {
		lock.Lock()
		close(acquired)
	}()

	select {
	case <-acquired:
		// Got the lock
	case <-time.After(concurrencyTimeout):
		s.ChannelMessageSend(m.ChannelID, h.msgs.RequestInProgress)
		return
	}
	defer lock.Unlock()

	// React to indicate processing
	if err := s.MessageReactionAdd(m.ChannelID, m.ID, "👀"); err != nil {
		slog.Warn("failed to add reaction", "emoji", "👀", "error", err)
	}

	// Start typing indicator
	stopTyping := h.startTyping(s, m.ChannelID)
	defer stopTyping()

	// Build message with attachments
	attachments := h.downloadAttachments(m.Attachments)

	// Status callback for intermediate updates
	var statusMsgID string
	onStatus := func(status string) {
		if statusMsgID == "" {
			msg, err := s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("-# %s", status))
			if err == nil {
				statusMsgID = msg.ID
			}
		} else {
			s.ChannelMessageEdit(m.ChannelID, statusMsgID, fmt.Sprintf("-# %s", status))
		}
	}

	// Route to LLM
	result := h.router.HandleWithAttachments(m.ChannelID, m.Content, attachments, onStatus)

	// Clean up status message
	if statusMsgID != "" {
		s.ChannelMessageDelete(m.ChannelID, statusMsgID)
	}

	// Remove processing emoji
	s.MessageReactionRemove(m.ChannelID, m.ID, "👀", s.State.User.ID)

	// Error case
	if result.Error {
		s.MessageReactionAdd(m.ChannelID, m.ID, "❌")
		s.ChannelMessageSend(m.ChannelID, h.msgs.AllProvidersFailed)
		return
	}

	// Send screenshot image if present
	if result.ImageData != nil {
		s.ChannelMessageSendComplex(m.ChannelID, &discordgo.MessageSend{
			Files: []*discordgo.File{
				{
					Name:   "screenshot.png",
					Reader: bytes.NewReader(result.ImageData),
				},
			},
		})
	}

	// Send text response
	if result.Text != "" {
		h.sendLongMessage(s, m.ChannelID, result.Text)
	}

	// Show token usage
	if result.TotalTokens > 0 {
		footer := fmt.Sprintf("-# %s | %d tokens", result.Provider, result.TotalTokens)
		if result.ToolsUsed > 0 {
			footer += fmt.Sprintf(" | %d tools", result.ToolsUsed)
		}
		s.ChannelMessageSend(m.ChannelID, footer)
	}

	// Status emoji based on what happened
	switch {
	case result.ImageData != nil:
		s.MessageReactionAdd(m.ChannelID, m.ID, "📸") // screenshot
	case result.IsFallback:
		s.MessageReactionAdd(m.ChannelID, m.ID, "⚡") // fallback provider used
	case result.ToolsUsed > 5:
		s.MessageReactionAdd(m.ChannelID, m.ID, "🔧") // heavy tool use
	case result.ToolsUsed > 0:
		s.MessageReactionAdd(m.ChannelID, m.ID, "⚙") // tools used
	case result.TotalTokens > 3000:
		s.MessageReactionAdd(m.ChannelID, m.ID, "📝") // long response
	default:
		s.MessageReactionAdd(m.ChannelID, m.ID, "✅") // simple success
	}
}

func (h *Handler) downloadAttachments(attachments []*discordgo.MessageAttachment) []provider.ContentPart {
	var parts []provider.ContentPart

	for _, att := range attachments {
		// Check if it's an image
		if !imageContentTypes[att.ContentType] {
			// Non-image attachment: add as text description
			parts = append(parts, provider.ContentPart{
				Type: provider.ContentText,
				Text: fmt.Sprintf("[Attachment: %s (%s, %d bytes)]", att.Filename, att.ContentType, att.Size),
			})
			continue
		}

		// Skip very large images
		if att.Size > maxImageDownload {
			parts = append(parts, provider.ContentPart{
				Type: provider.ContentText,
				Text: fmt.Sprintf("[Image too large: %s (%d bytes, max %d)]", att.Filename, att.Size, maxImageDownload),
			})
			continue
		}

		// Download image
		resp, err := http.Get(att.URL)
		if err != nil {
			slog.Warn("failed to download attachment", "url", att.URL, "error", err)
			continue
		}
		defer resp.Body.Close()

		data, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxImageDownload)))
		if err != nil {
			slog.Warn("failed to read attachment", "url", att.URL, "error", err)
			continue
		}

		parts = append(parts, provider.ContentPart{
			Type:      provider.ContentImage,
			ImageData: data,
			MimeType:  att.ContentType,
		})
		slog.Debug("downloaded attachment", "filename", att.Filename, "size", len(data))
	}

	return parts
}

func (h *Handler) handleBuiltinCommand(s *discordgo.Session, m *discordgo.MessageCreate) bool {
	content := strings.TrimSpace(m.Content)

	switch {
	case content == "!reset":
		h.router.GetSessions().Reset(m.ChannelID)
		s.ChannelMessageSend(m.ChannelID, h.msgs.SessionReset)
		return true

	case content == "!status":
		sess := h.router.GetSessions().GetOrCreate(m.ChannelID)
		msg := fmt.Sprintf("**Status**\n- Active Provider: %s\n- Messages: %d",
			sess.GetActiveProvider(), sess.MessageCount())
		s.ChannelMessageSend(m.ChannelID, msg)
		return true

	case content == "!provider":
		var sb strings.Builder
		sb.WriteString("**Provider Priority**\n")
		sess := h.router.GetSessions().GetOrCreate(m.ChannelID)
		active := sess.GetActiveProvider()
		for i, p := range h.router.GetProviders() {
			marker := ""
			if p.Name() == active {
				marker = " ← active"
			}
			sb.WriteString(fmt.Sprintf("%d. %s (%s)%s\n", i+1, p.Name(), p.Model(), marker))
		}
		s.ChannelMessageSend(m.ChannelID, sb.String())
		return true

	case content == "!model":
		var sb strings.Builder
		sb.WriteString("**Models**\n")
		for _, p := range h.router.GetProviders() {
			sb.WriteString(fmt.Sprintf("- %s: `%s`\n", p.Name(), p.Model()))
		}
		s.ChannelMessageSend(m.ChannelID, sb.String())
		return true

	case strings.HasPrefix(content, "!model "):
		args := strings.Fields(content[7:])
		if len(args) < 2 {
			s.ChannelMessageSend(m.ChannelID, h.msgs.ModelUsage)
			return true
		}
		providerName := args[0]
		modelName := args[1]
		for _, p := range h.router.GetProviders() {
			if p.Name() == providerName {
				p.SetModel(modelName)
				s.ChannelMessageSend(m.ChannelID, fmt.Sprintf(h.msgs.ModelChanged, providerName, modelName))
				return true
			}
		}
		s.ChannelMessageSend(m.ChannelID, fmt.Sprintf(h.msgs.ProviderNotFound, providerName))
		return true
	}

	return false
}

func (h *Handler) sendLongMessage(s *discordgo.Session, channelID, text string) {
	if len(text) <= maxDiscordMessage {
		if _, err := s.ChannelMessageSend(channelID, text); err != nil {
			slog.Error("failed to send message", "error", err)
		}
		return
	}

	// Very long output: upload as file
	if len(text) > fileUploadThreshold {
		s.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{
			Content: text[:maxDiscordMessage-50] + "\n\n... (전체 내용은 첨부 파일 참조)",
			Files: []*discordgo.File{
				{
					Name:   "response.txt",
					Reader: strings.NewReader(text),
				},
			},
		})
		return
	}

	// Split into chunks, respecting code blocks
	chunks := splitMessage(text, maxDiscordMessage)
	for _, chunk := range chunks {
		if _, err := s.ChannelMessageSend(channelID, chunk); err != nil {
			slog.Error("failed to send chunk", "error", err)
			return
		}
	}
}

func splitMessage(text string, maxLen int) []string {
	var chunks []string
	remaining := text

	for len(remaining) > 0 {
		if len(remaining) <= maxLen {
			chunks = append(chunks, remaining)
			break
		}

		// Find a good split point
		splitAt := maxLen
		// Try to split at a newline
		if idx := strings.LastIndex(remaining[:maxLen], "\n"); idx > maxLen/2 {
			splitAt = idx + 1
		}

		// Check if we're inside a code block
		chunk := remaining[:splitAt]
		openBlocks := strings.Count(chunk, "```")
		if openBlocks%2 != 0 {
			// Unclosed code block — close it and reopen in next chunk
			chunk += "\n```"
			chunks = append(chunks, chunk)
			remaining = "```\n" + remaining[splitAt:]
		} else {
			chunks = append(chunks, chunk)
			remaining = remaining[splitAt:]
		}
	}

	return chunks
}

func (h *Handler) startTyping(s *discordgo.Session, channelID string) func() {
	done := make(chan struct{})
	go func() {
		s.ChannelTyping(channelID)
		ticker := time.NewTicker(typingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				s.ChannelTyping(channelID)
			}
		}
	}()
	return func() { close(done) }
}
