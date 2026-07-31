package discord

import (
	"os"
	"strings"
	"testing"
)

var osReadFile = os.ReadFile

// Real pty capture shapes from `claude setup-token`.

func TestOSC8URLExtraction(t *testing.T) {
	sample := "Browser didn't open? Use the url below to sign in (c to copy)\r\n\r\n" +
		"\x1b]8;id=1k5qrrx;https://claude.com/cai/oauth/authorize?code=true&state=xyz\x1b\\visible-wrapped-text\x1b]8;;\x1b\\"
	m := osc8URLRe.FindStringSubmatch(sample)
	if m == nil {
		t.Fatal("no URL match")
	}
	if m[1] != "https://claude.com/cai/oauth/authorize?code=true&state=xyz" {
		t.Fatalf("wrong URL: %q", m[1])
	}
	// The empty-URL hyperlink terminator must not match.
	if osc8URLRe.MatchString("\x1b]8;;\x1b\\") {
		t.Fatal("matched empty hyperlink terminator")
	}
}

// A representative sign-in URL as claude CLI 2.1.172 emits it.
const signInURL = "https://claude.com/cai/oauth/authorize?code=true&client_id=9d1c250a-e61b-44d9-88ed-5944d1962f5e" +
	"&response_type=code&redirect_uri=https%3A%2F%2Fplatform.claude.com%2Foauth%2Fcode%2Fcallback" +
	"&scope=user%3Ainference&code_challenge=OHMkEeWbBmvkuezVyjGh0RE60ZvpVm0L58Vd8gA-9LQ" +
	"&code_challenge_method=S256&state=No1VOy5fsFNBRr2u06rPu17jyjxzjRTJU9TRq51QjWl"

// TestFindSignInURLPlainText is the regression test for logins that never
// produced a URL. The CLI emits OSC-8 hyperlinks only when it believes the
// terminal supports them, which it infers from variables a terminal
// emulator sets. Under launchd — how the bot actually runs — none are set
// and the URL arrives as plain text, so matching only the escape form
// relayed nothing at all.
func TestFindSignInURLPlainText(t *testing.T) {
	raw := "Welcome to Claude Code v2.1.172\r\n\r\n" +
		"Browser didn't open? Use the url below to sign in\r\n\r\n  " + signInURL + "\r\n\r\n" +
		"Paste code here if prompted > "

	if got := findSignInURL(raw); got != signInURL {
		t.Fatalf("plain-text URL not found\n got: %q\nwant: %q", got, signInURL)
	}
}

func TestFindSignInURLOSC8(t *testing.T) {
	raw := "\x1b]8;id=1k5qrrx;" + signInURL + "\x1b\\visible-wrapped-text\x1b]8;;\x1b\\"

	if got := findSignInURL(raw); got != signInURL {
		t.Fatalf("OSC-8 URL not found\n got: %q\nwant: %q", got, signInURL)
	}
}

// TestFindSignInURLIgnoresBanner guards against relaying the welcome
// banner's link, which carries no OAuth parameters and yields no code.
func TestFindSignInURLIgnoresBanner(t *testing.T) {
	raw := "Welcome to Claude Code\r\nDocs: https://claude.com/product/claude-code\r\nStarting sign-in...\r\n"

	if got := findSignInURL(raw); got != "" {
		t.Fatalf("banner link must not be relayed, got %q", got)
	}
}

// TestFindSignInURLWaitsForCompleteQuery covers a URL still streaming in:
// relaying a truncated one sends the user to a page that mints no code.
func TestFindSignInURLWaitsForCompleteQuery(t *testing.T) {
	partial := signInURL[:strings.Index(signInURL, "&code_challenge=")]

	if got := findSignInURL("Use the url below to sign in\r\n  " + partial); got != "" {
		t.Fatalf("incomplete URL must not be relayed, got %q", got)
	}
}

// TestFindSignInURLThroughANSI covers the URL being split by colour codes
// or cursor movement, which a redrawing TUI emits mid-line.
func TestFindSignInURLThroughANSI(t *testing.T) {
	split := strings.Index(signInURL, "&scope=")
	raw := "  \x1b[36m" + signInURL[:split] + "\x1b[0m\x1b[36m" + signInURL[split:] + "\x1b[0m\r\n"

	if got := findSignInURL(raw); got != signInURL {
		t.Fatalf("URL split by escapes not reassembled\n got: %q\nwant: %q", got, signInURL)
	}
}

func TestLoginOutputSnapshotShowsRecentState(t *testing.T) {
	raw := "\x1b[2K\rWaiting for authorization...\r\n\x1b[32mStill waiting\x1b[0m\r\n"

	got := loginOutputSnapshot(raw)
	for _, want := range []string{"Waiting for authorization", "Still waiting"} {
		if !strings.Contains(got, want) {
			t.Fatalf("snapshot missing %q, got %q", want, got)
		}
	}
	if strings.Contains(got, "\x1b") {
		t.Fatalf("snapshot must be escape-free, got %q", got)
	}
}

func TestOATTokenExtraction(t *testing.T) {
	sample := "\x1b[32m✓\x1b[0m Long-lived authentication token created\r\n sk-ant-oat01-AbCd_efGH-1234567890 \r\n(copied)"
	tok := oatTokenRe.FindString(sample)
	if tok != "sk-ant-oat01-AbCd_efGH-1234567890" {
		t.Fatalf("wrong token: %q", tok)
	}
}

func TestExtractLoginFeedback(t *testing.T) {
	// Cleaned-up reproduction of the CLI's bad-code reaction, with masked
	// echo and spinner frames interleaved (as captured live).
	raw := "\x1b[2K✻\r\x1b[1A************************t-real\r\n" +
		"\x1b[31mOAuth error: Request failed with status code 400\x1b[0m\r\n" +
		"Press Enter to retry.\r\n✽\r"
	got := extractLoginFeedback(raw)
	if !strings.Contains(got, "OAuth error: Request failed with status code 400") {
		t.Fatalf("missing error line, got %q", got)
	}
	if !strings.Contains(got, "Press Enter to retry.") {
		t.Fatalf("missing retry line, got %q", got)
	}
	// Masked code echo must never be relayed (leaks code tail).
	if strings.Contains(got, "***") {
		t.Fatalf("leaked masked echo: %q", got)
	}
}

func TestExtractLoginFeedbackQuietOnSuccess(t *testing.T) {
	raw := "\x1b[2K✻ some spinner\r\n✓ Long-lived authentication token created\r\n"
	if got := extractLoginFeedback(raw); got != "" {
		t.Fatalf("expected no feedback on success path, got %q", got)
	}
}

func TestUpsertEnvVar(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/config"

	// Append to a fresh file.
	if err := upsertEnvVar(path, "CLAUDE_CODE_OAUTH_TOKEN", "tok1"); err != nil {
		t.Fatal(err)
	}
	// Preserve other keys, replace existing.
	if err := upsertEnvVar(path, "OTHER", "x"); err != nil {
		t.Fatal(err)
	}
	if err := upsertEnvVar(path, "CLAUDE_CODE_OAUTH_TOKEN", "tok2"); err != nil {
		t.Fatal(err)
	}
	data, _ := readFileString(path)
	if !strings.Contains(data, "CLAUDE_CODE_OAUTH_TOKEN=tok2") || strings.Contains(data, "tok1") {
		t.Fatalf("replace failed: %q", data)
	}
	if !strings.Contains(data, "OTHER=x") {
		t.Fatalf("lost other key: %q", data)
	}
	if strings.Count(data, "CLAUDE_CODE_OAUTH_TOKEN=") != 1 {
		t.Fatalf("duplicated key: %q", data)
	}
}

func readFileString(path string) (string, error) {
	b, err := osReadFile(path)
	return string(b), err
}
