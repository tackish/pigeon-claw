package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Config struct {
	DiscordToken       string
	AnthropicAPIKey    string
	OpenAIAPIKey       string
	GeminiAPIKey       string
	OllamaHost         string
	OllamaModel        string
	AnthropicModel     string
	OpenAIModel        string
	GeminiModel        string
	ProviderPriority   []string
	SystemPrompt       string
	MaxSessionMessages int
	RequestTimeout     time.Duration
	ExecTimeout        time.Duration
	MaxToolIterations  int
	MaxToolOutput      int
	SessionDir         string
	LogLevel           string
	AllowedChannels    []string
	MentionChannels    []string
	ResponseLanguage   string
}

var (
	current *Config
	mu      sync.RWMutex
)

func Load() (*Config, error) {
	home, _ := os.UserHomeDir()
	loadEnvFile(home + "/.pigeon-claw/config")

	cfg := &Config{
		DiscordToken:    os.Getenv("DISCORD_TOKEN"),
		AnthropicAPIKey: os.Getenv("ANTHROPIC_API_KEY"),
		OpenAIAPIKey:    os.Getenv("OPENAI_API_KEY"),
		GeminiAPIKey:    os.Getenv("GEMINI_API_KEY"),
		OllamaHost:      envOrDefault("OLLAMA_HOST", "http://localhost:11434"),
		OllamaModel:     envOrDefault("OLLAMA_MODEL", "llama3"),
		AnthropicModel:  envOrDefault("ANTHROPIC_MODEL", "claude-sonnet-4-20250514"),
		OpenAIModel:     envOrDefault("OPENAI_MODEL", "gpt-4o"),
		GeminiModel:     envOrDefault("GEMINI_MODEL", "gemini-2.0-flash"),
		SystemPrompt:     loadSystemPrompt(),
		LogLevel:         envOrDefault("LOG_LEVEL", "info"),
		ResponseLanguage: os.Getenv("RESPONSE_LANGUAGE"),
	}

	if cfg.DiscordToken == "" {
		return nil, fmt.Errorf("DISCORD_TOKEN is required")
	}

	priority := envOrDefault("PROVIDER_PRIORITY", "claude,openai,gemini,ollama")
	cfg.ProviderPriority = strings.Split(priority, ",")
	for i, p := range cfg.ProviderPriority {
		cfg.ProviderPriority[i] = strings.TrimSpace(p)
	}

	if ch := os.Getenv("ALLOWED_CHANNELS"); ch != "" {
		for _, c := range strings.Split(ch, ",") {
			cfg.AllowedChannels = append(cfg.AllowedChannels, strings.TrimSpace(c))
		}
	}

	if ch := os.Getenv("MENTION_CHANNELS"); ch != "" {
		for _, c := range strings.Split(ch, ",") {
			cfg.MentionChannels = append(cfg.MentionChannels, strings.TrimSpace(c))
		}
	}

	cfg.MaxSessionMessages = envOrDefaultInt("MAX_SESSION_MESSAGES", 50)
	cfg.MaxToolIterations = envOrDefaultInt("MAX_TOOL_ITERATIONS", 10)
	cfg.MaxToolOutput = envOrDefaultInt("MAX_TOOL_OUTPUT", 4000)
	cfg.RequestTimeout = envOrDefaultDuration("REQUEST_TIMEOUT", 30*time.Second)
	cfg.ExecTimeout = envOrDefaultDuration("EXEC_TIMEOUT", 60*time.Second)

	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = "."
	}
	cfg.SessionDir = envOrDefault("SESSION_DIR", homeDir+"/.pigeon-claw/sessions")

	mu.Lock()
	current = cfg
	mu.Unlock()

	return cfg, nil
}

func Current() *Config {
	mu.RLock()
	defer mu.RUnlock()
	return current
}

func Reload() (*Config, error) {
	return Load()
}

func loadEnvFile(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return // .env not found, skip silently
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		// Config file always wins over existing env vars
		os.Setenv(key, val)
	}
}

func loadSystemPrompt() string {
	// 1. SYSTEM_PROMPT_FILE 환경변수로 파일 경로 지정
	if path := os.Getenv("SYSTEM_PROMPT_FILE"); path != "" {
		data, err := os.ReadFile(path)
		if err == nil {
			return strings.TrimSpace(string(data))
		}
	}

	// 2. 기본 경로: ~/.pigeon-claw/prompt.md
	home, _ := os.UserHomeDir()
	defaultPath := home + "/.pigeon-claw/prompt.md"
	if data, err := os.ReadFile(defaultPath); err == nil {
		return strings.TrimSpace(string(data))
	}

	// 3. SYSTEM_PROMPT 환경변수 (인라인)
	return os.Getenv("SYSTEM_PROMPT")
}

func envOrDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

func envOrDefaultInt(key string, defaultVal int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return defaultVal
}

func envOrDefaultDuration(key string, defaultVal time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		// Try standard duration format first (e.g., "30s", "5m")
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
		// Fallback: treat plain numbers as seconds (e.g., "600" → 600s)
		if secs, err := strconv.Atoi(v); err == nil {
			return time.Duration(secs) * time.Second
		}
	}
	return defaultVal
}
