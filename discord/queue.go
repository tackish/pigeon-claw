package discord

import (
	"sync"

	"github.com/bwmarrin/discordgo"
)

// maxQueuedPerChannel bounds how many follow-ups wait behind the running
// request. Deep queues are worse than a refusal: by the time the tenth one
// runs, the user has usually moved on.
const maxQueuedPerChannel = 10

// channelQueue serializes the requests of one channel.
//
// A channel runs one request at a time, but a message that arrives while one
// is running must not be thrown away. Steering covers messages that arrive
// while the CLI can still take them; this covers the rest — most importantly
// the window between the answer being sent and the run actually finishing,
// where steering is already closed and the slot is still held. Those messages
// used to be answered with "still working, try again", which loses them.
//
// running doubles as the slot: it is set under the same mutex that guards the
// backlog, so a message can never be queued just after the runner decided the
// backlog was empty.
type channelQueue struct {
	mu      sync.Mutex
	running bool
	items   []*discordgo.MessageCreate
}

// admission tells the caller what happened to a message.
type admission int

const (
	runNow   admission = iota // caller owns the channel and must run it
	queued                    // will run when the current request finishes
	rejected                  // backlog is full
)

// admit claims the channel for m, or queues it behind the running request.
func (q *channelQueue) admit(m *discordgo.MessageCreate) admission {
	q.mu.Lock()
	defer q.mu.Unlock()

	if !q.running {
		q.running = true
		return runNow
	}
	if len(q.items) >= maxQueuedPerChannel {
		return rejected
	}
	q.items = append(q.items, m)
	return queued
}

// next returns the message to run after the current one, releasing the
// channel when the backlog is empty. Releasing here rather than in the caller
// is what makes the hand-off atomic.
func (q *channelQueue) next() *discordgo.MessageCreate {
	q.mu.Lock()
	defer q.mu.Unlock()

	if len(q.items) == 0 {
		q.running = false
		return nil
	}
	m := q.items[0]
	q.items = q.items[1:]
	return m
}

// drop discards the backlog and reports how many were waiting. Used by
// !cancel: cancelling the running request while its follow-ups quietly go
// ahead anyway is not what anyone means by cancel.
func (q *channelQueue) drop() int {
	q.mu.Lock()
	defer q.mu.Unlock()

	n := len(q.items)
	q.items = nil
	return n
}

// depth reports how many messages are waiting.
func (q *channelQueue) depth() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}

// queueFor returns the channel's queue, creating it on first use.
func (h *Handler) queueFor(channelID string) *channelQueue {
	q, _ := h.channelQueues.LoadOrStore(channelID, &channelQueue{})
	return q.(*channelQueue)
}
