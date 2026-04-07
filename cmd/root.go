package cmd

import (
	"fmt"
	"os"
)

var version = "dev"

func Execute() {
	if len(os.Args) < 2 {
		// If no .env exists, run wizard first
		if !envExists() {
			fmt.Println("No configuration found. Starting setup wizard...")
			fmt.Println()
			runInit()
			return
		}
		runServe()
		return
	}

	switch os.Args[1] {
	case "serve":
		runServe()
	case "init":
		runInit()
	case "permission":
		runPermission()
	case "start":
		runDaemon("start")
	case "stop":
		runDaemon("stop")
	case "restart":
		runDaemon("restart")
	case "status":
		runDaemon("status")
	case "reload":
		runDaemon("reload")
	case "logs":
		runDaemon("logs")
	case "doctor":
		runDoctor()
	case "help", "-h", "--help":
		printHelp()
	case "version", "-v", "--version":
		fmt.Printf("pigeon-claw %s\n", version)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		printHelp()
		os.Exit(1)
	}
}

func printHelp() {
	fmt.Printf(`pigeon-claw %s — Discord-based remote Mac agent

A lightweight alternative to openclaw. Chat in Discord, LLM controls your Mac.

Usage:
  pigeon-claw                Run the bot (or setup wizard if not configured)
  pigeon-claw init           Interactive setup wizard
  pigeon-claw serve          Run in foreground
  pigeon-claw permission     macOS permissions setup only

Daemon:
  pigeon-claw start          Start as background daemon
  pigeon-claw stop           Stop the daemon
  pigeon-claw restart        Restart (stop + start)
  pigeon-claw reload         Hot reload config (SIGHUP)
  pigeon-claw status         Check if running
  pigeon-claw logs           Tail logs in real-time

Info:
  pigeon-claw doctor         Diagnose config, permissions, and connectivity
  pigeon-claw help           Show this help
  pigeon-claw version        Print version

Providers:
  claude-cli    Claude CLI via Max subscription (no API key, recommended)
  claude        Anthropic API (ANTHROPIC_API_KEY)
  openai        OpenAI API (OPENAI_API_KEY)
  gemini        Google Gemini API (GEMINI_API_KEY)
  ollama        Local Ollama (default localhost:11434)

Environment (.env file or env vars):
  DISCORD_TOKEN          Discord bot token (required)
  PROVIDER_PRIORITY      Priority order (e.g., claude-cli,ollama)
  ALLOWED_CHANNELS       Channel IDs that respond to all messages
  MENTION_CHANNELS       Channel IDs that respond only to @mentions
  SYSTEM_PROMPT_FILE     Custom prompt file (default: ~/.pigeon-claw/prompt.md)
  SYSTEM_PROMPT          Inline custom prompt (lower priority than file)
  MAX_SESSION_MESSAGES   Sliding window size (default: 50)
  REQUEST_TIMEOUT        Provider timeout (default: 30s)
  EXEC_TIMEOUT           Shell command timeout (default: 60s)
  MAX_TOOL_ITERATIONS    Max tool use loop (default: 10)
  LOG_LEVEL              debug, info, warn, error (default: info)

Discord Commands:
  !reset                 Clear channel session
  !status                Show active provider + message count
  !provider              Show provider priority with models
  !model                 List all provider models
  !model <prov> <model>  Change model at runtime (e.g., !model ollama gemma4:e4b)

Config: ~/.pigeon-claw/config (created by init wizard)
Sessions: ~/.pigeon-claw/sessions/

Quick Start:
  1. pigeon-claw init          # interactive setup wizard
  2. pigeon-claw start         # run as daemon
`, version)
}
