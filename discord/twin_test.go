package discord

import (
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
)

const (
	selfUser  = "bot-user-1"
	otherUser = "human-2"
)

func msg(id, author, content string, age time.Duration) *discordgo.Message {
	return &discordgo.Message{
		ID:        id,
		Author:    &discordgo.User{ID: author},
		Content:   content,
		Timestamp: time.Now().Add(-age),
	}
}

// TestCountTwinRepliesFindsDuplicate is the case that matters: two bots
// answered the same command, so the channel holds a reply we did not send.
func TestCountTwinRepliesFindsDuplicate(t *testing.T) {
	recent := []*discordgo.Message{
		msg("mine", selfUser, "**Status** — hostA pid 1, v0.0.49", time.Second),
		msg("theirs", selfUser, "**Status**\n- Active Provider:", 2*time.Second),
	}

	if got := countTwinReplies(recent, selfUser, "mine", "**Status**", time.Now()); got != 1 {
		t.Fatalf("twin replies = %d, want 1", got)
	}
}

// TestCountTwinRepliesAloneIsQuiet: a single instance must never warn.
// A false alarm sends the user hunting for a process that is not there.
func TestCountTwinRepliesAloneIsQuiet(t *testing.T) {
	recent := []*discordgo.Message{
		msg("mine", selfUser, "**Status** — hostA pid 1, v0.0.49", time.Second),
		msg("human", otherUser, "**Status** 좀 보여줘", time.Second),
		msg("other-cmd", selfUser, "-# 재시작 중...", 3*time.Second),
	}

	if got := countTwinReplies(recent, selfUser, "mine", "**Status**", time.Now()); got != 0 {
		t.Fatalf("twin replies = %d, want 0 (no duplicate present)", got)
	}
}

// TestCountTwinRepliesIgnoresOldInvocation: our own reply from a previous
// /status minutes ago is not a twin.
func TestCountTwinRepliesIgnoresOldInvocation(t *testing.T) {
	recent := []*discordgo.Message{
		msg("mine", selfUser, "**Status** — hostA pid 1, v0.0.49", time.Second),
		msg("earlier", selfUser, "**Status** — hostA pid 1, v0.0.49", 10*time.Minute),
	}

	if got := countTwinReplies(recent, selfUser, "mine", "**Status**", time.Now()); got != 0 {
		t.Fatalf("twin replies = %d, want 0 (older invocation, not a twin)", got)
	}
}

func TestCountTwinRepliesCountsMultiple(t *testing.T) {
	const notice = "**Status** — hostA pid 1, v0.0.49"
	recent := []*discordgo.Message{
		msg("mine", selfUser, notice, time.Second),
		msg("t1", selfUser, notice, time.Second),
		msg("t2", selfUser, notice, time.Second),
	}

	if got := countTwinReplies(recent, selfUser, "mine", notice, time.Now()); got != 2 {
		t.Fatalf("twin replies = %d, want 2", got)
	}
}

// TestCountTwinRepliesNoSelfIDStaysQuiet: without knowing which messages
// are ours, every reply would look like a twin.
func TestCountTwinRepliesNoSelfIDStaysQuiet(t *testing.T) {
	recent := []*discordgo.Message{
		msg("mine", selfUser, "**Status** x", time.Second),
		msg("theirs", selfUser, "**Status** y", time.Second),
	}

	if got := countTwinReplies(recent, "", "mine", "**Status**", time.Now()); got != 0 {
		t.Fatalf("twin replies = %d, want 0 when self is unknown", got)
	}
}

func TestCountTwinRepliesToleratesNilEntries(t *testing.T) {
	recent := []*discordgo.Message{
		nil,
		{ID: "no-author", Content: "**Status** x", Timestamp: time.Now()},
		msg("mine", selfUser, "**Status** ours", time.Second),
	}

	if got := countTwinReplies(recent, selfUser, "mine", "**Status**", time.Now()); got != 0 {
		t.Fatalf("twin replies = %d, want 0", got)
	}
}
