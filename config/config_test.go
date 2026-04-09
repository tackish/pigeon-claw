package config

import (
	"os"
	"testing"
)

// clearConfigEnv removes all config-related env vars so tests are isolated
// from the user's ~/.pigeon-claw/config file.
func clearConfigEnv() {
	for _, key := range []string{
		"DISCORD_TOKEN", "PROVIDER_PRIORITY", "ANTHROPIC_API_KEY",
		"OPENAI_API_KEY", "GEMINI_API_KEY", "OLLAMA_HOST", "OLLAMA_MODEL",
		"ANTHROPIC_MODEL", "OPENAI_MODEL", "GEMINI_MODEL", "SYSTEM_PROMPT",
		"SYSTEM_PROMPT_FILE", "LOG_LEVEL", "RESPONSE_LANGUAGE",
		"MAX_SESSION_MESSAGES", "MAX_TOOL_ITERATIONS", "MAX_TOOL_OUTPUT",
		"REQUEST_TIMEOUT", "EXEC_TIMEOUT", "SESSION_DIR",
		"ALLOWED_CHANNELS", "MENTION_CHANNELS",
	} {
		os.Unsetenv(key)
	}
}

func TestLoadRequiresDiscordToken(t *testing.T) {
	clearConfigEnv()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", t.TempDir())
	defer os.Setenv("HOME", origHome)

	_, err := Load()
	if err == nil {
		t.Fatal("expected error when DISCORD_TOKEN is missing")
	}
}

func TestLoadDefaults(t *testing.T) {
	clearConfigEnv()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", t.TempDir())
	defer os.Setenv("HOME", origHome)

	os.Setenv("DISCORD_TOKEN", "test-token")
	defer clearConfigEnv()

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
	clearConfigEnv()
	origHome := os.Getenv("HOME")
	tmpHome := t.TempDir()
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", origHome)

	// Write a config file with custom values
	configDir := tmpHome + "/.pigeon-claw"
	os.MkdirAll(configDir, 0755)
	os.WriteFile(configDir+"/config", []byte(
		"DISCORD_TOKEN=test-token\nPROVIDER_PRIORITY=ollama,claude\nMAX_SESSION_MESSAGES=100\nALLOWED_CHANNELS=ch1,ch2,ch3\n",
	), 0644)

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

	// Config file should override existing env vars
	os.Setenv("TEST_LOAD_ENV", "original")
	loadEnvFile(envFile)
	if v := os.Getenv("TEST_LOAD_ENV"); v != "works" {
		t.Fatalf("expected config file to override, got '%s'", v)
	}

	os.Unsetenv("TEST_LOAD_ENV")
	os.Unsetenv("TEST_EMPTY")
}
