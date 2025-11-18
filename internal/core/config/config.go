package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

const DefaultResumePrompt = `CRITICAL: You are resuming a session that was last active {{time_since}}.

{{#different_directory}}
IMMEDIATELY change to the working directory where this session was last active:
  cd {{last_cwd}}

DO NOT PROCEED until you have changed to {{last_cwd}}. The session was launched from {{project_path}} but you were actively working in {{last_cwd}}.
{{/different_directory}}
{{#same_directory}}
You are in the correct directory: {{project_path}}
{{/same_directory}}

Before making ANY changes:
1. Run 'git status' to see what's uncommitted
2. Run 'git log --oneline -5' to see recent commits
3. Check if there's work in progress that could be overwritten

Session resumed from {{last_updated}}. Proceed with extreme caution.`

type Config struct {
	ResumePromptTemplate string
	TerminalCommand      string   // Custom command to spawn terminal (optional)
	ClaudeFlags          []string // Additional flags to pass to claude --resume
}

type tomlConfig struct {
	ClaudeFlags []string `toml:"claude_flags"`
}

// Load reads config from ~/.config/ccrider/
func Load() (*Config, error) {
	cfg := &Config{
		ResumePromptTemplate: DefaultResumePrompt,
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return cfg, nil // Use defaults
	}

	configDir := filepath.Join(home, ".config", "ccrider")
	promptPath := filepath.Join(configDir, "resume_prompt.txt")
	terminalPath := filepath.Join(configDir, "terminal_command.txt")
	tomlPath := filepath.Join(configDir, "config.toml")

	// Load TOML config if it exists
	if _, err := os.Stat(tomlPath); err == nil {
		var tc tomlConfig
		if _, err := toml.DecodeFile(tomlPath, &tc); err == nil {
			cfg.ClaudeFlags = tc.ClaudeFlags
		}
	}

	// If custom template exists, use it
	if data, err := os.ReadFile(promptPath); err == nil {
		cfg.ResumePromptTemplate = string(data)
	}

	// If custom terminal command exists, use it
	if data, err := os.ReadFile(terminalPath); err == nil {
		cfg.TerminalCommand = strings.TrimSpace(string(data))
	}

	return cfg, nil
}
