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

func TestSteerSessionSteerWritesMessage(t *testing.T) {
	stdin := &fakeStdin{}
	ss := &steerSession{stdin: stdin}

	if !ss.steer("follow-up") {
		t.Fatal("steer should succeed on a live session")
	}
	if !strings.Contains(stdin.buf.String(), `"follow-up"`) {
		t.Fatalf("stdin missing steered message: %q", stdin.buf.String())
	}
	if stdin.closed {
		t.Fatal("steer must not close stdin")
	}
}

func TestSteerSessionClosesOnResult(t *testing.T) {
	stdin := &fakeStdin{}
	ss := &steerSession{stdin: stdin}

	// A response is out — request complete, stdin closes immediately.
	ss.shutdown()
	if !stdin.closed {
		t.Fatal("stdin should close as soon as a result arrives")
	}
}

func TestSteerSessionSteerAfterCloseFails(t *testing.T) {
	stdin := &fakeStdin{}
	ss := &steerSession{stdin: stdin}

	ss.shutdown()
	if ss.steer("too late") {
		t.Fatal("steer should fail after session closed")
	}
}

func TestSteerSessionShutdownIsIdempotent(t *testing.T) {
	stdin := &fakeStdin{}
	ss := &steerSession{stdin: stdin}

	ss.shutdown()
	ss.shutdown()
	if !stdin.closed {
		t.Fatal("shutdown should close stdin")
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
	ss := &steerSession{stdin: stdin}

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
