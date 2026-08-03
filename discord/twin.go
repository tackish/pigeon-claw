package discord

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

// Detecting a second bot on the same token.
//
// A duplicate is invisible from inside a process: both instances share one
// bot user, and each ignores the other's messages as its own. It is also
// hard to spot from the machine — the twin may run under a name `pgrep
// pigeon-claw` misses (a `go run` temp binary, a debugger build) or on a
// host nobody thought to check. What it cannot hide is Discord itself:
// both answer every command, so the channel ends up with two replies.
//
// So ask the channel. Post a reply, then read back what actually landed:
// another message from our bot user, in the same window, that we did not
// send, is a twin — proof from the only place both are visible.

// twinProbeDelay is how long to wait before reading the channel back. The
// twin's reply has to have landed and Discord has to have made it
// readable; a second covers both without making /status feel sluggish.
const twinProbeDelay = 2 * time.Second

// twinProbeWindow bounds how far back a message may be and still count as
// a reply to this command rather than an older one.
const twinProbeWindow = 30 * time.Second

// checkForTwin looks for another instance's answer to the same command and
// reports it to the channel. ownID is the message this instance just sent;
// prefix is the leading text both versions' replies share.
//
// Failures are logged, not reported: a missed detection is a lost warning,
// while a false alarm sends the user hunting for a process that is not
// there.
func (h *Handler) checkForTwin(s *discordgo.Session, channelID, ownID, prefix string) {
	time.Sleep(twinProbeDelay)

	recent, err := s.ChannelMessages(channelID, 20, "", "", "")
	if err != nil {
		slog.Warn("twin check: cannot read channel", "error", err)
		return
	}

	selfID := ""
	if s.State != nil && s.State.User != nil {
		selfID = s.State.User.ID
	}
	if selfID == "" {
		return // cannot tell our own messages apart; no basis to warn
	}

	twins := countTwinReplies(recent, selfID, ownID, prefix, time.Now())
	if twins == 0 {
		return
	}

	slog.Warn("duplicate bot instance detected", "channel", channelID, "twin_replies", twins)
	s.ChannelMessageSend(channelID, fmt.Sprintf(
		"⚠️ **봇이 %d개 실행 중입니다.** 같은 명령에 %d개가 응답했습니다.\n"+
			"모든 명령이 중복 실행되고, 같은 요청이 두 번 처리됩니다.\n"+
			"찾는 법 — 이름이 `pigeon-claw` 가 아닐 수 있습니다:\n"+
			"```\npgrep -fl 'pigeon[-_]claw|go-build|__debug_bin|dlv'\n```\n"+
			"`!restart` 를 보내면 양쪽 모두 현재 바이너리로 재실행되어, "+
			"하나만 살아남고 나머지는 잠금에 걸려 종료됩니다.",
		twins+1, twins+1))
}

// countTwinReplies counts replies to the same command that this instance
// did not send. Split out from the fetch so the decision — which is where
// a false alarm would come from — is testable.
func countTwinReplies(recent []*discordgo.Message, selfID, ownID, prefix string, now time.Time) int {
	if selfID == "" {
		return 0
	}
	cutoff := now.Add(-twinProbeWindow)
	n := 0
	for _, m := range recent {
		if m == nil || m.ID == ownID || m.Author == nil || m.Author.ID != selfID {
			continue
		}
		if !strings.HasPrefix(m.Content, prefix) {
			continue
		}
		if m.Timestamp.Before(cutoff) {
			continue // an earlier invocation, not a twin's reply
		}
		n++
	}
	return n
}
