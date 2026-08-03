package discord

import (
	"sync"
	"testing"

	"github.com/bwmarrin/discordgo"
)

func msgWithID(id string) *discordgo.MessageCreate {
	return &discordgo.MessageCreate{Message: &discordgo.Message{ID: id}}
}

func TestQueueRunsFirstAndQueuesRest(t *testing.T) {
	q := &channelQueue{}

	if got := q.admit(msgWithID("a")); got != runNow {
		t.Fatalf("first message should run, got %v", got)
	}
	if got := q.admit(msgWithID("b")); got != queued {
		t.Fatalf("second message should queue, got %v", got)
	}

	next := q.next()
	if next == nil || next.ID != "b" {
		t.Fatalf("expected b to run next, got %v", next)
	}
	if q.next() != nil {
		t.Fatal("backlog should be empty")
	}
	// Draining released the channel, so a later message runs immediately.
	if got := q.admit(msgWithID("c")); got != runNow {
		t.Fatalf("channel should be free after draining, got %v", got)
	}
}

// The window this whole thing exists for: a message that arrives after the
// runner checked for more work must still be picked up, not dropped.
func TestQueueHandsOffWithoutLosingMessages(t *testing.T) {
	q := &channelQueue{}
	q.admit(msgWithID("running"))

	var wg sync.WaitGroup
	const senders = 8
	wg.Add(senders)
	for i := 0; i < senders; i++ {
		go func(i int) {
			defer wg.Done()
			q.admit(msgWithID(string(rune('a' + i))))
		}(i)
	}
	wg.Wait()

	seen := 0
	for q.next() != nil {
		seen++
	}
	if seen != senders {
		t.Fatalf("drained %d messages, want %d — a message was lost", seen, senders)
	}
}

func TestQueueRejectsWhenFull(t *testing.T) {
	q := &channelQueue{}
	q.admit(msgWithID("running"))

	for i := 0; i < maxQueuedPerChannel; i++ {
		if got := q.admit(msgWithID("x")); got != queued {
			t.Fatalf("message %d should queue, got %v", i, got)
		}
	}
	if got := q.admit(msgWithID("overflow")); got != rejected {
		t.Fatalf("a full backlog must reject rather than grow, got %v", got)
	}
}

func TestQueueDropClearsBacklog(t *testing.T) {
	q := &channelQueue{}
	q.admit(msgWithID("running"))
	q.admit(msgWithID("a"))
	q.admit(msgWithID("b"))

	if got := q.drop(); got != 2 {
		t.Fatalf("drop reported %d, want 2", got)
	}
	if q.depth() != 0 {
		t.Fatal("backlog should be empty after drop")
	}
	// Dropping the backlog must not release a channel that is still running.
	if got := q.admit(msgWithID("c")); got != queued {
		t.Fatalf("channel is still running, so c should queue, got %v", got)
	}
}
