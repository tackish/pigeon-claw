package provider

import (
	"strings"
	"testing"
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

func TestSteerSessionSteerIncrementsPending(t *testing.T) {
	stdin := &fakeStdin{}
	ss := &steerSession{stdin: stdin, pending: 1}

	if !ss.steer("follow-up") {
		t.Fatal("steer should succeed on a live session")
	}
	if ss.pending != 2 {
		t.Fatalf("pending = %d, want 2", ss.pending)
	}
	if !strings.Contains(stdin.buf.String(), `"follow-up"`) {
		t.Fatalf("stdin missing steered message: %q", stdin.buf.String())
	}

	// First turn completes — one steered turn still pending, stdin stays open.
	ss.turnDone()
	if stdin.closed {
		t.Fatal("stdin closed while a turn is still pending")
	}

	// Last turn completes — stdin closes so the CLI exits.
	ss.turnDone()
	if !stdin.closed {
		t.Fatal("stdin should close when no turns remain")
	}
}

func TestSteerSessionSteerAfterCloseFails(t *testing.T) {
	stdin := &fakeStdin{}
	ss := &steerSession{stdin: stdin, pending: 1}

	ss.turnDone() // pending hits 0 → closed
	if ss.steer("too late") {
		t.Fatal("steer should fail after session closed")
	}
}

func TestSteerSessionShutdownIsIdempotent(t *testing.T) {
	stdin := &fakeStdin{}
	ss := &steerSession{stdin: stdin, pending: 1}

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
	ss := &steerSession{stdin: stdin, pending: 1}

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
