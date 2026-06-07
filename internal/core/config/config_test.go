package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// writeConfigToml points HOME at a temp dir and writes config.toml there.
func writeConfigToml(t *testing.T, content string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".config", "ccrider")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadPerProviderFlags(t *testing.T) {
	writeConfigToml(t, `claude_flags = ["--dangerously-skip-permissions"]
codex_flags = ["--dangerously-bypass-approvals-and-sandbox"]
copilot_flags = ["--allow-all-tools"]
`)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"--dangerously-skip-permissions"}; !reflect.DeepEqual(cfg.ClaudeFlags, want) {
		t.Errorf("ClaudeFlags = %v, want %v", cfg.ClaudeFlags, want)
	}
	if want := []string{"--dangerously-bypass-approvals-and-sandbox"}; !reflect.DeepEqual(cfg.CodexFlags, want) {
		t.Errorf("CodexFlags = %v, want %v", cfg.CodexFlags, want)
	}
	if want := []string{"--allow-all-tools"}; !reflect.DeepEqual(cfg.CopilotFlags, want) {
		t.Errorf("CopilotFlags = %v, want %v", cfg.CopilotFlags, want)
	}
}

func TestSaveRoundTripsPerProviderFlags(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	in := &Config{
		ClaudeFlags:  []string{"--claude-only"},
		CodexFlags:   []string{"--codex-only"},
		CopilotFlags: []string{"--copilot-only"},
	}
	if err := Save(in); err != nil {
		t.Fatal(err)
	}

	out, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(out.ClaudeFlags, in.ClaudeFlags) {
		t.Errorf("ClaudeFlags = %v, want %v", out.ClaudeFlags, in.ClaudeFlags)
	}
	if !reflect.DeepEqual(out.CodexFlags, in.CodexFlags) {
		t.Errorf("CodexFlags = %v, want %v", out.CodexFlags, in.CodexFlags)
	}
	if !reflect.DeepEqual(out.CopilotFlags, in.CopilotFlags) {
		t.Errorf("CopilotFlags = %v, want %v", out.CopilotFlags, in.CopilotFlags)
	}
}
