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
