package discord

import (
	"sync"
	"testing"
	"time"
)

func TestEventDedupeDropsRedelivery(t *testing.T) {
	d := newEventDedupe(time.Minute)

	if !d.firstTime("msg-1") {
		t.Fatal("first delivery must be handled")
	}
	if d.firstTime("msg-1") {
		t.Fatal("redelivery must be dropped — handling it runs the command twice")
	}
	if !d.firstTime("msg-2") {
		t.Fatal("a different event must still be handled")
	}
}

// TestEventDedupeEmptyIDAlwaysHandled: an event without an ID cannot be
// distinguished from any other, so acting twice beats swallowing every one.
func TestEventDedupeEmptyIDAlwaysHandled(t *testing.T) {
	d := newEventDedupe(time.Minute)

	for i := 0; i < 3; i++ {
		if !d.firstTime("") {
			t.Fatal("events without an ID must never be dropped")
		}
	}
}

// TestEventDedupeExpires keeps the map from being a permanent ledger: an ID
// reappearing long after the redelivery window is treated as new.
func TestEventDedupeExpires(t *testing.T) {
	d := newEventDedupe(20 * time.Millisecond)

	if !d.firstTime("msg-1") {
		t.Fatal("first delivery must be handled")
	}
	time.Sleep(40 * time.Millisecond)
	if !d.firstTime("msg-1") {
		t.Fatal("entry should have expired after the TTL")
	}
}

func TestEventDedupeEvictsExpiredEntries(t *testing.T) {
	d := newEventDedupe(10 * time.Millisecond)

	for i := 0; i < 300; i++ {
		d.firstTime(string(rune('a'+i%26)) + string(rune(i)))
	}
	time.Sleep(20 * time.Millisecond)
	// Crossing the eviction threshold again must reap the expired ones.
	for i := 0; i < 300; i++ {
		d.firstTime("second-" + string(rune(i)))
	}

	d.mu.Lock()
	size := len(d.seen)
	d.mu.Unlock()
	if size > 600 {
		t.Fatalf("map grew unbounded: %d entries", size)
	}
}

// TestEventDedupeConcurrent covers two goroutines racing on the same event,
// which is exactly the double-websocket case: only one may proceed.
func TestEventDedupeConcurrent(t *testing.T) {
	d := newEventDedupe(time.Minute)

	const racers = 16
	var wg sync.WaitGroup
	var mu sync.Mutex
	handled := 0

	wg.Add(racers)
	for i := 0; i < racers; i++ {
		go func() {
			defer wg.Done()
			if d.firstTime("same-id") {
				mu.Lock()
				handled++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if handled != 1 {
		t.Fatalf("%d goroutines handled the same event, want exactly 1", handled)
	}
}
