package provider

import (
	"context"
	"os/exec"
	"strings"
	"sync"
	"testing"
)

// streamJSONCmd builds a command that emits the given stream-json lines
// on stdout, standing in for the claude CLI.
func streamJSONCmd(lines ...string) *exec.Cmd {
	var sb strings.Builder
	for _, l := range lines {
		sb.WriteString(l)
		sb.WriteString("\n")
	}
	return exec.Command("bash", "-c", "cat <<'EOF'\n"+sb.String()+"EOF")
}

func TestExecuteCmdFailedRunReturnsError(t *testing.T) {
	c := NewClaudeCLI("test-model", "")
	// A failed run omits the result field entirely.
	cmd := streamJSONCmd(`{"type":"result","subtype":"error_max_turns","is_error":true}`)

	resp, err := c.executeCmd(context.Background(), cmd, nil, nil)
	if err == nil {
		t.Fatalf("expected an error for a run that produced no answer, got %+v", resp)
	}
	if !strings.Contains(err.Error(), "error_max_turns") {
		t.Fatalf("error should name the failure subtype, got: %v", err)
	}
}

func TestExecuteCmdEmptyResultDoesNotReportTurn(t *testing.T) {
	c := NewClaudeCLI("test-model", "")
	stdin := &fakeStdin{}
	ss := &steerSession{stdin: stdin}

	var statuses []string
	onStatus := func(s string) { statuses = append(statuses, s) }

	cmd := streamJSONCmd(`{"type":"result","subtype":"error_during_execution","is_error":true}`)
	if _, err := c.executeCmd(context.Background(), cmd, onStatus, ss); err == nil {
		t.Fatal("expected an error for a failed run")
	}

	for _, s := range statuses {
		if strings.HasPrefix(s, "TURN_RESULT:") {
			t.Fatalf("empty result must not be reported as a completed turn: %q", s)
		}
	}
	if !stdin.closed {
		t.Fatal("stdin should still close so the CLI exits")
	}
}

func TestExecuteCmdDeliversResultText(t *testing.T) {
	c := NewClaudeCLI("test-model", "")
	stdin := &fakeStdin{}
	ss := &steerSession{stdin: stdin}

	var turns []string
	onStatus := func(s string) {
		if strings.HasPrefix(s, "TURN_RESULT:") {
			turns = append(turns, strings.TrimPrefix(s, "TURN_RESULT:"))
		}
	}

	cmd := streamJSONCmd(`{"type":"result","subtype":"success","result":"hello","usage":{"input_tokens":3,"output_tokens":4}}`)
	resp, err := c.executeCmd(context.Background(), cmd, onStatus, ss)
	if err != nil {
		t.Fatalf("executeCmd: %v", err)
	}
	if resp.Content != "hello" {
		t.Fatalf("Content = %q, want %q", resp.Content, "hello")
	}
	if resp.Usage.TotalTokens != 7 {
		t.Fatalf("TotalTokens = %d, want 7", resp.Usage.TotalTokens)
	}
	if len(turns) != 1 || turns[0] != "hello" {
		t.Fatalf("turn results = %v, want [hello]", turns)
	}
}

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

func TestModelArgsOmittedWhenUnset(t *testing.T) {
	// Nothing pinned: the CLI must be left to run its own configured model.
	if args := (&ClaudeCLI{}).modelArgs(); len(args) != 0 {
		t.Fatalf("no model configured should pass no flags, got %v", args)
	}

	args := (&ClaudeCLI{modelSetting: newModelSetting("opus"), fallback: "sonnet"}).modelArgs()
	want := []string{"--model", "opus", "--fallback-model", "sonnet"}
	if strings.Join(args, " ") != strings.Join(want, " ") {
		t.Fatalf("modelArgs = %v, want %v", args, want)
	}

	// A fallback alone must not smuggle a model choice back in.
	args = (&ClaudeCLI{fallback: "sonnet"}).modelArgs()
	if strings.Join(args, " ") != "--fallback-model sonnet" {
		t.Fatalf("fallback-only args = %v", args)
	}
}

func TestExecuteCmdReportsModelThatAnswered(t *testing.T) {
	c := NewClaudeCLI("", "")
	cmd := streamJSONCmd(
		`{"type":"assistant","message":{"model":"claude-opus-4-8","content":[{"type":"text","text":"hi"}]}}`,
		`{"type":"result","subtype":"success","result":"hi"}`,
	)

	resp, err := c.executeCmd(context.Background(), cmd, nil, nil)
	if err != nil {
		t.Fatalf("executeCmd: %v", err)
	}
	if resp.Model != "claude-opus-4-8" {
		t.Fatalf("Model = %q, want the model from the stream", resp.Model)
	}
}

// The /model picker changes the model from the Discord event goroutine while
// a request may be building its argv on another. Run with -race.
func TestSetModelIsSafeDuringRun(t *testing.T) {
	c := NewClaudeCLI("claude-opus-5", "")
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			c.SetModel("claude-sonnet-5")
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			_ = c.modelArgs()
			_ = c.Model()
		}
	}()
	wg.Wait()
}
