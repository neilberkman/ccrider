package antigravitysessions

import (
	"path/filepath"
	"testing"
	"time"
)

func TestParseAll(t *testing.T) {
	root := filepath.Join("testdata", "basic")
	sessions, err := ParseAll(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("ParseAll() returned %d sessions, want 1", len(sessions))
	}
	session := sessions[0]
	if session.SessionID != "11111111-2222-3333-4444-555555555555" || session.ImportID != session.SessionID {
		t.Fatalf("session identifiers = %q/%q", session.SessionID, session.ImportID)
	}
	if session.ProjectPath != "/redacted/antigravity-project" {
		t.Fatalf("ProjectPath = %q", session.ProjectPath)
	}
	if session.Summary != "Fix Antigravity support" {
		t.Fatalf("Summary = %q", session.Summary)
	}
	if len(session.Messages) != 2 {
		t.Fatalf("message count = %d, want 2", len(session.Messages))
	}
	user := session.Messages[0]
	if user.Type != "user" || user.Sender != "human" || user.TextContent != "Fix Antigravity support" {
		t.Fatalf("user = %#v", user)
	}
	if user.CWD != session.ProjectPath {
		t.Fatalf("user CWD = %q, want %q", user.CWD, session.ProjectPath)
	}
	assistant := session.Messages[1]
	if assistant.Type != "assistant" || assistant.Sender != "assistant" || assistant.TextContent != "Antigravity support is ready." {
		t.Fatalf("assistant = %#v", assistant)
	}
	if assistant.UUID == "" || assistant.UUID == user.UUID {
		t.Fatalf("message UUIDs = %q/%q", user.UUID, assistant.UUID)
	}
}

func TestParseAllReturnsNoSessionsForMissingRoot(t *testing.T) {
	sessions, err := ParseAll(filepath.Join(t.TempDir(), "missing"))
	if err != nil {
		t.Fatal(err)
	}
	if sessions != nil {
		t.Fatalf("sessions = %v, want nil", sessions)
	}
}

func TestWorkspaceIndexFallsBackToHistory(t *testing.T) {
	index := workspaceIndex{history: []historyEntry{{
		Display:   "old conversation",
		Timestamp: 1783715875448,
		Workspace: "/redacted/older-project",
	}}}
	got := index.workspaceFor("not-the-latest", "old conversation", time.UnixMilli(1783715875448))
	if got != "/redacted/older-project" {
		t.Fatalf("workspaceFor() = %q", got)
	}
}
