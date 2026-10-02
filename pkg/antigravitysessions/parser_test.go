package antigravitysessions

import (
	"database/sql"
	"encoding/hex"
	"os"
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

func TestDefaultRoots(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	want := []Root{
		{Path: filepath.Join(home, ".gemini", "antigravity-cli"), Provider: "antigravity"},
		{Path: filepath.Join(home, ".gemini", "antigravity-ide"), Provider: "antigravity-ide"},
		{Path: filepath.Join(home, ".gemini", "antigravity"), Provider: "antigravity-desktop"},
	}
	got := DefaultRoots()
	if len(got) != len(want) {
		t.Fatalf("DefaultRoots() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("DefaultRoots()[%d] = %v, want %v", i, got[i], want[i])
		}
	}
	if DefaultRoot() != want[0].Path {
		t.Errorf("DefaultRoot() = %q, want the CLI root %q", DefaultRoot(), want[0].Path)
	}
}

// The desktop app writes no cache/last_conversations.json or history.jsonl;
// its workspaces are only in conversation_summaries.db.
func TestParseAllResolvesWorkspaceFromSummaries(t *testing.T) {
	const conversationID = "22222222-3333-4444-5555-666666666666"
	root := t.TempDir()
	writeTranscript(t, root, conversationID)
	execSQL(t, filepath.Join(root, "conversation_summaries.db"),
		"CREATE TABLE conversation_summaries (conversation_id text PRIMARY KEY, workspace_uris text NOT NULL)",
		`INSERT INTO conversation_summaries VALUES ('`+conversationID+`', '["file:///redacted/desktop%20project","file:///redacted/other"]')`,
	)

	sessions, err := ParseAll(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].ProjectPath != "/redacted/desktop project" {
		t.Fatalf("sessions = %+v, want one session in /redacted/desktop project", sessions)
	}
}

// The IDE writes no conversation_summaries.db either; the workspace is only
// in the conversation's own database.
func TestParseAllResolvesWorkspaceFromConversationDB(t *testing.T) {
	const conversationID = "33333333-4444-5555-6666-777777777777"
	root := t.TempDir()
	writeTranscript(t, root, conversationID)
	if err := os.MkdirAll(filepath.Join(root, "conversations"), 0755); err != nil {
		t.Fatal(err)
	}
	workspace := protoField(1, []byte("file:///redacted/ide-project"))
	workspace = append(workspace, protoField(2, []byte("file:///redacted/ide-project"))...)
	metadata := append([]byte{0x10, 0x07}, protoField(1, workspace)...) // a varint field, then the workspace
	execSQL(t, filepath.Join(root, "conversations", conversationID+".db"),
		"CREATE TABLE trajectory_metadata_blob (id text PRIMARY KEY, data blob)",
		"INSERT INTO trajectory_metadata_blob VALUES ('main', x'"+hex.EncodeToString(metadata)+"')",
	)

	sessions, err := ParseAll(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].ProjectPath != "/redacted/ide-project" {
		t.Fatalf("sessions = %+v, want one session in /redacted/ide-project", sessions)
	}
}

func TestWorkspaceForTrustsSummariesOverConversationDB(t *testing.T) {
	index := workspaceIndex{
		summaries:        map[string]string{"listed": ""},
		conversationsDir: filepath.Join(t.TempDir(), "missing"),
	}
	if got := index.workspaceFor("listed", "", time.Time{}); got != "" {
		t.Fatalf("workspaceFor(listed) = %q, want no workspace", got)
	}
	if got := index.workspaceFor("unlisted", "", time.Time{}); got != "" {
		t.Fatalf("workspaceFor(unlisted) = %q, want no workspace", got)
	}
}

func TestProtoBytesFieldsStopsAtMalformedInput(t *testing.T) {
	message := append(protoField(1, []byte("ok")), 0x0a, 0x7f, 'x') // second field claims 127 bytes
	got := protoBytesFields(message, 1)
	if len(got) != 1 || string(got[0]) != "ok" {
		t.Fatalf("protoBytesFields() = %q, want only the well-formed field", got)
	}
}

func writeTranscript(t *testing.T, root, conversationID string) {
	t.Helper()
	dir := filepath.Join(root, "brain", conversationID, ".system_generated", "logs")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	transcript := `{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-10-01T14:07:33Z","content":"<USER_REQUEST>\nhello\n</USER_REQUEST>"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "transcript.jsonl"), []byte(transcript), 0644); err != nil {
		t.Fatal(err)
	}
}

func execSQL(t *testing.T, dbPath string, statements ...string) {
	t.Helper()
	conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	for _, statement := range statements {
		if _, err := conn.Exec(statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
}

// protoField encodes one length-delimited protobuf field (payloads under 128 bytes).
func protoField(number byte, payload []byte) []byte {
	return append([]byte{number<<3 | 2, byte(len(payload))}, payload...)
}
