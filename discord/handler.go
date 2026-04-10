package discord

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/tackish/pigeon-claw/i18n"
	"github.com/tackish/pigeon-claw/provider"
	"github.com/tackish/pigeon-claw/router"
)

const (
	maxDiscordMessage   = 2000
	fileUploadThreshold = 10000
	typingInterval   = 10 * time.Second
	maxImageDownload = 20 * 1024 * 1024 // 20MB
)

var imageContentTypes = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/gif":  true,
	"image/webp": true,
}

type retryInfo struct {
	channelID   string
	content     string
	attachments []*discordgo.MessageAttachment
}

type Handler struct {
	router          *router.Router
	channelLocks    sync.Map // map[channelID]*sync.Mutex
	retryMessages   sync.Map // map[messageID]*retryInfo
	activeRequests  sync.Map // map[channelID]string — content being processed
	cancelFuncs     sync.Map // map[channelID]context.CancelFunc
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

	// Per-channel concurrency: 1 request at a time.
	// If the user wants to interrupt, they can use !cancel.
	semI, _ := h.channelLocks.LoadOrStore(m.ChannelID, make(chan struct{}, 1))
	sem := semI.(chan struct{})

	select {
	case sem <- struct{}{}:
		// Acquired the slot
	default:
		// Channel is busy — show what's being processed
		active, _ := h.activeRequests.Load(m.ChannelID)
		preview := "..."
		if str, ok := active.(string); ok && str != "" {
			preview = str
			if len(preview) > 80 {
				preview = preview[:80] + "..."
			}
		}
		s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("-# %s", fmt.Sprintf(h.msgs.RequestInProgress, preview)))
		return
	}
	defer func() { <-sem }()

	// Track what's being processed for concurrency messages
	h.activeRequests.Store(m.ChannelID, m.Content)
	defer h.activeRequests.Delete(m.ChannelID)

	// Create cancellable context for !cancel support
	ctx, cancel := context.WithCancel(context.Background())
	h.cancelFuncs.Store(m.ChannelID, cancel)
	defer h.cancelFuncs.Delete(m.ChannelID)
	defer cancel()

	// React to indicate processing
	if err := s.MessageReactionAdd(m.ChannelID, m.ID, "👀"); err != nil {
		slog.Warn("failed to add reaction", "emoji", "👀", "error", err)
	}

	// Start typing indicator (stops when ctx is cancelled or request completes)
	stopTyping := h.startTyping(ctx, s, m.ChannelID)
	defer stopTyping()

	// Build message with attachments
	attachments := h.downloadAttachments(m.Attachments)

	// Status message: show progress with elapsed time
	preview := m.Content
	if len(preview) > 60 {
		preview = preview[:60] + "..."
	}

	startTime := time.Now()
	var statusMsgID string
	var idleAlertID string
	var lastStatus string
	var cliPID string
	var lastActivity time.Time
	var toolRunning bool // true while a Bash/Read/Edit tool is executing
	var statusMu sync.Mutex

	// Create initial status message
	initMsg, err := s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("-# ⏳ `%s` 처리 중...", preview))
	if err == nil {
		statusMsgID = initMsg.ID
	}

	// Auto-cancel if any tool produces no new stream-json event within
	// this window. We terminate ONLY the claude-cli process (its
	// SysProcAttr puts it in its own pgid), so child processes like
	// ffmpeg/python keep running, orphaned to init. The user can then
	// check on them via PID in a follow-up message.
	const autoCancelIdle = 1 * time.Minute

	// Periodic elapsed time updater + idle alert
	statusDone := make(chan struct{})
	idleAlerted := false
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-statusDone:
				return
			case <-ticker.C:
				statusMu.Lock()
				elapsed := time.Since(startTime).Truncate(time.Second)
				text := fmt.Sprintf("-# ⏳ %s 경과", elapsed)
				if cliPID != "" {
					text += fmt.Sprintf(" | %s", cliPID)
				}
				if lastStatus != "" {
					text += fmt.Sprintf("\n-# %s", lastStatus)
				}
				if !lastActivity.IsZero() {
					idle := time.Since(lastActivity).Truncate(time.Second)
					if toolRunning {
						text += fmt.Sprintf(" | tool 실행 중 %s", idle)
					} else if idle > 20*time.Second {
						text += fmt.Sprintf(" | CLI 응답 대기 %s", idle)
					}
					if idle > autoCancelIdle && !idleAlerted {
						idleAlerted = true
						msg := fmt.Sprintf(
							"-# ⚠ %s 동안 응답 없음 — CLI 세션을 종료합니다. "+
								"자식 프로세스는 살아있어요. "+
								"`ls -lt ~/.pigeon-claw/jobs/` 또는 `ps aux | grep <명령어>` 로 확인하세요.", idle)
						alertMsg, _ := s.ChannelMessageSend(m.ChannelID, msg)
						if alertMsg != nil {
							idleAlertID = alertMsg.ID
						}
						cancel()
					}
				}
				if statusMsgID != "" {
					s.ChannelMessageEdit(m.ChannelID, statusMsgID, text)
				}
				statusMu.Unlock()
			}
		}
	}()

	onStatus := func(status string) {
		statusMu.Lock()
		defer statusMu.Unlock()

		// Capture PID from CLI start event
		if strings.HasPrefix(status, "🚀 CLI started") {
			cliPID = status
			return
		}

		// Tool lifecycle markers from claude-cli provider
		if strings.HasPrefix(status, "TOOL_START:") {
			toolRunning = true
			status = strings.TrimPrefix(status, "TOOL_START:")
		} else if strings.HasPrefix(status, "TOOL_END:") {
			toolRunning = false
			status = strings.TrimPrefix(status, "TOOL_END:")
		}

		lastStatus = status
		lastActivity = time.Now()
		elapsed := time.Since(startTime).Truncate(time.Second)
		text := fmt.Sprintf("-# ⏳ %s 경과", elapsed)
		if cliPID != "" {
			text += fmt.Sprintf(" | %s", cliPID)
		}
		text += fmt.Sprintf("\n-# %s", status)
		if statusMsgID != "" {
			s.ChannelMessageEdit(m.ChannelID, statusMsgID, text)
		}
	}

	// Route to LLM
	result := h.router.HandleWithAttachments(ctx, m.ChannelID, m.Content, attachments, onStatus)

	// Stop elapsed time updater and clean up status messages
	close(statusDone)
	statusMu.Lock()
	wasIdleCancelled := idleAlerted
	if statusMsgID != "" {
		s.ChannelMessageDelete(m.ChannelID, statusMsgID)
	}
	// Keep idleAlertID on screen if that's how we got here — it has the
	// instructions for checking on orphaned processes. Delete it only
	// if the request completed normally.
	if idleAlertID != "" && !wasIdleCancelled {
		s.ChannelMessageDelete(m.ChannelID, idleAlertID)
	}
	statusMu.Unlock()

	// Remove processing emoji
	s.MessageReactionRemove(m.ChannelID, m.ID, "👀", s.State.User.ID)

	// If the request was cancelled (!cancel or idle timeout), discard any
	// result that came back afterwards.
	if ctx.Err() != nil {
		if wasIdleCancelled {
			slog.Warn("request auto-cancelled due to idle timeout", "channel", m.ChannelID)
			s.MessageReactionAdd(m.ChannelID, m.ID, "⏱")
		} else {
			slog.Info("request cancelled by user", "channel", m.ChannelID)
			s.MessageReactionAdd(m.ChannelID, m.ID, "🛑")
		}
		return
	}

	// Error case: send error message with 🔄 for retry
	if result.Error {
		s.MessageReactionAdd(m.ChannelID, m.ID, "❌")
		errMsg, _ := s.ChannelMessageSend(m.ChannelID, h.msgs.AllProvidersFailed)
		if errMsg != nil {
			s.MessageReactionAdd(m.ChannelID, errMsg.ID, "🔄")
			h.retryMessages.Store(errMsg.ID, &retryInfo{
				channelID:   m.ChannelID,
				content:     m.Content,
				attachments: m.Attachments,
			})
		}
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
	case content == "!help":
		s.ChannelMessageSend(m.ChannelID, h.msgs.Help)
		return true

	case content == "!reset":
		h.router.GetSessions().Reset(m.ChannelID)
		s.ChannelMessageSend(m.ChannelID, h.msgs.SessionReset)
		return true

	case content == "!restart":
		s.ChannelMessageSend(m.ChannelID, "-# 재시작 중...")
		go func() {
			time.Sleep(500 * time.Millisecond)

			// Release PID lock before exit
			home, _ := os.UserHomeDir()
			os.Remove(filepath.Join(home, ".pigeon-claw", "pigeon-claw.pid"))

			// Find binary and resolve symlinks (brew uses symlinks)
			exe, err := exec.LookPath("pigeon-claw")
			if err != nil {
				exe, _ = os.Executable()
			}
			resolved, err := filepath.EvalSymlinks(exe)
			if err != nil {
				resolved = exe
			}

			slog.Info("restarting", "binary", resolved)

			// Pass restart channel so new process can send completion message
			env := os.Environ()
			env = append(env, "PIGEON_RESTART_CHANNEL="+m.ChannelID)

			if err := syscall.Exec(resolved, []string{resolved, "serve"}, env); err != nil {
				slog.Error("syscall.Exec failed, falling back to cmd.Start", "error", err)
				// Fallback: start new process and exit
				cmd := exec.Command(resolved, "serve")
				cmd.Env = env
				cmd.Stdout = os.Stdout
				cmd.Stderr = os.Stderr
				cmd.Start()
				os.Exit(0)
			}
		}()
		return true

	case content == "!cancel":
		if cancel, ok := h.cancelFuncs.LoadAndDelete(m.ChannelID); ok {
			cancel.(context.CancelFunc)()
			s.ChannelMessageSend(m.ChannelID, "-# 현재 요청을 취소했습니다.")
		} else {
			s.ChannelMessageSend(m.ChannelID, "-# 처리 중인 요청이 없습니다.")
		}
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

	case content == "!debug":
		sess := h.router.GetSessions().GetOrCreate(m.ChannelID)
		debug := h.router.GetDebug(m.ChannelID)

		var sb strings.Builder
		sb.WriteString("**Debug Info**\n")
		sb.WriteString(fmt.Sprintf("- Channel: `%s`\n", m.ChannelID))
		sb.WriteString(fmt.Sprintf("- Active Provider: `%s`\n", sess.GetActiveProvider()))
		sb.WriteString(fmt.Sprintf("- Session Messages: %d\n", sess.MessageCount()))
		sb.WriteString(fmt.Sprintf("- CLI Session ID: `%s`\n", sess.GetCLISessionID()))
		sb.WriteString("\n**Providers**\n")
		for i, p := range h.router.GetProviders() {
			sb.WriteString(fmt.Sprintf("%d. %s (`%s`)\n", i+1, p.Name(), p.Model()))
		}

		if debug != nil {
			if !debug.LastRequestAt.IsZero() {
				sb.WriteString(fmt.Sprintf("\n**Last Request**\n"))
				sb.WriteString(fmt.Sprintf("- Time: %s\n", debug.LastRequestAt.Format("2006-01-02 15:04:05")))
				sb.WriteString(fmt.Sprintf("- Message: `%s`\n", debug.LastRequestMsg))
				if !debug.LastCompleteAt.IsZero() {
					elapsed := debug.LastCompleteAt.Sub(debug.LastRequestAt).Truncate(time.Second)
					sb.WriteString(fmt.Sprintf("- Completed: %s (%s, %d tokens)\n", debug.LastCompleteAt.Format("15:04:05"), elapsed, debug.LastTokens))
				} else {
					elapsed := time.Since(debug.LastRequestAt).Truncate(time.Second)
					sb.WriteString(fmt.Sprintf("- Status: **처리 중** (%s 경과)\n", elapsed))
				}
			}
			if debug.LastError != "" {
				sb.WriteString(fmt.Sprintf("\n**Last Error**\n"))
				sb.WriteString(fmt.Sprintf("- Provider: `%s`\n", debug.LastProvider))
				sb.WriteString(fmt.Sprintf("- Time: %s\n", debug.LastErrorAt.Format("2006-01-02 15:04:05")))
				sb.WriteString(fmt.Sprintf("- Error:\n```\n%s\n```\n", debug.LastError))
			}
		} else {
			sb.WriteString("\n*No activity recorded for this channel.*\n")
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
			Content: text[:maxDiscordMessage-50] + "\n\n" + h.msgs.SeeAttachment,
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

func (h *Handler) startTyping(ctx context.Context, s *discordgo.Session, channelID string) func() {
	done := make(chan struct{})
	var once sync.Once
	stop := func() { once.Do(func() { close(done) }) }
	go func() {
		s.ChannelTyping(channelID)
		ticker := time.NewTicker(typingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.ChannelTyping(channelID)
			}
		}
	}()
	return stop
}

func (h *Handler) OnReactionAdd(s *discordgo.Session, r *discordgo.MessageReactionAdd) {
	// Ignore bot's own reactions
	if r.UserID == s.State.User.ID {
		return
	}
	if r.Emoji.Name != "🔄" {
		return
	}

	val, ok := h.retryMessages.LoadAndDelete(r.MessageID)
	if !ok {
		return
	}
	info := val.(*retryInfo)

	// Delete the error message
	s.ChannelMessageDelete(r.ChannelID, r.MessageID)

	// Build attachments
	attachments := h.downloadAttachments(info.attachments)

	// React to indicate processing
	s.ChannelTyping(r.ChannelID)

	// Status callback
	var statusMsgID string
	onStatus := func(status string) {
		if statusMsgID == "" {
			msg, err := s.ChannelMessageSend(r.ChannelID, fmt.Sprintf("-# %s", status))
			if err == nil {
				statusMsgID = msg.ID
			}
		} else {
			s.ChannelMessageEdit(r.ChannelID, statusMsgID, fmt.Sprintf("-# %s", status))
		}
	}

	// Retry the request
	result := h.router.HandleWithAttachments(context.Background(), info.channelID, info.content, attachments, onStatus)

	if statusMsgID != "" {
		s.ChannelMessageDelete(r.ChannelID, statusMsgID)
	}

	if result.Error {
		errMsg, _ := s.ChannelMessageSend(r.ChannelID, h.msgs.AllProvidersFailed)
		if errMsg != nil {
			s.MessageReactionAdd(r.ChannelID, errMsg.ID, "🔄")
			h.retryMessages.Store(errMsg.ID, info)
		}
		return
	}

	if result.ImageData != nil {
		s.ChannelMessageSendComplex(r.ChannelID, &discordgo.MessageSend{
			Files: []*discordgo.File{{Name: "screenshot.png", Reader: bytes.NewReader(result.ImageData)}},
		})
	}
	if result.Text != "" {
		h.sendLongMessage(s, r.ChannelID, result.Text)
	}
	if result.TotalTokens > 0 {
		footer := fmt.Sprintf("-# %s | %d tokens", result.Provider, result.TotalTokens)
		if result.ToolsUsed > 0 {
			footer += fmt.Sprintf(" | %d tools", result.ToolsUsed)
		}
		s.ChannelMessageSend(r.ChannelID, footer)
	}
}

var slashCommands = []*discordgo.ApplicationCommand{
	{Name: "help", Description: "Show available commands"},
	{Name: "reset", Description: "Reset current channel session"},
	{Name: "cancel", Description: "Cancel the current request"},
	{Name: "restart", Description: "Restart bot (includes update check)"},
	{Name: "status", Description: "Show active provider and message count"},
	{Name: "debug", Description: "Show last error, session ID, debug info"},
	{Name: "model", Description: "List or change provider models"},
	{Name: "provider", Description: "Show provider priority order"},
}

func (h *Handler) RegisterSlashCommands(s *discordgo.Session) {
	for _, guild := range s.State.Guilds {
		// Remove stale commands from previous registrations
		existing, err := s.ApplicationCommands(s.State.User.ID, guild.ID)
		if err == nil {
			for _, cmd := range existing {
				s.ApplicationCommandDelete(s.State.User.ID, guild.ID, cmd.ID)
			}
		}

		// Register fresh commands
		for _, cmd := range slashCommands {
			if _, err := s.ApplicationCommandCreate(s.State.User.ID, guild.ID, cmd); err != nil {
				slog.Warn("failed to register slash command", "command", cmd.Name, "guild", guild.ID, "error", err)
			}
		}
		slog.Info("slash commands registered", "guild", guild.ID, "count", len(slashCommands))
	}
}

func (h *Handler) OnInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionApplicationCommand {
		return
	}

	// Resolve user from guild member or DM user
	var userID string
	if i.Member != nil {
		userID = i.Member.User.ID
	} else if i.User != nil {
		userID = i.User.ID
	}

	// Create a fake MessageCreate so we can reuse handleBuiltinCommand
	fake := &discordgo.MessageCreate{
		Message: &discordgo.Message{
			ChannelID: i.ChannelID,
			Content:   "!" + i.ApplicationCommandData().Name,
			Author:    &discordgo.User{ID: userID},
		},
	}

	// Run the command (sends response via ChannelMessageSend)
	h.handleBuiltinCommand(s, fake)

	// Acknowledge the interaction silently to prevent "interaction failed"
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredMessageUpdate,
	})
}
