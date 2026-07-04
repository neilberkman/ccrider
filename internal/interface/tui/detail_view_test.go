package tui

import (
	"os"
	"strings"
	"testing"
)

// Every resume command the TUI emits must include the `cd <project> && `
// prefix: providers scope session storage by the directory the session was
// created in, so a bare resume command fails from any other cwd.
//
// isolateConfig points HOME at an empty temp dir so the commands built via
// session.DisplayResumeCommand don't pick up the developer's real
// ~/.config/ccrider (e.g. configured provider flags).
func isolateConfig(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
}

func TestLaunchSessionMessageHasCdPrefix(t *testing.T) {
	isolateConfig(t)
	msg := launchSession("claude", "sess-1", "/Users/x/proj", "/Users/x/elsewhere", "", "", false)()
	lm, ok := msg.(sessionLaunchedMsg)
	if !ok {
		t.Fatalf("expected sessionLaunchedMsg, got %T", msg)
	}
	want := "cd '/Users/x/proj' && claude --resume 'sess-1'"
	if lm.message != want {
		t.Errorf("message = %q, want %q", lm.message, want)
	}
}

func TestWriteCommandToFileHasCdPrefix(t *testing.T) {
	isolateConfig(t)
	msg := writeCommandToFile("codex", "rollout-2026-06-04T14-40-56-019e93f0-3efe-7742-9598-bb06b36fb25a", "/Users/x/proj", "/Users/x/elsewhere")()
	lm, ok := msg.(sessionLaunchedMsg)
	if !ok {
		t.Fatalf("expected sessionLaunchedMsg, got %T", msg)
	}
	if lm.err != nil {
		t.Fatalf("writeCommandToFile error: %v", lm.err)
	}

	data, err := os.ReadFile("/tmp/ccrider-cmd.sh")
	if err != nil {
		t.Fatalf("reading command file: %v", err)
	}
	want := "cd '/Users/x/proj' && codex resume '019e93f0-3efe-7742-9598-bb06b36fb25a'"
	if !strings.Contains(string(data), want) {
		t.Errorf("command file = %q, want it to contain %q", string(data), want)
	}
}

func TestViewTerminalFallbackShowsCdPrefix(t *testing.T) {
	isolateConfig(t)
	// lastCwd differs from projectPath: the displayed command must cd to the
	// PROJECT path — resuming from lastCwd would not find the session.
	m := Model{
		fallbackProvider:    "copilot",
		fallbackSessionID:   "sess-1",
		fallbackProjectPath: "/Users/x/proj",
		fallbackLastCwd:     "/Users/x/elsewhere",
	}
	out := m.viewTerminalFallback()
	want := "cd '/Users/x/proj' && copilot --resume='sess-1'"
	if !strings.Contains(out, want) {
		t.Errorf("fallback view = %q, want it to contain %q", out, want)
	}
	if strings.Contains(out, "elsewhere") {
		t.Errorf("fallback view must not cd to lastCwd: %q", out)
	}
}

func TestViewTerminalFallbackMissingProjectPath(t *testing.T) {
	isolateConfig(t)
	m := Model{
		fallbackProvider:  "claude",
		fallbackSessionID: "sess-1",
	}
	out := m.viewTerminalFallback()
	want := "claude --resume 'sess-1' # project path missing in DB for session sess-1"
	if !strings.Contains(out, want) {
		t.Errorf("fallback view = %q, want it to contain %q", out, want)
	}
}
