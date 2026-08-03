package discord

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os/exec"
	"runtime/debug"
	"strconv"
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
	maxImageDownload    = 20 * 1024 * 1024 // 20MB
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
	channelQueues   sync.Map // map[channelID]*channelQueue — one runner per channel
	retryMessages   sync.Map // map[messageID]*retryInfo
	activeRequests  sync.Map // map[channelID]string — content being processed
	cancelFuncs     sync.Map // map[channelID]context.CancelFunc
	recordingNames  sync.Map // map[channelID]string — name for current recording
	mu              sync.RWMutex
	allowedChannels map[string]bool
	mentionChannels map[string]bool
	msgs            i18n.Messages

	// Gateway redeliveries are dropped here — see eventDedupe.
	dedupe *eventDedupe
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

// channelPolicy reports how this instance treats channelID: whether it is
// in the allow list, whether it is mention-only, and whether this instance
// serves it at all.
//
// Both the message and interaction paths must consult it. Several bots can
// share one token while being configured for different channels — a normal
// deployment — and the gateway hands every event to all of them. The filter
// is what keeps each in its own channels; an unfiltered path lets a foreign
// instance answer here, which reads as the bot replying twice.
func (h *Handler) channelPolicy(channelID string) (allowed, mentionOnly, serves bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	allowed = h.allowedChannels[channelID]
	mentionOnly = h.mentionChannels[channelID]
	hasFilter := len(h.allowedChannels) > 0 || len(h.mentionChannels) > 0
	serves = !hasFilter || allowed || mentionOnly
	return allowed, mentionOnly, serves
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
	return &Handler{
		router:          r,
		allowedChannels: allowed,
		mentionChannels: mention,
		msgs:            i18n.Get(language),
		dedupe:          newEventDedupe(5 * time.Minute),
	}
}

func (h *Handler) OnMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	// Ignore bot's own messages
	if m.Author.ID == s.State.User.ID {
		return
	}

	// A redelivered message is not a second request — acting on it runs
	// the command twice.
	if h.dedupe != nil && !h.dedupe.firstTime(m.ID) {
		slog.Warn("dropping duplicate message event", "message", m.ID, "channel", m.ChannelID)
		return
	}

	// Ignore channels not in allowed or mention list
	_, isMentionOnly, serves := h.channelPolicy(m.ChannelID)
	if !serves {
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

	h.dispatch(s, m, true)
}

// dispatch routes one request: into a live run if that run can still take
// it, otherwise onto the channel's queue.
//
// Every request goes through here — a Discord message and a 🔄 retry alike.
// Running two at once on the same channel would have two CLI processes
// resuming the same session, so the queue is the only way in.
func (h *Handler) dispatch(s *discordgo.Session, m *discordgo.MessageCreate, allowSteer bool) {
	// A live run can take the message directly — the agent picks it up
	// mid-task (text only; the steering path can't carry attachments).
	if allowSteer && len(m.Attachments) == 0 && h.router.Steer(m.ChannelID, m.Content) {
		s.MessageReactionAdd(m.ChannelID, m.ID, "➕")
		return
	}

	// Otherwise one request runs at a time per channel. Anything arriving
	// while one is running waits its turn instead of being turned away —
	// steering is already closed in the window between the answer and the
	// run actually ending, and a message refused there is simply lost.
	q := h.queueFor(m.ChannelID)
	switch q.admit(m) {
	case queued:
		s.MessageReactionAdd(m.ChannelID, m.ID, "📥")
		return
	case rejected:
		s.ChannelMessageSend(m.ChannelID, fmt.Sprintf(
			"-# ⚠️ 대기열이 가득 찼습니다 (%d개). 현재 요청이 끝난 뒤 다시 보내주세요.", maxQueuedPerChannel))
		return
	}

	// This goroutine owns the channel until the backlog drains.
	for {
		h.runGuarded(s, m)
		next := q.next()
		if next == nil {
			return
		}
		m = next
	}
}

// runGuarded runs one request and contains a panic. Without this a panic
// would skip the hand-off below and leave the channel marked as running
// forever, so every later message would queue behind a request that already
// died — the channel would go silent until the bot restarts.
func (h *Handler) runGuarded(s *discordgo.Session, m *discordgo.MessageCreate) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("request panicked", "channel", m.ChannelID, "panic", r, "stack", string(debug.Stack()))
			s.ChannelMessageSend(m.ChannelID, "-# ❌ 내부 오류로 요청이 중단되었습니다. 로그를 확인하세요.")
			s.MessageReactionAdd(m.ChannelID, m.ID, "💥")
		}
	}()
	h.processRequest(s, m)
}

// processRequest runs one request to completion and reports it in the
// channel. The caller owns the channel slot (see channelQueue).
func (h *Handler) processRequest(s *discordgo.Session, m *discordgo.MessageCreate) {
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
	var lastStatus string
	var cliPID string // display string, e.g. "🚀 CLI started (PID 2762)"
	var lastActivity time.Time
	var toolRunning bool     // true while a Bash/Read/Edit tool is executing
	var streamedResults bool // true once turn results were sent via TURN_RESULT
	var statusMu sync.Mutex

	// Create initial status message
	initMsg, err := s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("-# ⏳ `%s` 처리 중...", preview))
	if err == nil {
		statusMsgID = initMsg.ID
	}

	// Periodic elapsed time updater
	statusDone := make(chan struct{})
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
				}
				if statusMsgID != "" {
					s.ChannelMessageEdit(m.ChannelID, statusMsgID, text)
				}
				statusMu.Unlock()
			}
		}
	}()

	onStatus := func(status string) {
		// A turn finished while the run continues (steered messages
		// pending) — deliver its text to the channel right away.
		if strings.HasPrefix(status, "TURN_RESULT:") {
			text := strings.TrimPrefix(status, "TURN_RESULT:")
			sent := strings.TrimSpace(text) != ""
			statusMu.Lock()
			lastActivity = time.Now()
			toolRunning = false
			// Only a delivered turn suppresses the final send below —
			// an empty result must not swallow the response.
			if sent {
				streamedResults = true
			}
			statusMu.Unlock()
			if sent {
				h.sendLongMessage(s, m.ChannelID, text)
			}
			return
		}

		statusMu.Lock()
		defer statusMu.Unlock()

		// Capture PID from CLI start event, e.g. "🚀 CLI started (PID 2762)"
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
	if statusMsgID != "" {
		s.ChannelMessageDelete(m.ChannelID, statusMsgID)
	}
	statusMu.Unlock()

	// Remove processing emoji
	s.MessageReactionRemove(m.ChannelID, m.ID, "👀", s.State.User.ID)

	// If the request was cancelled (!cancel or idle timeout), discard any
	// result that came back afterwards.
	if ctx.Err() != nil {
		slog.Info("request cancelled by user", "channel", m.ChannelID)
		s.MessageReactionAdd(m.ChannelID, m.ID, "🛑")
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

	// Send text response — unless turn results were already streamed
	// via TURN_RESULT (result.Text is their concatenation).
	statusMu.Lock()
	streamed := streamedResults
	statusMu.Unlock()
	if result.Text != "" && !streamed {
		h.sendLongMessage(s, m.ChannelID, result.Text)
	}

	// Close with what the request actually did — elapsed time, cost, and
	// the tools that ran — so a reply never just stops.
	delivered := streamed || result.Text != "" || result.ImageData != nil
	s.ChannelMessageSend(m.ChannelID, requestSummary(result, time.Since(startTime), delivered))

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

	case content == "!recording" || strings.HasPrefix(content, "!recording "):
		nameArg := strings.TrimSpace(strings.TrimPrefix(content, "!recording"))
		if err := obsStartRecording(); err != nil {
			s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("-# ❌ OBS 녹화 시작 실패: %s", err))
			return true
		}
		if nameArg != "" {
			h.recordingNames.Store(m.ChannelID, nameArg)
			s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("-# 🔴 OBS 녹화 시작: `%s`", nameArg))
		} else {
			h.recordingNames.Delete(m.ChannelID)
			s.ChannelMessageSend(m.ChannelID, "-# 🔴 OBS 녹화 시작")
		}
		return true

	case content == "!stop-recording" || strings.HasPrefix(content, "!stop-recording "):
		folderArg := strings.TrimSpace(strings.TrimPrefix(content, "!stop-recording"))
		h.handleStopRecording(s, m.ChannelID, folderArg)
		return true

	case content == "!reset":
		h.router.GetSessions().Reset(m.ChannelID)
		s.ChannelMessageSend(m.ChannelID, h.msgs.SessionReset)
		return true

	case content == "!restart":
		s.ChannelMessageSend(m.ChannelID, "-# 재시작 중...")
		go h.restartProcess(m.ChannelID)
		return true

	case content == "!update":
		go h.handleUpdate(s, m.ChannelID)
		return true

	case content == "!cancel":
		// Cancelling the running request while its follow-ups go ahead
		// anyway is not what anyone means by cancel — drop those too.
		dropped := h.queueFor(m.ChannelID).drop()
		if cancel, ok := h.cancelFuncs.LoadAndDelete(m.ChannelID); ok {
			cancel.(context.CancelFunc)()
			msg := "-# 현재 요청을 취소했습니다."
			if dropped > 0 {
				msg += fmt.Sprintf(" 대기 중이던 %d개도 취소했습니다.", dropped)
			}
			s.ChannelMessageSend(m.ChannelID, msg)
		} else if dropped > 0 {
			s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("-# 대기 중이던 요청 %d개를 취소했습니다.", dropped))
		} else {
			s.ChannelMessageSend(m.ChannelID, "-# 처리 중인 요청이 없습니다.")
		}
		return true

	case content == "!status" || content == "!debug":
		h.sendStatus(s, m.ChannelID)
		return true

	case content == "!model":
		h.sendModelPicker(s, m.ChannelID)
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

// snapshotCLIChildren returns a list of "PID  elapsed  command" lines for
// every process still alive in the claude-cli's process group, excluding
// the CLI itself. claude-cli is started with Setpgid=true, so its pgid
// equals its PID; pgrep -g catches children AND grandchildren (e.g., a
// bash -c wrapper's ffmpeg child), plus any backgrounded siblings that
// were launched earlier in the same request.
func snapshotCLIChildren(cliPID int) []string {
	if cliPID <= 0 {
		return nil
	}
	out, err := exec.Command("pgrep", "-g", strconv.Itoa(cliPID)).Output()
	if err != nil {
		return nil
	}

	var lines []string
	selfStr := strconv.Itoa(cliPID)
	for _, pidStr := range strings.Fields(string(out)) {
		if pidStr == selfStr {
			continue // skip claude-cli itself — it's about to be killed
		}
		psOut, err := exec.Command("ps", "-p", pidStr, "-o", "pid=,etime=,command=").Output()
		if err != nil {
			continue
		}
		line := strings.TrimSpace(string(psOut))
		if line == "" {
			continue
		}
		// Trim very long command lines so they fit in Discord code blocks.
		if len(line) > 140 {
			line = line[:137] + "..."
		}
		lines = append(lines, line)
	}
	return lines
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

	// Take the 🔄 back so the failed message stops looking clickable; it
	// stays in the channel as the anchor the retry reacts on.
	s.MessageReactionRemove(r.ChannelID, r.MessageID, "🔄", s.State.User.ID)

	// Run the retry the same way a message runs — same queue, same cancel
	// support, same reporting. Calling the router directly here would let a
	// retry run beside a normal request, with both resuming one CLI session.
	// Steering is not offered: a retry replaces a failed request, it is not
	// a follow-up to whatever is running now.
	h.dispatch(s, &discordgo.MessageCreate{Message: &discordgo.Message{
		ID:          r.MessageID,
		ChannelID:   r.ChannelID,
		Content:     info.content,
		Author:      &discordgo.User{ID: r.UserID},
		Attachments: info.attachments,
	}}, false)
}

var slashCommands = []*discordgo.ApplicationCommand{
	{Name: "help", Description: "Show available commands"},
	{Name: "reset", Description: "Reset current channel session"},
	{Name: "cancel", Description: "Cancel the current request"},
	{Name: "restart", Description: "Restart bot (includes update check)"},
	{Name: "update", Description: "Update pigeon-claw to the latest release and restart"},
	{Name: "status", Description: "Show providers, models, queue and last error"},
	{Name: "model", Description: "Pick a model from a dropdown"},
	{
		Name:        "recording",
		Description: "Start OBS recording",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "name",
				Description: "File name (without extension)",
				Required:    false,
			},
		},
	},
	{
		Name:        "stop-recording",
		Description: "Stop OBS recording and optionally move to a folder",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "folder",
				Description: "Subfolder under OBS recording directory",
				Required:    false,
			},
		},
	},
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
	if i.Type != discordgo.InteractionApplicationCommand && i.Type != discordgo.InteractionMessageComponent {
		return
	}

	// Same reasoning as OnMessageCreate: a redelivered interaction would
	// run the slash command a second time.
	if h.dedupe != nil && !h.dedupe.firstTime(i.ID) {
		slog.Warn("dropping duplicate interaction event", "interaction", i.ID)
		return
	}

	// Slash commands were exempt from the channel filter that messages go
	// through, so an instance configured for other channels still answered
	// them here — the bot appeared to reply twice to /status while
	// replying once to ordinary messages. Check before acknowledging,
	// so a foreign instance stays out of it entirely.
	if _, _, serves := h.channelPolicy(i.ChannelID); !serves {
		return
	}

	// Picking from the model menu answers the interaction itself — it must
	// not go through the deferred-ephemeral path below, which would leave
	// the click unacknowledged.
	if i.Type == discordgo.InteractionMessageComponent {
		if !h.handleModelSelect(s, i) {
			// An unanswered interaction shows the user a red "This
			// interaction failed", so answer even when we don't know it.
			slog.Warn("unhandled component interaction", "custom_id", i.MessageComponentData().CustomID)
			h.respondToComponent(s, i, "알 수 없는 동작입니다.")
		}
		return
	}

	// Acknowledge interaction immediately (Discord requires response within 3s).
	// Use ephemeral deferred response so nothing visible shows up — the
	// actual command output is sent via ChannelMessageSend.
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: discordgo.MessageFlagsEphemeral,
		},
	})
	if err != nil {
		slog.Warn("interaction ack failed", "error", err)
	}

	// Resolve user from guild member or DM user
	var userID string
	if i.Member != nil {
		userID = i.Member.User.ID
	} else if i.User != nil {
		userID = i.User.ID
	}

	// Build command content: "!name" + first string option (if any)
	cmdData := i.ApplicationCommandData()
	commandText := "!" + cmdData.Name
	for _, opt := range cmdData.Options {
		if opt.Type == discordgo.ApplicationCommandOptionString && opt.StringValue() != "" {
			commandText += " " + opt.StringValue()
			break // only the first string arg
		}
	}

	fake := &discordgo.MessageCreate{
		Message: &discordgo.Message{
			ChannelID: i.ChannelID,
			Content:   commandText,
			Author:    &discordgo.User{ID: userID},
		},
	}

	// Run the command (sends response via ChannelMessageSend)
	h.handleBuiltinCommand(s, fake)

	// Delete the deferred ephemeral response
	s.InteractionResponseDelete(i.Interaction)
}
