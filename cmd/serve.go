package cmd

import (
	"log/slog"
	"os"
	"path/filepath"

	"github.com/tackish/pigeon-claw/bot"
	"github.com/tackish/pigeon-claw/config"
)

func runServe() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	setupLogger(cfg.LogLevel)

	home, _ := os.UserHomeDir()
	lockPath := filepath.Join(home, ".pigeon-claw", "pigeon-claw.pid")
	if err := acquireLock(lockPath); err != nil {
		slog.Error("cannot start", "error", err)
		os.Exit(1)
	}
	defer releaseLock(lockPath)

	checkUpdate()
	slog.Info("starting pigeon-claw",
		"allowed_channels", cfg.AllowedChannels,
		"mention_channels", cfg.MentionChannels,
		"provider_priority", cfg.ProviderPriority,
	)

	b, err := bot.New(cfg)
	if err != nil {
		slog.Error("failed to create bot", "error", err)
		os.Exit(1)
	}

	if err := b.Run(); err != nil {
		slog.Error("bot exited with error", "error", err)
		os.Exit(1)
	}
}

func setupLogger(level string) {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl})))
}
