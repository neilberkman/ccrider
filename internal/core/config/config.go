package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

const DefaultResumePrompt = `Resuming session from {{last_updated}}.{{#different_directory}} Session launched from {{project_path}}, but you were last working in: {{last_cwd}}{{/different_directory}}

IMPORTANT: This session has been inactive for {{time_since}}. Before proceeding: check git status, look around to understand what changed, and be careful not to overwrite any work in progress.`

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
