package bot

import (
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/bwmarrin/discordgo"
	"github.com/tackish/pigeon-claw/config"
	"github.com/tackish/pigeon-claw/discord"
	"github.com/tackish/pigeon-claw/executor"
	"github.com/tackish/pigeon-claw/prompt"
	"github.com/tackish/pigeon-claw/provider"
	"github.com/tackish/pigeon-claw/router"
	"github.com/tackish/pigeon-claw/session"
)

type Bot struct {
	cfg     *config.Config
	session *discordgo.Session
	router  *router.Router
	handler *discord.Handler
}

func New(cfg *config.Config) (*Bot, error) {
	dg, err := discordgo.New("Bot " + cfg.DiscordToken)
	if err != nil {
		return nil, fmt.Errorf("error creating Discord session: %w", err)
	}

	dg.Identify.Intents = discordgo.IntentsGuildMessages | discordgo.IntentsMessageContent

	providers := buildProviders(cfg)
	if len(providers) == 0 {
		return nil, fmt.Errorf("no providers configured. Set at least one API key")
	}

	store := session.NewStore(cfg.MaxSessionMessages, cfg.SessionDir)
	isCLI := len(cfg.ProviderPriority) > 0 && cfg.ProviderPriority[0] == "claude-cli"
	promptBuilder := prompt.NewBuilder(cfg.SystemPrompt, isCLI, cfg.ResponseLanguage)
	exec := executor.New(cfg.ExecTimeout, cfg.MaxToolOutput)
	rtr := router.New(providers, store, promptBuilder, exec, cfg.MaxToolIterations, cfg.RequestTimeout)
	handler := discord.NewHandler(rtr, cfg.AllowedChannels, cfg.MentionChannels, cfg.ResponseLanguage)

	dg.AddHandler(handler.OnMessageCreate)
	dg.AddHandler(handler.OnReactionAdd)
	dg.AddHandler(handler.OnInteraction)

	return &Bot{
		cfg:     cfg,
		session: dg,
		router:  rtr,
		handler: handler,
	}, nil
}

func (b *Bot) Run() error {
	if err := b.session.Open(); err != nil {
		return fmt.Errorf("error opening connection: %w", err)
	}
	defer b.session.Close()

	slog.Info("bot is running", "user", b.session.State.User.Username)

	// Register slash commands
	b.handler.RegisterSlashCommands(b.session)

	// Send restart completion message if restarted via !restart
	if ch := os.Getenv("PIGEON_RESTART_CHANNEL"); ch != "" {
		b.session.ChannelMessageSend(ch, "-# ✅ 재시작 완료")
		os.Unsetenv("PIGEON_RESTART_CHANNEL")
	}

	// Handle SIGHUP for config reload
	sighup := make(chan os.Signal, 1)
	signal.Notify(sighup, syscall.SIGHUP)

	// Handle SIGINT/SIGTERM for shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case <-sighup:
			slog.Info("received SIGHUP, reloading config")
			newCfg, err := config.Reload()
			if err != nil {
				slog.Error("failed to reload config", "error", err)
				continue
			}
			newProviders := buildProviders(newCfg)
			if len(newProviders) == 0 {
				slog.Error("no providers in new config, keeping current")
				continue
			}
			b.router.UpdateProviders(newProviders)
			b.handler.UpdateAllowedChannels(newCfg.AllowedChannels)
			b.handler.UpdateMentionChannels(newCfg.MentionChannels)
			b.cfg = newCfg
			slog.Info("config reloaded",
				"providers", len(newProviders),
				"allowed_channels", newCfg.AllowedChannels,
				"mention_channels", newCfg.MentionChannels,
			)

		case <-quit:
			slog.Info("shutting down")
			return nil
		}
	}
}

func buildProviders(cfg *config.Config) []provider.Provider {
	available := map[string]func() provider.Provider{
		"claude": func() provider.Provider {
			if cfg.AnthropicAPIKey == "" {
				return nil
			}
			return provider.NewClaude(cfg.AnthropicAPIKey, cfg.AnthropicModel)
		},
		"openai": func() provider.Provider {
			if cfg.OpenAIAPIKey == "" {
				return nil
			}
			return provider.NewOpenAI(cfg.OpenAIAPIKey, cfg.OpenAIModel)
		},
		"gemini": func() provider.Provider {
			if cfg.GeminiAPIKey == "" {
				return nil
			}
			return provider.NewGemini(cfg.GeminiAPIKey, cfg.GeminiModel)
		},
		"ollama": func() provider.Provider {
			return provider.NewOllama(cfg.OllamaHost, cfg.OllamaModel)
		},
		"claude-cli": func() provider.Provider {
			return provider.NewClaudeCLI("") // auto-detect from CLI
		},
	}

	var providers []provider.Provider
	for _, name := range cfg.ProviderPriority {
		factory, ok := available[name]
		if !ok {
			slog.Warn("unknown provider in priority list", "provider", name)
			continue
		}
		p := factory()
		if p == nil {
			slog.Debug("skipping provider (no API key)", "provider", name)
			continue
		}
		providers = append(providers, p)
		slog.Info("provider enabled", "provider", name)
	}

	return providers
}
