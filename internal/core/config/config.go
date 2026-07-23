package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

const DefaultResumePrompt = `Resuming session from {{last_updated}}.{{#different_directory}} Session launched from {{project_path}}, but you were last working in: {{last_cwd}}

CRITICAL: You MUST immediately cd to {{last_cwd}} before doing anything else. The session was launched from the project root for database access, but all your work is in the worktree directory.{{/different_directory}}

IMPORTANT: This session has been inactive for {{time_since}}. Before proceeding: check git status, look around to understand what changed, and be careful not to overwrite any work in progress.`

type Config struct {
	AmpEnabled           bool
	ResumePromptTemplate string
	TerminalCommand      string            // Custom command to spawn terminal (optional)
	ClaudeFlags          []string          // Additional flags to pass to claude --resume
	CodexFlags           []string          // Additional flags to pass to codex resume (placed before the subcommand)
	CopilotFlags         []string          // Additional flags to pass to copilot --resume
	OpenCodeFlags        []string          // Additional flags to pass to opencode --session
	PiFlags              []string          // Additional flags to pass to pi --session/--fork
	LLMProvider          string            // LLM provider for summarization: "anthropic" or "bedrock"
	LastExportDir        string            // Last manually chosen export directory (global)
	RepoExportDirs       map[string]string // Last chosen export directory per repo root
}

type tomlConfig struct {
	AmpEnabled     bool              `toml:"amp_enabled"`
	ClaudeFlags    []string          `toml:"claude_flags"`
	CodexFlags     []string          `toml:"codex_flags"`
	CopilotFlags   []string          `toml:"copilot_flags"`
	OpenCodeFlags  []string          `toml:"opencode_flags"`
	PiFlags        []string          `toml:"pi_flags"`
	LLMProvider    string            `toml:"llm_provider"`
	LastExportDir  string            `toml:"last_export_dir,omitempty"`
	RepoExportDirs map[string]string `toml:"repo_export_dirs,omitempty"`
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
			cfg.AmpEnabled = tc.AmpEnabled
			cfg.CodexFlags = tc.CodexFlags
			cfg.CopilotFlags = tc.CopilotFlags
			cfg.OpenCodeFlags = tc.OpenCodeFlags
			cfg.PiFlags = tc.PiFlags
			cfg.LLMProvider = tc.LLMProvider
			cfg.LastExportDir = tc.LastExportDir
			cfg.RepoExportDirs = tc.RepoExportDirs
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

// Save writes the TOML-serializable parts of the config back to config.toml.
// Fields stored in separate files (resume_prompt.txt, terminal_command.txt) are not affected.
func Save(cfg *Config) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	configDir := filepath.Join(home, ".config", "ccrider")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return err
	}

	tc := tomlConfig{
		AmpEnabled:     cfg.AmpEnabled,
		ClaudeFlags:    cfg.ClaudeFlags,
		CodexFlags:     cfg.CodexFlags,
		CopilotFlags:   cfg.CopilotFlags,
		OpenCodeFlags:  cfg.OpenCodeFlags,
		PiFlags:        cfg.PiFlags,
		LLMProvider:    cfg.LLMProvider,
		LastExportDir:  cfg.LastExportDir,
		RepoExportDirs: cfg.RepoExportDirs,
	}

	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(tc); err != nil {
		return err
	}

	return os.WriteFile(filepath.Join(configDir, "config.toml"), buf.Bytes(), 0644)
}
