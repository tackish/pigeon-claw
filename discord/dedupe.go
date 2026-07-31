package discord

import (
	"sync"
	"time"
)

// eventDedupe drops gateway events this process has already handled.
//
// Every Discord event carries a unique snowflake ID, so a repeat is always
// a redelivery, never a second user action — even two identical messages
// sent back to back get different IDs. Handling one twice is what makes a
// command run twice: /login starts two `claude setup-token` processes whose
// OAuth challenges compete, so the code the user pastes can only ever match
// one of them and the other sits silent.
//
// Redelivery has several causes — a reconnect that leaves the previous
// websocket draining, a resumed session replaying its backlog, a handler
// registered more than once — and they are hard to tell apart from inside
// the process. Keying on the event ID covers all of them.
//
// This is per-process. Two bots on one token each dedupe their own stream
// and both still answer; that is what the instance lock and the host/pid in
// !status are for.
type eventDedupe struct {
	mu   sync.Mutex
	seen map[string]time.Time
	ttl  time.Duration
}

func newEventDedupe(ttl time.Duration) *eventDedupe {
	return &eventDedupe{seen: make(map[string]time.Time), ttl: ttl}
}

// firstTime reports whether id is being handled for the first time, and
// records it. An empty id is always treated as new — better to act twice
// than to swallow every event that lacks one.
func (d *eventDedupe) firstTime(id string) bool {
	if id == "" {
		return true
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	now := time.Now()
	if seenAt, ok := d.seen[id]; ok && now.Sub(seenAt) < d.ttl {
		return false
	}

	// Evict expired entries here rather than on a timer: the map only
	// grows on traffic, so that is exactly when it needs trimming.
	if len(d.seen) > 256 {
		for k, t := range d.seen {
			if now.Sub(t) >= d.ttl {
				delete(d.seen, k)
			}
		}
	}

	d.seen[id] = now
	return true
}
