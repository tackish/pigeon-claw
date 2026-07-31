package discord

import (
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/tackish/pigeon-claw/provider"
)

// recordingSender captures what the login flow would post to Discord.
type recordingSender struct {
	mu   sync.Mutex
	msgs []string
}

func (r *recordingSender) ChannelMessageSend(_ string, content string, _ ...discordgo.RequestOption) (*discordgo.Message, error) {
	r.mu.Lock()
	r.msgs = append(r.msgs, content)
	r.mu.Unlock()
	return &discordgo.Message{}, nil
}

func (r *recordingSender) all() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.msgs...)
}

// waitFor polls until some captured message satisfies match, or the
// deadline passes. Returns the matching message.
func (r *recordingSender) waitFor(d time.Duration, match func(string) bool) (string, bool) {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		for _, m := range r.all() {
			if match(m) {
				return m, true
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return "", false
}

// TestLoginFlowEndToEnd drives the real thing: spawn `claude setup-token`
// under a pty, wait for the sign-in URL to be relayed, submit a code the
// way /code does, and require that the CLI's verdict comes back to the
// channel.
//
// This covers the plumbing the unit tests can't — pty wiring, the reader
// goroutine, and the write in handleLoginCode — which is exactly where a
// login that goes silent after "코드 확인 중..." would be hiding.
//
// The submitted code is deliberately invalid, so no token can be minted
// and the machine's real credentials are untouched; the CLI answers with
// an OAuth error, which is the reaction under test.
func TestLoginFlowEndToEnd(t *testing.T) {
	requireClaudeCLI(t)
	runLoginFlow(t)
}

// TestLoginFlowDaemonEnv runs the same flow under the environment launchd
// actually gives the bot — only HOME and PATH, no terminal variables.
// That matters because the CLI decides whether to emit OSC-8 hyperlinks
// from those variables: without them the sign-in URL arrives as plain
// text, which the flow used to miss entirely, relaying nothing at all.
//
// Re-execs the test binary because the environment has to be stripped for
// the whole process, not just the spawned CLI.
func TestLoginFlowDaemonEnv(t *testing.T) {
	requireClaudeCLI(t)

	if os.Getenv("PIGEON_LOGIN_E2E_CHILD") != "" {
		runLoginFlow(t)
		return
	}

	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory: %v", err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestLoginFlowDaemonEnv", "-test.v")
	// Exactly what com.tek.pigeon-claw.plist passes — no TERM, no
	// TERM_PROGRAM, nothing a terminal emulator would set.
	cmd.Env = []string{
		"PIGEON_LOGIN_E2E_CHILD=1",
		"HOME=" + home,
		"PATH=/usr/local/bin:/usr/bin:/bin:/opt/homebrew/bin",
	}
	out, err := cmd.CombinedOutput()
	t.Logf("child output:\n%s", out)
	if err != nil {
		t.Fatalf("login flow failed under the daemon's environment: %v", err)
	}
}

func requireClaudeCLI(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("spawns the claude CLI and talks to Anthropic's OAuth endpoint")
	}
	if _, err := os.Stat(provider.FindClaudeBin()); err != nil {
		if _, lookErr := exec.LookPath("claude"); lookErr != nil {
			t.Skip("claude CLI not installed")
		}
	}
}

func runLoginFlow(t *testing.T) {
	t.Helper()

	h := &Handler{}
	sender := &recordingSender{}

	h.handleLogin(sender, "test-channel")
	defer h.cancelActiveLogin(sender)

	// 1. The sign-in URL must reach the channel. Before the plain-text
	//    fallback this step silently never happened under a daemon
	//    environment, where the CLI emits no OSC-8 hyperlink.
	urlMsg, ok := sender.waitFor(90*time.Second, func(m string) bool {
		return strings.Contains(m, "https://claude.com/")
	})
	if !ok {
		t.Fatalf("sign-in URL never relayed within 90s; messages so far: %q", sender.all())
	}
	if !strings.Contains(urlMsg, "code_challenge=") {
		t.Fatalf("relayed URL lacks OAuth parameters, browser flow would mint no code:\n%s", urlMsg)
	}

	// 2. Submit a code exactly as /code does.
	h.handleLoginCode(sender, "test-channel", "bogus-authorization-code-for-test#state")

	if _, ok := sender.waitFor(5*time.Second, func(m string) bool {
		return strings.Contains(m, "코드 확인 중")
	}); !ok {
		t.Fatalf("code submission not acknowledged; messages: %q", sender.all())
	}

	// 3. The CLI's verdict must come back. Silence here is the reported
	//    bug: the channel sits on "코드 확인 중..." until the timeout.
	verdict, ok := sender.waitFor(45*time.Second, func(m string) bool {
		return strings.Contains(m, "코드가 거부되었습니다") || // relayed CLI error
			strings.Contains(m, "응답이 없습니다") || // silence watchdog fired
			strings.Contains(m, "재인증 완료") // (not expected for a bogus code)
	})
	if !ok {
		t.Fatalf("no reaction to the submitted code within 45s — this is the "+
			"'/code 후 무반응' symptom.\nmessages: %q", sender.all())
	}
	t.Logf("CLI verdict relayed: %s", verdict)

	if strings.Contains(verdict, "재인증 완료") {
		t.Fatalf("a bogus code must never mint a token: %s", verdict)
	}
}
