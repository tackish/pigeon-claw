package cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func envExists() bool {
	home, _ := os.UserHomeDir()
	_, err := os.Stat(filepath.Join(home, ".pigeon-claw", "config"))
	return err == nil
}

func runInit() {
	reader := bufio.NewReader(os.Stdin)
	home, _ := os.UserHomeDir()
	configDir := filepath.Join(home, ".pigeon-claw")
	envPath := filepath.Join(configDir, "config")

	fmt.Println("=== pigeon-claw Setup Wizard ===")
	fmt.Println()

	// Step 1: Discord Token
	fmt.Println("[1/4] Discord Bot Token")
	fmt.Println()
	fmt.Println("  You need a Discord bot token. If you don't have one:")
	fmt.Println("  1. Go to https://discord.com/developers/applications")
	fmt.Println("  2. New Application → Bot tab → Reset Token → Copy")
	fmt.Println("  3. Enable 'Message Content Intent' under Privileged Gateway Intents")
	fmt.Println("  4. OAuth2 tab → check 'bot' scope → copy invite URL → open in browser")
	fmt.Println()
	fmt.Print("  Paste your Discord bot token: ")
	token, _ := reader.ReadString('\n')
	token = strings.TrimSpace(token)
	if token == "" {
		fmt.Println("  ✗ Token is required. Run 'pigeon-claw init' again when ready.")
		os.Exit(1)
	}
	fmt.Println("  ✓ Token set")
	fmt.Println()

	// Step 2: Provider
	fmt.Println("[2/4] LLM Provider")
	fmt.Println()
	fmt.Println("  Which provider do you want to use?")
	fmt.Println()
	fmt.Println("  1. claude-cli  — Claude Max/Pro subscription (recommended, no API key)")
	fmt.Println("  2. ollama      — Local models, free (requires ollama installed)")
	fmt.Println("  3. claude      — Anthropic API (requires API key)")
	fmt.Println("  4. openai      — OpenAI API (requires API key)")
	fmt.Println("  5. gemini      — Google Gemini API (requires API key)")
	fmt.Println()
	fmt.Print("  Choose [1-5, default=1]: ")
	choice, _ := reader.ReadString('\n')
	choice = strings.TrimSpace(choice)
	if choice == "" {
		choice = "1"
	}

	var providerPriority string
	var extraEnv string

	switch choice {
	case "1":
		providerPriority = "claude-cli"
		fmt.Println()
		fmt.Println("  Claude CLI selected. Checking installation...")
		claudePath := findClaude()
		if claudePath == "" {
			fmt.Println("  ✗ Claude CLI not found. Install it:")
			fmt.Println("    npm install -g @anthropic-ai/claude-code")
			fmt.Println("  Then run: claude login")
		} else {
			fmt.Printf("  ✓ Found: %s\n", claudePath)
			fmt.Println("  Make sure you're logged in: claude login")
		}
	case "2":
		providerPriority = "ollama"
		fmt.Println()
		fmt.Print("  Ollama model [default=gemma4:e4b]: ")
		model, _ := reader.ReadString('\n')
		model = strings.TrimSpace(model)
		if model == "" {
			model = "gemma4:e4b"
		}
		extraEnv = fmt.Sprintf("OLLAMA_HOST=http://localhost:11434\nOLLAMA_MODEL=%s\n", model)
		fmt.Printf("  ✓ Ollama with %s\n", model)
		fmt.Println("  Make sure ollama is running: brew services start ollama")
		fmt.Printf("  And pull the model: ollama pull %s\n", model)
	case "3":
		providerPriority = "claude"
		fmt.Print("  Anthropic API key: ")
		key, _ := reader.ReadString('\n')
		extraEnv = fmt.Sprintf("ANTHROPIC_API_KEY=%s\n", strings.TrimSpace(key))
	case "4":
		providerPriority = "openai"
		fmt.Print("  OpenAI API key: ")
		key, _ := reader.ReadString('\n')
		extraEnv = fmt.Sprintf("OPENAI_API_KEY=%s\n", strings.TrimSpace(key))
	case "5":
		providerPriority = "gemini"
		fmt.Print("  Gemini API key: ")
		key, _ := reader.ReadString('\n')
		extraEnv = fmt.Sprintf("GEMINI_API_KEY=%s\n", strings.TrimSpace(key))
	default:
		providerPriority = "claude-cli"
	}

	fmt.Println()

	// Step 3: Channels
	fmt.Println("[3/4] Channel Configuration")
	fmt.Println()
	fmt.Println("  Restrict the bot to specific channels? (optional)")
	fmt.Println("  To find a channel ID: Discord → Settings → Advanced → Developer Mode → right-click channel → Copy ID")
	fmt.Println()
	fmt.Print("  Channel IDs (comma-separated, or press Enter to respond in all channels): ")
	channels, _ := reader.ReadString('\n')
	channels = strings.TrimSpace(channels)

	var channelEnv string
	if channels != "" {
		channelEnv = fmt.Sprintf("ALLOWED_CHANNELS=%s\n", channels)

		fmt.Println()
		fmt.Print("  Any mention-only channels? (bot responds only to @mentions, comma-separated, or Enter to skip): ")
		mention, _ := reader.ReadString('\n')
		mention = strings.TrimSpace(mention)
		if mention != "" {
			channelEnv += fmt.Sprintf("MENTION_CHANNELS=%s\n", mention)
		}
	}

	fmt.Println()

	// Step 4: Language
	fmt.Println("[4/6] Response Language")
	fmt.Println()
	fmt.Println("  1. 한국어 (Korean)")
	fmt.Println("  2. English")
	fmt.Println("  3. 日本語 (Japanese)")
	fmt.Println("  4. 中文 (Chinese)")
	fmt.Println()
	fmt.Print("  Choose [1-4, default=1]: ")
	langChoice, _ := reader.ReadString('\n')
	langChoice = strings.TrimSpace(langChoice)
	if langChoice == "" {
		langChoice = "1"
	}

	var responseLang string
	switch langChoice {
	case "1":
		responseLang = "korean"
		fmt.Println("  ✓ 한국어")
	case "2":
		responseLang = ""
		fmt.Println("  ✓ English")
	case "3":
		responseLang = "japanese"
		fmt.Println("  ✓ 日本語")
	case "4":
		responseLang = "chinese"
		fmt.Println("  ✓ 中文")
	default:
		responseLang = "korean"
		fmt.Println("  ✓ 한국어")
	}

	fmt.Println()

	// Step 5: Custom Prompt
	fmt.Println("[5/6] Custom System Prompt")
	fmt.Println()
	fmt.Println("  The system prompt tells the LLM how to behave.")
	fmt.Println("  A default prompt is built-in (based on openclaw's approach):")
	fmt.Println("    - Execute tasks directly, don't narrate routine actions")
	fmt.Println("    - Brief factual responses, no filler")
	fmt.Println("    - Use tools to find answers, never guess")
	fmt.Println()
	fmt.Println("  You can override it later by editing ~/.pigeon-claw/prompt.md")
	fmt.Println()
	fmt.Print("  Customize the prompt now? [y/N]: ")
	promptAnswer, _ := reader.ReadString('\n')
	promptAnswer = strings.TrimSpace(strings.ToLower(promptAnswer))
	if promptAnswer == "y" || promptAnswer == "yes" {
		fmt.Println()
		fmt.Println("  Enter your prompt (end with an empty line):")
		fmt.Println("  Tip: add role, rules, or constraints for your use case.")
		fmt.Println()
		var lines []string
		for {
			line, _ := reader.ReadString('\n')
			line = strings.TrimRight(line, "\n\r")
			if line == "" {
				break
			}
			lines = append(lines, line)
		}
		if len(lines) > 0 {
			promptPath := filepath.Join(configDir, "prompt.md")
			os.WriteFile(promptPath, []byte(strings.Join(lines, "\n")+"\n"), 0644)
			fmt.Printf("  ✓ Prompt saved to %s\n", promptPath)
		}
	} else {
		fmt.Println("  ✓ Using default prompt (you can customize later)")
	}

	fmt.Println()

	// Step 6: Timeout
	fmt.Println("[6/6] Advanced Settings")
	fmt.Println()
	fmt.Print("  Request timeout [default=300s]: ")
	timeout, _ := reader.ReadString('\n')
	timeout = strings.TrimSpace(timeout)
	if timeout == "" {
		timeout = "300s"
	}

	fmt.Println()

	// Write .env
	os.MkdirAll(configDir, 0755)

	var env strings.Builder
	env.WriteString(fmt.Sprintf("DISCORD_TOKEN=%s\n", token))
	env.WriteString(fmt.Sprintf("PROVIDER_PRIORITY=%s\n", providerPriority))
	if extraEnv != "" {
		env.WriteString(extraEnv)
	}
	if channelEnv != "" {
		env.WriteString(channelEnv)
	}
	if responseLang != "" {
		env.WriteString(fmt.Sprintf("RESPONSE_LANGUAGE=%s\n", responseLang))
	}
	env.WriteString(fmt.Sprintf("REQUEST_TIMEOUT=%s\n", timeout))
	env.WriteString("LOG_LEVEL=info\n")

	if err := os.WriteFile(envPath, []byte(env.String()), 0600); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to write config: %s\n", err)
		os.Exit(1)
	}

	fmt.Printf("✓ Config saved to %s\n", envPath)
	fmt.Println()

	// Ask about permissions
	fmt.Print("Run macOS permission setup now? [Y/n]: ")
	answer, _ := reader.ReadString('\n')
	answer = strings.TrimSpace(strings.ToLower(answer))
	if answer == "" || answer == "y" || answer == "yes" {
		fmt.Println()
		runPermission()
	}

	fmt.Println()
	fmt.Println("=== Setup Complete ===")
	fmt.Println()
	fmt.Println("  Start the bot:")
	fmt.Println("    pigeon-claw start      # background")
	fmt.Println("    pigeon-claw serve      # foreground")
	fmt.Println()
	fmt.Println("  Edit config later:")
	fmt.Printf("    nano %s\n", envPath)
}
