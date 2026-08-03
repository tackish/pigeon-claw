package discord

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/tackish/pigeon-claw/update"
)

// sendStatus answers !status (and its !debug alias) with everything known
// about this channel: which process replied, what is running or waiting,
// which providers and models are configured, and the last request/error.
//
// This used to be three commands — !status, !provider and !debug — that
// overlapped so heavily you had to run all three to get a whole picture.
func (h *Handler) sendStatus(s *discordgo.Session, channelID string) {
	sess := h.router.GetSessions().GetOrCreate(channelID)

	// Host, PID and version identify which process answered. Every instance
	// on the token replies, so two answers here means two bots are live —
	// the thing that makes every command run twice. A machine-local lock
	// cannot catch a duplicate on another host, so surface it instead.
	host, err := os.Hostname()
	if err != nil {
		host = "unknown"
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "**Status** — `%s` pid %d, v%s\n", host, os.Getpid(), update.Current())
	fmt.Fprintf(&sb, "- Channel: `%s`\n", channelID)
	fmt.Fprintf(&sb, "- Messages: %d\n", sess.MessageCount())
	fmt.Fprintf(&sb, "- CLI Session: `%s`\n", orNone(sess.GetCLISessionID()))

	// A queued message is answered with nothing but a 📥 reaction, so this
	// is the only place the user can see it is still coming.
	if active, ok := h.activeRequests.Load(channelID); ok {
		preview, _ := active.(string)
		if len(preview) > 60 {
			preview = preview[:60] + "..."
		}
		fmt.Fprintf(&sb, "- 처리 중: `%s`\n", preview)
	}
	if waiting := h.queueFor(channelID).depth(); waiting > 0 {
		fmt.Fprintf(&sb, "- 대기 중: %d개\n", waiting)
	}

	sb.WriteString("\n**Providers** (우선순위 순)\n")
	active := sess.GetActiveProvider()
	for i, p := range h.router.GetProviders() {
		marker := ""
		if p.Name() == active {
			marker = " ← active"
		}
		fmt.Fprintf(&sb, "%d. %s (`%s`)%s\n", i+1, p.Name(), displayModel(p.Model()), marker)
	}

	if debug := h.router.GetDebug(channelID); debug != nil {
		if !debug.LastRequestAt.IsZero() {
			sb.WriteString("\n**Last Request**\n")
			fmt.Fprintf(&sb, "- Time: %s\n", debug.LastRequestAt.Format("2006-01-02 15:04:05"))
			fmt.Fprintf(&sb, "- Message: `%s`\n", debug.LastRequestMsg)
			if !debug.LastCompleteAt.IsZero() {
				elapsed := debug.LastCompleteAt.Sub(debug.LastRequestAt).Truncate(time.Second)
				fmt.Fprintf(&sb, "- Completed: %s (%s, %d tokens)\n",
					debug.LastCompleteAt.Format("15:04:05"), elapsed, debug.LastTokens)
			} else {
				fmt.Fprintf(&sb, "- Status: **처리 중** (%s 경과)\n",
					time.Since(debug.LastRequestAt).Truncate(time.Second))
			}
		}
		if debug.LastError != "" {
			sb.WriteString("\n**Last Error**\n")
			fmt.Fprintf(&sb, "- Provider: `%s`\n", debug.LastProvider)
			fmt.Fprintf(&sb, "- Time: %s\n", debug.LastErrorAt.Format("2006-01-02 15:04:05"))
			fmt.Fprintf(&sb, "- Error:\n```\n%s\n```\n", debug.LastError)
		}
	}

	sent, err := s.ChannelMessageSend(channelID, sb.String())
	// Read the channel back: a second reply to this same command is a
	// duplicate instance, which no machine-local check can see.
	if err == nil && sent != nil {
		go h.checkForTwin(s, channelID, sent.ID, "**Status**")
	}
}

// orNone keeps an empty value from rendering as an empty code span.
func orNone(v string) string {
	if v == "" {
		return "none"
	}
	return v
}
