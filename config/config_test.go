package config

import (
	"os"
	"testing"
)

func TestLoadRequiresDiscordToken(t *testing.T) {
	os.Unsetenv("DISCORD_TOKEN")
	_, err := Load()
	if err == nil {
		t.Fatal("expected error when DISCORD_TOKEN is missing")
	}
}

func TestLoadDefaults(t *testing.T) {
	os.Setenv("DISCORD_TOKEN", "test-token")
	defer os.Unsetenv("DISCORD_TOKEN")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.OllamaHost != "http://localhost:11434" {
		t.Fatalf("expected default OLLAMA_HOST, got '%s'", cfg.OllamaHost)
	}
	if cfg.MaxSessionMessages != 50 {
		t.Fatalf("expected 50, got %d", cfg.MaxSessionMessages)
	}
	if cfg.MaxToolIterations != 10 {
		t.Fatalf("expected 10, got %d", cfg.MaxToolIterations)
	}
	if len(cfg.ProviderPriority) != 4 {
		t.Fatalf("expected 4 default providers, got %d", len(cfg.ProviderPriority))
	}
}

func TestLoadCustomValues(t *testing.T) {
	os.Setenv("DISCORD_TOKEN", "test-token")
	os.Setenv("PROVIDER_PRIORITY", "ollama,claude")
	os.Setenv("MAX_SESSION_MESSAGES", "100")
	os.Setenv("ALLOWED_CHANNELS", "ch1,ch2,ch3")
	defer func() {
		os.Unsetenv("DISCORD_TOKEN")
		os.Unsetenv("PROVIDER_PRIORITY")
		os.Unsetenv("MAX_SESSION_MESSAGES")
		os.Unsetenv("ALLOWED_CHANNELS")
	}()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(cfg.ProviderPriority) != 2 {
		t.Fatalf("expected 2 providers, got %d", len(cfg.ProviderPriority))
	}
	if cfg.ProviderPriority[0] != "ollama" {
		t.Fatalf("expected 'ollama' first, got '%s'", cfg.ProviderPriority[0])
	}
	if cfg.MaxSessionMessages != 100 {
		t.Fatalf("expected 100, got %d", cfg.MaxSessionMessages)
	}
	if len(cfg.AllowedChannels) != 3 {
		t.Fatalf("expected 3 channels, got %d", len(cfg.AllowedChannels))
	}
}

func TestLoadEnvFile(t *testing.T) {
	// Create temp .env file
	dir := t.TempDir()
	envFile := dir + "/.env"
	os.WriteFile(envFile, []byte("TEST_LOAD_ENV=works\n# comment\nTEST_EMPTY=\n"), 0644)

	os.Unsetenv("TEST_LOAD_ENV")
	loadEnvFile(envFile)

	if v := os.Getenv("TEST_LOAD_ENV"); v != "works" {
		t.Fatalf("expected 'works', got '%s'", v)
	}

	// Should not override existing
	os.Setenv("TEST_LOAD_ENV", "original")
	loadEnvFile(envFile)
	if v := os.Getenv("TEST_LOAD_ENV"); v != "original" {
		t.Fatalf("expected 'original', got '%s'", v)
	}

	os.Unsetenv("TEST_LOAD_ENV")
	os.Unsetenv("TEST_EMPTY")
}
