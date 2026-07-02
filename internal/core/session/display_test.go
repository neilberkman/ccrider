package session

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/neilberkman/ccrider/internal/core/config"
	"github.com/neilberkman/ccrider/internal/core/db"
)

// withTempConfig points HOME at a temp dir, optionally writing a config.toml,
// so tests control exactly what ConfiguredFlags sees.
func withTempConfig(t *testing.T, configToml string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	if configToml != "" {
		dir := filepath.Join(home, ".config", "ccrider")
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(configToml), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDisplayResumeCommand(t *testing.T) {
	t.Run("no config", func(t *testing.T) {
		withTempConfig(t, "")
		got := DisplayResumeCommand("claude", "sess-1", "/Users/x/proj", "/Users/x/elsewhere", false)
		want := "cd '/Users/x/proj' && claude --resume 'sess-1'"
		if got != want {
			t.Errorf("DisplayResumeCommand = %q, want %q", got, want)
		}
	})

	t.Run("configured claude flags are applied", func(t *testing.T) {
		withTempConfig(t, "claude_flags = [\"--dangerously-skip-permissions\"]\n")
		got := DisplayResumeCommand("claude", "sess-1", "/Users/x/proj", "", false)
		want := "cd '/Users/x/proj' && claude --dangerously-skip-permissions --resume 'sess-1'"
		if got != want {
			t.Errorf("DisplayResumeCommand = %q, want %q", got, want)
		}
	})

	t.Run("codex ignores claude flags", func(t *testing.T) {
		withTempConfig(t, "claude_flags = [\"--dangerously-skip-permissions\"]\n")
		got := DisplayResumeCommand("codex", "rollout-2026-06-04T14-40-56-019e93f0-3efe-7742-9598-bb06b36fb25a", "/Users/x/proj", "", false)
		want := "cd '/Users/x/proj' && codex resume '019e93f0-3efe-7742-9598-bb06b36fb25a'"
		if got != want {
			t.Errorf("DisplayResumeCommand = %q, want %q", got, want)
		}
	})

	t.Run("configured codex flags are applied before subcommand", func(t *testing.T) {
		withTempConfig(t, "codex_flags = [\"--dangerously-bypass-approvals-and-sandbox\"]\n")
		got := DisplayResumeCommand("codex", "rollout-2026-06-04T14-40-56-019e93f0-3efe-7742-9598-bb06b36fb25a", "/Users/x/proj", "", false)
		want := "cd '/Users/x/proj' && codex --dangerously-bypass-approvals-and-sandbox resume '019e93f0-3efe-7742-9598-bb06b36fb25a'"
		if got != want {
			t.Errorf("DisplayResumeCommand = %q, want %q", got, want)
		}
	})

	t.Run("configured copilot flags are applied", func(t *testing.T) {
		withTempConfig(t, "copilot_flags = [\"--allow-all-tools\"]\n")
		got := DisplayResumeCommand("copilot", "sess-1", "/Users/x/proj", "", false)
		want := "cd '/Users/x/proj' && copilot --allow-all-tools --resume='sess-1'"
		if got != want {
			t.Errorf("DisplayResumeCommand = %q, want %q", got, want)
		}
	})

	t.Run("configured opencode flags are applied", func(t *testing.T) {
		withTempConfig(t, "opencode_flags = [\"--model\", \"anthropic/claude-sonnet-4\"]\n")
		got := DisplayResumeCommand("opencode", "sess-1", "/Users/x/proj", "", false)
		want := "cd '/Users/x/proj' && opencode --model anthropic/claude-sonnet-4 --session 'sess-1'"
		if got != want {
			t.Errorf("DisplayResumeCommand = %q, want %q", got, want)
		}
	})

	t.Run("configured pi flags are applied", func(t *testing.T) {
		withTempConfig(t, "pi_flags = [\"--offline\"]\n")
		got := DisplayResumeCommand("pi", "2026-06-18T13-47-19-786Z_019edafc-796a-79ce-a42b-f1d986bd3e8c", "/Users/x/proj", "", false)
		want := "cd '/Users/x/proj' && pi --offline --session '019edafc-796a-79ce-a42b-f1d986bd3e8c'"
		if got != want {
			t.Errorf("DisplayResumeCommand = %q, want %q", got, want)
		}
	})

	t.Run("each provider gets only its own flags", func(t *testing.T) {
		withTempConfig(t, "claude_flags = [\"--claude-only\"]\ncodex_flags = [\"--codex-only\"]\ncopilot_flags = [\"--copilot-only\"]\nopencode_flags = [\"--opencode-only\"]\npi_flags = [\"--pi-only\"]\n")
		cases := map[string]string{
			"claude":   "cd '/Users/x/proj' && claude --claude-only --resume 'sess-1'",
			"codex":    "cd '/Users/x/proj' && codex --codex-only resume 'sess-1'",
			"copilot":  "cd '/Users/x/proj' && copilot --copilot-only --resume='sess-1'",
			"opencode": "cd '/Users/x/proj' && opencode --opencode-only --session 'sess-1'",
			"pi":       "cd '/Users/x/proj' && pi --pi-only --session 'sess-1'",
		}
		for provider, want := range cases {
			if got := DisplayResumeCommand(provider, "sess-1", "/Users/x/proj", "", false); got != want {
				t.Errorf("DisplayResumeCommand(%s) = %q, want %q", provider, got, want)
			}
		}
	})

	t.Run("no flags configured yields bare command per provider", func(t *testing.T) {
		withTempConfig(t, "")
		cases := map[string]string{
			"claude":   "cd '/Users/x/proj' && claude --resume 'sess-1'",
			"codex":    "cd '/Users/x/proj' && codex resume 'sess-1'",
			"copilot":  "cd '/Users/x/proj' && copilot --resume='sess-1'",
			"opencode": "cd '/Users/x/proj' && opencode --session 'sess-1'",
			"pi":       "cd '/Users/x/proj' && pi --session 'sess-1'",
		}
		for provider, want := range cases {
			if got := DisplayResumeCommand(provider, "sess-1", "/Users/x/proj", "", false); got != want {
				t.Errorf("DisplayResumeCommand(%s) = %q, want %q", provider, got, want)
			}
		}
	})

	t.Run("working dir is project path, not lastCwd", func(t *testing.T) {
		withTempConfig(t, "")
		got := DisplayResumeCommand("copilot", "sess-1", "/Users/x/proj", "/Users/x/worktree", false)
		want := "cd '/Users/x/proj' && copilot --resume='sess-1'"
		if got != want {
			t.Errorf("DisplayResumeCommand = %q, want %q", got, want)
		}
	})

	t.Run("fork", func(t *testing.T) {
		withTempConfig(t, "")
		got := DisplayResumeCommand("claude", "sess-1", "/Users/x/proj", "", true)
		want := "cd '/Users/x/proj' && claude --resume 'sess-1' --fork-session"
		if got != want {
			t.Errorf("DisplayResumeCommand = %q, want %q", got, want)
		}
	})
}

func TestProviderFlags(t *testing.T) {
	cfg := &config.Config{
		ClaudeFlags:   []string{"--claude-only"},
		CodexFlags:    []string{"--codex-only"},
		CopilotFlags:  []string{"--copilot-only"},
		OpenCodeFlags: []string{"--opencode-only"},
		PiFlags:       []string{"--pi-only"},
	}
	cases := map[string]string{
		ProviderClaude:   "--claude-only",
		ProviderCodex:    "--codex-only",
		ProviderCopilot:  "--copilot-only",
		ProviderOpenCode: "--opencode-only",
		ProviderPi:       "--pi-only",
		// Unknown/empty providers resume via claude (see BuildResumeSpec), so
		// they get the claude flags.
		"":        "--claude-only",
		"unknown": "--claude-only",
	}
	for provider, want := range cases {
		got := ProviderFlags(cfg, provider)
		if len(got) != 1 || got[0] != want {
			t.Errorf("ProviderFlags(cfg, %q) = %v, want [%s]", provider, got, want)
		}
	}

	if got := ProviderFlags(nil, ProviderClaude); got != nil {
		t.Errorf("ProviderFlags(nil, claude) = %v, want nil", got)
	}
}

type fakeLaunchInfoSource struct {
	info    *db.Session
	lastCwd string
	err     error
}

func (f fakeLaunchInfoSource) GetSessionLaunchInfo(string) (*db.Session, string, error) {
	return f.info, f.lastCwd, f.err
}

func TestDisplayResumeCommandFor(t *testing.T) {
	t.Run("found", func(t *testing.T) {
		withTempConfig(t, "")
		src := fakeLaunchInfoSource{
			info:    &db.Session{SessionID: "sess-1", ProjectPath: "/Users/x/proj", Provider: "codex"},
			lastCwd: "/Users/x/elsewhere",
		}
		got := DisplayResumeCommandFor(src, "sess-1")
		want := "cd '/Users/x/proj' && codex resume 'sess-1'"
		if got != want {
			t.Errorf("DisplayResumeCommandFor = %q, want %q", got, want)
		}
	})

	t.Run("lookup failure falls back to bare command with comment", func(t *testing.T) {
		withTempConfig(t, "")
		src := fakeLaunchInfoSource{err: errors.New("not found")}
		got := DisplayResumeCommandFor(src, "sess-1")
		want := "claude --resume 'sess-1' # project path missing in DB for session sess-1"
		if got != want {
			t.Errorf("DisplayResumeCommandFor = %q, want %q", got, want)
		}
	})
}
