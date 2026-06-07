package session

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/neilberkman/ccrider/internal/core/db"
)

// withTempConfig points HOME at a temp dir, optionally writing a config.toml,
// so tests control exactly what ConfiguredClaudeFlags sees.
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
