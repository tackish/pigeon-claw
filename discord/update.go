package discord

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/tackish/pigeon-claw/update"
)

// updateTimeout bounds the brew run. A cold `brew update` can take a
// couple of minutes; beyond that something is wedged and the bot should
// say so rather than look hung.
const updateTimeout = 10 * time.Minute

// updateMu serializes update attempts — two concurrent brew upgrades on
// the same formula fight over the same lock and both fail.
var updateMu sync.Mutex

// handleUpdate compares the running version against the latest release and,
// when behind, upgrades via Homebrew and restarts into the new binary.
// Runs in its own goroutine: brew takes far longer than a Discord handler
// should block for.
func (h *Handler) handleUpdate(s *discordgo.Session, channelID string) {
	if !updateMu.TryLock() {
		s.ChannelMessageSend(channelID, "-# ⚠️ 이미 업데이트가 진행 중입니다.")
		return
	}
	defer updateMu.Unlock()

	current := update.Current()
	s.ChannelMessageSend(channelID, fmt.Sprintf("-# 🔎 현재 `%s` — 최신 릴리스 확인 중...", current))

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	latest, err := update.Latest(ctx)
	cancel()
	if err != nil {
		s.ChannelMessageSend(channelID, fmt.Sprintf("-# ❌ 릴리스 조회 실패: %s", err))
		return
	}

	if current == "dev" {
		// A locally built binary carries no release version, so there is
		// nothing meaningful to compare — upgrading would silently replace
		// it with the published build.
		s.ChannelMessageSend(channelID, fmt.Sprintf(
			"-# ℹ️ 로컬 빌드(`dev`)로 실행 중이라 업데이트를 건너뜁니다. 최신 릴리스는 `%s` 입니다.", latest))
		return
	}

	if !update.IsNewer(latest, current) {
		s.ChannelMessageSend(channelID, fmt.Sprintf("-# ✅ 이미 최신 버전입니다 (`%s`).", current))
		return
	}

	s.ChannelMessageSend(channelID, fmt.Sprintf("-# ⬆ `%s` → `%s` 업데이트 중... (몇 분 걸릴 수 있습니다)", current, latest))
	slog.Info("updating via discord", "current", current, "latest", latest)

	ctx, cancel = context.WithTimeout(context.Background(), updateTimeout)
	defer cancel()
	if err := update.Upgrade(ctx); err != nil {
		slog.Error("update failed", "error", err)
		s.ChannelMessageSend(channelID, fmt.Sprintf("-# ❌ 업데이트 실패:\n```%s```", err))
		return
	}

	s.ChannelMessageSend(channelID, fmt.Sprintf("-# ✅ `%s` 설치 완료. 새 버전으로 재시작합니다...", latest))
	h.restartProcess(channelID)
}

// restartProcess re-execs the bot so a freshly installed binary takes
// effect, announcing completion in channelID once it is back up.
func (h *Handler) restartProcess(channelID string) {
	time.Sleep(500 * time.Millisecond) // let the outgoing message flush

	// The instance lock is an flock on an O_CLOEXEC descriptor: exec drops
	// it and the new image re-acquires it. Deleting the lock file here
	// would create a second lockable inode and let a duplicate instance
	// start alongside this one.

	// Use the symlink path (e.g. /opt/homebrew/bin/pigeon-claw) directly.
	// Do NOT resolve symlinks — we always want the currently-linked
	// version, so brew upgrades take effect on restart.
	exe, err := exec.LookPath("pigeon-claw")
	if err != nil {
		exe, _ = os.Executable()
	}

	slog.Info("restarting", "binary", exe)

	// Pass the channel so the new process can report it is back.
	env := append(os.Environ(), "PIGEON_RESTART_CHANNEL="+channelID)

	if err := syscall.Exec(exe, []string{exe, "serve"}, env); err != nil {
		slog.Error("syscall.Exec failed, falling back to cmd.Start", "error", err)
		cmd := exec.Command(exe, "serve")
		cmd.Env = env
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Start()
		os.Exit(0)
	}
}
