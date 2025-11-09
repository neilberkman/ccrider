package config

import (
	"os"
	"path/filepath"
)

const DefaultResumePrompt = `Resuming session from {{last_updated}}. You were last working in: {{last_cwd}}

IMPORTANT: This session has been inactive for {{time_since}}. Before proceeding: check git status, look around to understand what changed, and be careful not to overwrite any work in progress.

First, navigate to where you left off.`

type Config struct {
	ResumePromptTemplate string
}

// Load reads config from ~/.config/ccrider/config (if it exists)
func Load() (*Config, error) {
	cfg := &Config{
		ResumePromptTemplate: DefaultResumePrompt,
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return cfg, nil // Use defaults
	}

	configPath := filepath.Join(home, ".config", "ccrider", "resume_prompt.txt")

	// If custom template exists, use it
	if data, err := os.ReadFile(configPath); err == nil {
		cfg.ResumePromptTemplate = string(data)
	}

	return cfg, nil
}
