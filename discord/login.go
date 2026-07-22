package discord

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/creack/pty"
	"github.com/tackish/pigeon-claw/provider"
)

// Remote re-authentication for the claude CLI, driven entirely from Discord.
//
// `claude setup-token` is an interactive OAuth flow that REQUIRES a TTY: it
// prints a sign-in URL, waits for the user to paste an authorization code,
// then prints a long-lived (1-year) token. We attach it to a pty, relay the
// URL to Discord, accept the code via `/code`, capture the token, and persist
// it as CLAUDE_CODE_OAUTH_TOKEN — which `claude -p` prefers over the keychain
// login, so the bot keeps working even after the interactive session expires.
//
// os.Setenv makes it take effect immediately: the provider spawns claude with
// exec.Command, which inherits the parent environment, so no restart is needed.

const loginTimeout = 5 * time.Minute

var (
	// The canonical sign-in URL is embedded in an OSC-8 hyperlink escape
	// (ESC ] 8 ; params ; URI ST) — always complete and never line-wrapped,
	// unlike the visible copy the terminal also prints.
	osc8URLRe = regexp.MustCompile("\x1b\\]8;[^;]*;(https://[^\x1b\x07]+)")
	// Long-lived OAuth tokens look like sk-ant-oat01-...
	oatTokenRe = regexp.MustCompile(`sk-ant-oat[0-9]{2}-[A-Za-z0-9_-]+`)
)

type loginFlow struct {
	ptmx      *os.File
	cmd       *exec.Cmd
	channelID string
	cancel    chan struct{}
}

// handleLogin starts `claude setup-token` under a pty and begins relaying its
// output to Discord. Only one login may run at a time.
func (h *Handler) handleLogin(s *discordgo.Session, channelID string) {
	h.loginMu.Lock()
	if h.activeLogin != nil {
		h.loginMu.Unlock()
		s.ChannelMessageSend(channelID, "-# ⚠️ 이미 로그인이 진행 중입니다. 코드를 `/code <코드>` 로 보내거나 `/login-cancel` 로 취소하세요.")
		return
	}

	cmd := exec.Command(provider.FindClaudeBin(), "setup-token")
	if home, err := os.UserHomeDir(); err == nil {
		cmd.Dir = home
	}
	ptmx, err := pty.Start(cmd)
	if err != nil {
		h.loginMu.Unlock()
		s.ChannelMessageSend(channelID, fmt.Sprintf("-# ❌ 로그인 시작 실패: %s", err))
		return
	}
	// A wide terminal keeps the URL and token on single lines (no wrapping).
	_ = pty.Setsize(ptmx, &pty.Winsize{Rows: 50, Cols: 400})

	fl := &loginFlow{ptmx: ptmx, cmd: cmd, channelID: channelID, cancel: make(chan struct{})}
	h.activeLogin = fl
	h.loginMu.Unlock()

	s.ChannelMessageSend(channelID, "🔐 Claude 재인증을 시작합니다. 인증 URL을 기다리는 중...")

	go h.runLoginReader(s, fl)

	go func() {
		select {
		case <-time.After(loginTimeout):
			if h.claimLogin(fl) {
				teardownLogin(fl)
				s.ChannelMessageSend(fl.channelID, "-# ⌛ 로그인 시간 초과(5분)로 취소되었습니다. `/login` 으로 다시 시도하세요.")
			}
		case <-fl.cancel:
		}
	}()
}

// runLoginReader drains the pty, relaying the sign-in URL once and finishing
// as soon as a token appears (or the process exits).
func (h *Handler) runLoginReader(s *discordgo.Session, fl *loginFlow) {
	buf := make([]byte, 4096)
	var acc strings.Builder
	urlSent := false
	for {
		n, err := fl.ptmx.Read(buf)
		if n > 0 {
			acc.Write(buf[:n])
			raw := acc.String()
			if !urlSent {
				if m := osc8URLRe.FindStringSubmatch(raw); m != nil {
					urlSent = true
					s.ChannelMessageSend(fl.channelID, fmt.Sprintf(
						"🔗 아래 URL을 브라우저에서 열어 로그인한 뒤, 표시되는 코드를 `/code <코드>` 로 보내주세요:\n%s", m[1]))
				}
			}
			if tok := oatTokenRe.FindString(raw); tok != "" {
				if h.claimLogin(fl) {
					h.completeLogin(s, fl, tok)
				}
				return
			}
		}
		if err != nil {
			break
		}
	}

	// Process ended without us catching a token mid-stream — final sweep.
	if tok := oatTokenRe.FindString(acc.String()); tok != "" {
		if h.claimLogin(fl) {
			h.completeLogin(s, fl, tok)
		}
		return
	}
	if h.claimLogin(fl) {
		teardownLogin(fl)
		s.ChannelMessageSend(fl.channelID, "-# ❌ 로그인이 완료되지 않았습니다. `/login` 으로 다시 시도하세요.")
	}
}

// handleLoginCode feeds the pasted authorization code into the waiting pty.
func (h *Handler) handleLoginCode(s *discordgo.Session, channelID, code string) {
	h.loginMu.Lock()
	fl := h.activeLogin
	h.loginMu.Unlock()
	if fl == nil {
		s.ChannelMessageSend(channelID, "-# ⚠️ 진행 중인 로그인이 없습니다. 먼저 `/login` 을 실행하세요.")
		return
	}
	code = strings.TrimSpace(code)
	if code == "" {
		s.ChannelMessageSend(channelID, "-# 사용법: `/code <코드>`")
		return
	}
	if _, err := fl.ptmx.Write([]byte(code + "\r")); err != nil {
		s.ChannelMessageSend(channelID, fmt.Sprintf("-# ❌ 코드 전송 실패: %s", err))
		return
	}
	s.ChannelMessageSend(channelID, "-# ⏳ 코드 확인 중...")
}

// handleLoginCancel aborts an in-progress login.
func (h *Handler) handleLoginCancel(s *discordgo.Session, channelID string) {
	h.loginMu.Lock()
	fl := h.activeLogin
	h.loginMu.Unlock()
	if fl == nil {
		s.ChannelMessageSend(channelID, "-# 진행 중인 로그인이 없습니다.")
		return
	}
	if h.claimLogin(fl) {
		teardownLogin(fl)
		s.ChannelMessageSend(channelID, "-# 🚫 로그인을 취소했습니다.")
	}
}

// completeLogin persists the captured token and reports success. The caller
// must have already won claimLogin.
func (h *Handler) completeLogin(s *discordgo.Session, fl *loginFlow, token string) {
	teardownLogin(fl)
	if err := persistOAuthToken(token); err != nil {
		s.ChannelMessageSend(fl.channelID, fmt.Sprintf("-# ⚠️ 토큰은 발급됐지만 저장에 실패했습니다: %s", err))
		return
	}
	s.ChannelMessageSend(fl.channelID, fmt.Sprintf(
		"✅ 재인증 완료! 1년짜리 토큰이 발급·저장되었습니다 (`%s`). 즉시 적용되어 재시작이 필요 없습니다.", maskToken(token)))
}

// claimLogin lets exactly one terminal path (success / timeout / cancel /
// failure) proceed by atomically clearing the active login.
func (h *Handler) claimLogin(fl *loginFlow) bool {
	h.loginMu.Lock()
	defer h.loginMu.Unlock()
	if h.activeLogin != fl {
		return false
	}
	h.activeLogin = nil
	return true
}

func teardownLogin(fl *loginFlow) {
	close(fl.cancel)
	if fl.ptmx != nil {
		fl.ptmx.Close()
	}
	if fl.cmd != nil && fl.cmd.Process != nil {
		fl.cmd.Process.Kill()
	}
}

// persistOAuthToken sets CLAUDE_CODE_OAUTH_TOKEN for the running process (so
// new claude subprocesses inherit it immediately) and writes it to the config
// file so it survives restarts.
func persistOAuthToken(token string) error {
	os.Setenv("CLAUDE_CODE_OAUTH_TOKEN", token)
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	return upsertEnvVar(home+"/.pigeon-claw/config", "CLAUDE_CODE_OAUTH_TOKEN", token)
}

// upsertEnvVar replaces the KEY= line in a KEY=VALUE config file, or appends
// it if absent, preserving the rest of the file. Written with 0600 perms.
func upsertEnvVar(path, key, val string) error {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	var lines []string
	if len(data) > 0 {
		lines = strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	}
	prefix := key + "="
	found := false
	for i, ln := range lines {
		if strings.HasPrefix(strings.TrimSpace(ln), prefix) {
			lines[i] = key + "=" + val
			found = true
			break
		}
	}
	if !found {
		lines = append(lines, key+"="+val)
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0600)
}

func maskToken(token string) string {
	if len(token) <= 16 {
		return "****"
	}
	return token[:16] + "…" + token[len(token)-4:]
}
