package provider

import (
	"strings"
	"testing"
	"time"
)

// fakeStdin records writes and close calls for steerSession tests.
type fakeStdin struct {
	buf    strings.Builder
	closed bool
}

func (f *fakeStdin) Write(p []byte) (int, error) {
	return f.buf.Write(p)
}

func (f *fakeStdin) Close() error {
	f.closed = true
	return nil
}

// stdinClosed reads fakeStdin.closed under ss.mu — the deferred close
// fires from a timer goroutine.
func stdinClosed(ss *steerSession, stdin *fakeStdin) bool {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	return stdin.closed
}

func TestSteerSessionDrainedClosesImmediately(t *testing.T) {
	stdin := &fakeStdin{}
	ss := &steerSession{stdin: stdin, writes: 1}

	// One message written, one result — definitely drained, no grace.
	ss.turnResult()
	if !stdin.closed {
		t.Fatal("stdin should close when results catch up with writes")
	}
}

func TestSteerSessionSteerWritesMessage(t *testing.T) {
	stdin := &fakeStdin{}
	ss := &steerSession{stdin: stdin, writes: 1}

	if !ss.steer("follow-up") {
		t.Fatal("steer should succeed on a live session")
	}
	if ss.writes != 2 {
		t.Fatalf("writes = %d, want 2", ss.writes)
	}
	if !strings.Contains(stdin.buf.String(), `"follow-up"`) {
		t.Fatalf("stdin missing steered message: %q", stdin.buf.String())
	}

	// Both turns answered — stdin closes so the CLI exits.
	ss.turnResult()
	if stdin.closed {
		t.Fatal("stdin closed while a steered message may still be pending")
	}
	ss.turnResult()
	if !stdin.closed {
		t.Fatal("stdin should close when results catch up with writes")
	}
}

func TestSteerSessionMergedTurnClosesAfterGrace(t *testing.T) {
	oldGrace := steerDrainGrace
	steerDrainGrace = 20 * time.Millisecond
	defer func() { steerDrainGrace = oldGrace }()

	stdin := &fakeStdin{}
	ss := &steerSession{stdin: stdin, writes: 1}

	// Steered mid-turn; the CLI merges it into the running turn and
	// answers both with a single result (observed CLI behavior).
	if !ss.steer("merged follow-up") {
		t.Fatal("steer should succeed on a live session")
	}
	ss.turnResult() // results=1 < writes=2 → deferred close armed

	if stdinClosed(ss, stdin) {
		t.Fatal("stdin closed before the drain grace elapsed")
	}
	deadline := time.Now().Add(2 * time.Second)
	for !stdinClosed(ss, stdin) {
		if time.Now().After(deadline) {
			t.Fatal("stdin should close after the drain grace with no activity")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestSteerSessionQueuedTurnCancelsDeferredClose(t *testing.T) {
	oldGrace := steerDrainGrace
	steerDrainGrace = 30 * time.Millisecond
	defer func() { steerDrainGrace = oldGrace }()

	stdin := &fakeStdin{}
	ss := &steerSession{stdin: stdin, writes: 1}

	// Steered message was queued, not merged: after the first result
	// the CLI starts another turn, whose stream events cancel the
	// deferred close.
	ss.steer("queued follow-up")
	ss.turnResult() // deferred close armed
	ss.activity()   // next turn's first event

	time.Sleep(100 * time.Millisecond)
	if stdinClosed(ss, stdin) {
		t.Fatal("activity should cancel the deferred close")
	}

	ss.turnResult() // results=2 >= writes=2 → close now
	if !stdinClosed(ss, stdin) {
		t.Fatal("stdin should close once the queued turn's result arrives")
	}
}

func TestSteerSessionSteerCancelsDeferredClose(t *testing.T) {
	oldGrace := steerDrainGrace
	steerDrainGrace = 30 * time.Millisecond
	defer func() { steerDrainGrace = oldGrace }()

	stdin := &fakeStdin{}
	ss := &steerSession{stdin: stdin, writes: 1}

	ss.steer("first follow-up")
	ss.turnResult() // deferred close armed
	if !ss.steer("second follow-up") {
		t.Fatal("steer should succeed while the deferred close is pending")
	}

	time.Sleep(100 * time.Millisecond)
	if stdinClosed(ss, stdin) {
		t.Fatal("a new steer should cancel the deferred close")
	}
}

func TestSteerSessionSteerAfterCloseFails(t *testing.T) {
	stdin := &fakeStdin{}
	ss := &steerSession{stdin: stdin, writes: 1}

	ss.turnResult() // drained → closed
	if ss.steer("too late") {
		t.Fatal("steer should fail after session closed")
	}
}

func TestSteerSessionShutdownIsIdempotent(t *testing.T) {
	stdin := &fakeStdin{}
	ss := &steerSession{stdin: stdin, writes: 1}

	ss.shutdown()
	ss.shutdown()
	if !stdin.closed {
		t.Fatal("shutdown should close stdin")
	}
	if ss.steer("x") {
		t.Fatal("steer should fail after shutdown")
	}
}

func TestClaudeCLISteerUnknownSession(t *testing.T) {
	c := NewClaudeCLI("test-model", "")
	if c.Steer("no-such-session", "hello") {
		t.Fatal("Steer should fail for unknown session")
	}
}

func TestSteerSessionWriteUserMessageFormat(t *testing.T) {
	stdin := &fakeStdin{}
	ss := &steerSession{stdin: stdin, writes: 1}

	ss.mu.Lock()
	err := ss.writeUserMessage("hello world")
	ss.mu.Unlock()
	if err != nil {
		t.Fatalf("writeUserMessage: %v", err)
	}

	line := stdin.buf.String()
	if !strings.HasSuffix(line, "\n") {
		t.Fatal("stream-json message must be newline-terminated")
	}
	for _, want := range []string{`"type":"user"`, `"role":"user"`, `"text":"hello world"`} {
		if !strings.Contains(line, want) {
			t.Fatalf("message %q missing %s", line, want)
		}
	}
}
