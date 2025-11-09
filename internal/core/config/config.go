package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

const DefaultResumePrompt = `Resuming session from {{last_updated}}.{{#different_directory}} Started in your last working directory: {{last_cwd}}{{/different_directory}}

IMPORTANT: This session has been inactive for {{time_since}}. Before proceeding: check git status, look around to understand what changed, and be careful not to overwrite any work in progress.`

type Config struct {
	ResumePromptTemplate        string
	TerminalCommand             string // Custom command to spawn terminal (optional)
	DangerouslySkipPermissions bool   // Skip all permission prompts (use with caution)
}

type tomlConfig struct {
	DangerouslySkipPermissions bool `toml:"dangerously_skip_permissions"`
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
			cfg.DangerouslySkipPermissions = tc.DangerouslySkipPermissions
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
