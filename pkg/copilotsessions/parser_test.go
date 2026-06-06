package copilotsessions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSession creates a session-state/<id>/ directory with the given
// events.jsonl lines and (optionally) a workspace.yaml name.
func writeSession(t *testing.T, stateDir, sessionID, workspaceName string, eventLines ...string) {
	t.Helper()
	dir := filepath.Join(stateDir, sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if len(eventLines) > 0 {
		if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(strings.Join(eventLines, "\n")+"\n"), 0o644); err != nil {
			t.Fatalf("write events: %v", err)
		}
	}
	if workspaceName != "" {
		ws := "id: " + sessionID + "\nname: " + workspaceName + "\ncwd: /from/workspace\n"
		if err := os.WriteFile(filepath.Join(dir, "workspace.yaml"), []byte(ws), 0o644); err != nil {
			t.Fatalf("write workspace: %v", err)
		}
	}
}

const (
	evStart = `{"type":"session.start","id":"start-1","timestamp":"2026-06-03T12:13:05.376Z","data":{"context":{"cwd":"/Users/dmd/project"}}}`
	evUser0 = `{"type":"user.message","id":"u0","parentId":"start-1","timestamp":"2026-06-03T12:13:11.138Z","data":{"content":"make it build"}}`
	evAsst0 = `{"type":"assistant.message","id":"a0","parentId":"u0","timestamp":"2026-06-03T12:14:16.948Z","data":{"content":"Done, build is green."}}`
	// assistant.message with empty content (tool-only step) must be dropped.
	evAsstEmpty = `{"type":"assistant.message","id":"a-empty","parentId":"u0","timestamp":"2026-06-03T12:14:17.000Z","data":{"content":"","toolRequests":[{"name":"shell"}]}}`
	evUser1     = `{"type":"user.message","id":"u1","parentId":"a0","timestamp":"2026-06-03T12:15:00.000Z","data":{"content":"thanks"}}`
	// non-conversational events that must be ignored.
	evSystem = `{"type":"system.message","id":"sys-1","timestamp":"2026-06-03T12:13:05.500Z","data":{"infoType":"folder_trust","message":"trusted"}}`
)

func TestParseAll(t *testing.T) {
	stateDir := t.TempDir()
	writeSession(t, stateDir, "sess-a", "Fix the build",
		evStart, evSystem, evUser0, evAsst0, evAsstEmpty, evUser1)

	sessions, err := ParseAll(stateDir)
	if err != nil {
		t.Fatalf("ParseAll: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions, want 1", len(sessions))
	}

	s := sessions[0]
	if s.SessionID != "sess-a" {
		t.Errorf("SessionID = %q, want sess-a", s.SessionID)
	}
	if s.Summary != "Fix the build" {
		t.Errorf("Summary = %q, want workspace name", s.Summary)
	}
	// Importer derives the session key from the file basename; it must round-trip
	// to the Copilot UUID (the session-state directory name).
	base := filepath.Base(s.FilePath)
	if got := base[:len(base)-len(filepath.Ext(base))]; got != "sess-a" {
		t.Errorf("FilePath basename = %q, want session id sess-a", got)
	}

	// 3 conversational messages: user, assistant(content), user.
	// The empty assistant.message and the system.message are dropped.
	if len(s.Messages) != 3 {
		t.Fatalf("got %d messages, want 3", len(s.Messages))
	}
	want := []struct{ uuid, sender, text string }{
		{"u0", "human", "make it build"},
		{"a0", "assistant", "Done, build is green."},
		{"u1", "human", "thanks"},
	}
	for i, w := range want {
		m := s.Messages[i]
		if m.UUID != w.uuid || m.Sender != w.sender || m.TextContent != w.text {
			t.Errorf("message[%d] = (%q,%q,%q), want (%q,%q,%q)",
				i, m.UUID, m.Sender, m.TextContent, w.uuid, w.sender, w.text)
		}
		if m.CWD != "/Users/dmd/project" {
			t.Errorf("message[%d] CWD = %q, want cwd from session.start", i, m.CWD)
		}
	}
	if s.Messages[1].ParentUUID != "u0" {
		t.Errorf("assistant ParentUUID = %q, want u0", s.Messages[1].ParentUUID)
	}
}

func TestParseAllSummaryFallback(t *testing.T) {
	stateDir := t.TempDir()
	// No workspace.yaml name -> summary falls back to first user message.
	writeSession(t, stateDir, "sess-b", "", evStart, evUser0, evAsst0)

	sessions, err := ParseAll(stateDir)
	if err != nil {
		t.Fatalf("ParseAll: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions, want 1", len(sessions))
	}
	if sessions[0].Summary != "make it build" {
		t.Errorf("Summary fallback = %q, want first user message", sessions[0].Summary)
	}
}

func TestParseAllCwdFromWorkspaceWhenNoSessionStart(t *testing.T) {
	stateDir := t.TempDir()
	// No session.start event -> cwd comes from workspace.yaml.
	writeSession(t, stateDir, "sess-c", "Named", evUser0, evAsst0)

	sessions, err := ParseAll(stateDir)
	if err != nil {
		t.Fatalf("ParseAll: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions, want 1", len(sessions))
	}
	if sessions[0].Messages[0].CWD != "/from/workspace" {
		t.Errorf("CWD = %q, want /from/workspace", sessions[0].Messages[0].CWD)
	}
}

func TestParseAllSkipsSessionsWithoutConversation(t *testing.T) {
	stateDir := t.TempDir()
	// Has a conversation -> kept.
	writeSession(t, stateDir, "real", "Real", evStart, evUser0, evAsst0)
	// Directory with no events.jsonl -> skipped.
	if err := os.MkdirAll(filepath.Join(stateDir, "no-events"), 0o755); err != nil {
		t.Fatal(err)
	}
	// events.jsonl with only non-conversational events -> skipped.
	writeSession(t, stateDir, "only-system", "Sys", evStart, evSystem)

	sessions, err := ParseAll(stateDir)
	if err != nil {
		t.Fatalf("ParseAll: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions, want 1 (only the real conversation)", len(sessions))
	}
	if sessions[0].SessionID != "real" {
		t.Errorf("kept %q, want real", sessions[0].SessionID)
	}
}

func TestParseAllMissingStateDir(t *testing.T) {
	sessions, err := ParseAll(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("ParseAll on missing dir should not error, got %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("got %d sessions, want 0", len(sessions))
	}
}

func TestReadWorkspace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "workspace.yaml")
	content := "id: abc\nname: My Session: With Colon\ncwd: /tmp/x\nuser_named: false\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	name, cwd := readWorkspace(path)
	if name != "My Session: With Colon" {
		t.Errorf("name = %q, want full value past first colon", name)
	}
	if cwd != "/tmp/x" {
		t.Errorf("cwd = %q, want /tmp/x", cwd)
	}
}
