package importer

import (
	"os"
	"testing"
	"time"

	"github.com/neilberkman/ccrider/internal/core/db"
	"github.com/neilberkman/ccrider/pkg/ccsessions"
	"github.com/neilberkman/ccrider/pkg/codexsessions"
)

func TestImportSession(t *testing.T) {
	// Setup test database
	tmpfile, err := os.CreateTemp("", "test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Remove(tmpfile.Name())
	}()
	_ = tmpfile.Close()

	database, err := db.New(tmpfile.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = database.Close()
	}()

	imp := New(database)

	// Parse test session
	session, err := ccsessions.ParseFile("../../../pkg/ccsessions/testdata/sample.jsonl")
	if err != nil {
		t.Fatal(err)
	}

	inode, device, _ := getFileIdentity(session.FilePath)
	hash, err := computeFileHash(session.FilePath)
	if err != nil {
		t.Fatal(err)
	}

	err = imp.ImportSession(session, 0, inode, device, hash, "claude")
	if err != nil {
		t.Fatalf("ImportSession() error = %v", err)
	}

	// Verify it was imported
	var count int
	err = database.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&count)
	if err != nil {
		t.Fatal(err)
	}

	if count != 1 {
		t.Errorf("Expected 1 session, got %d", count)
	}

	// Verify messages imported
	err = database.QueryRow("SELECT COUNT(*) FROM messages").Scan(&count)
	if err != nil {
		t.Fatal(err)
	}

	if count != 2 { // 2 messages in sample.jsonl
		t.Errorf("Expected 2 messages, got %d", count)
	}
}

func TestImportSession_ResumedSession(t *testing.T) {
	// Setup test database
	tmpfile, err := os.CreateTemp("", "test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Remove(tmpfile.Name())
	}()
	_ = tmpfile.Close()

	database, err := db.New(tmpfile.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = database.Close()
	}()

	imp := New(database)

	// Import first session
	session1, err := ccsessions.ParseFile("../../../pkg/ccsessions/testdata/sample.jsonl")
	if err != nil {
		t.Fatal(err)
	}

	inode, device, _ := getFileIdentity(session1.FilePath)
	hash, err := computeFileHash(session1.FilePath)
	if err != nil {
		t.Fatal(err)
	}

	err = imp.ImportSession(session1, 0, inode, device, hash, "claude")
	if err != nil {
		t.Fatalf("ImportSession() error = %v", err)
	}

	err = imp.ImportSession(session1, 0, inode, device, hash, "claude")
	if err != nil {
		t.Fatalf("ImportSession() second import error = %v", err)
	}

	// Should still have only 1 session
	var sessionCount int
	err = database.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&sessionCount)
	if err != nil {
		t.Fatal(err)
	}

	if sessionCount != 1 {
		t.Errorf("Expected 1 session after duplicate import, got %d", sessionCount)
	}

	// Should still have only 2 messages (duplicates ignored)
	var messageCount int
	err = database.QueryRow("SELECT COUNT(*) FROM messages").Scan(&messageCount)
	if err != nil {
		t.Fatal(err)
	}

	if messageCount != 2 {
		t.Errorf("Expected 2 messages after duplicate import, got %d", messageCount)
	}
}

func TestImportSession_AgentSession(t *testing.T) {
	// Setup test database
	tmpfile, err := os.CreateTemp("", "test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Remove(tmpfile.Name())
	}()
	_ = tmpfile.Close()

	database, err := db.New(tmpfile.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = database.Close()
	}()

	imp := New(database)

	// Parse and import agent session
	session, err := ccsessions.ParseFile("../../../pkg/ccsessions/testdata/agent-session.jsonl")
	if err != nil {
		t.Fatal(err)
	}

	inode, device, _ := getFileIdentity(session.FilePath)
	hash, err := computeFileHash(session.FilePath)
	if err != nil {
		t.Fatal(err)
	}

	err = imp.ImportSession(session, 0, inode, device, hash, "claude")
	if err != nil {
		t.Fatalf("ImportSession() error = %v", err)
	}

	// Verify session was imported with correct sessionId (not filename)
	var sessionID string
	err = database.QueryRow("SELECT session_id FROM sessions").Scan(&sessionID)
	if err != nil {
		t.Fatal(err)
	}

	if sessionID != "agent-session" {
		t.Errorf("Expected session_id 'agent-session' (filename), got %s", sessionID)
	}
}

func TestImportSession_CodexSession(t *testing.T) {
	tmpfile, err := os.CreateTemp("", "test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(tmpfile.Name()) }()
	_ = tmpfile.Close()

	database, err := db.New(tmpfile.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()

	imp := New(database)

	session, err := codexsessions.ParseFile("../../../pkg/codexsessions/testdata/sample.jsonl")
	if err != nil {
		t.Fatal(err)
	}

	inode, device, _ := getFileIdentity(session.FilePath)
	hash, err := computeFileHash(session.FilePath)
	if err != nil {
		t.Fatal(err)
	}

	err = imp.ImportSession(session, 0, inode, device, hash, "codex")
	if err != nil {
		t.Fatalf("ImportSession() error = %v", err)
	}

	// Verify provider is stored correctly
	var provider string
	err = database.QueryRow("SELECT provider FROM sessions").Scan(&provider)
	if err != nil {
		t.Fatal(err)
	}
	if provider != "codex" {
		t.Errorf("Expected provider 'codex', got %q", provider)
	}

	// Verify messages were imported
	var msgCount int
	err = database.QueryRow("SELECT COUNT(*) FROM messages").Scan(&msgCount)
	if err != nil {
		t.Fatal(err)
	}
	if msgCount != 4 {
		t.Errorf("Expected 4 messages, got %d", msgCount)
	}

	// Verify idempotent re-import
	err = imp.ImportSession(session, 0, inode, device, hash, "codex")
	if err != nil {
		t.Fatalf("ImportSession() re-import error = %v", err)
	}

	var sessionCount int
	err = database.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&sessionCount)
	if err != nil {
		t.Fatal(err)
	}
	if sessionCount != 1 {
		t.Errorf("Expected 1 session after re-import, got %d", sessionCount)
	}
}

func TestImportSession_ProviderStoredForClaude(t *testing.T) {
	tmpfile, err := os.CreateTemp("", "test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(tmpfile.Name()) }()
	_ = tmpfile.Close()

	database, err := db.New(tmpfile.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()

	imp := New(database)

	session, err := ccsessions.ParseFile("../../../pkg/ccsessions/testdata/sample.jsonl")
	if err != nil {
		t.Fatal(err)
	}

	inode, device, _ := getFileIdentity(session.FilePath)
	hash, err := computeFileHash(session.FilePath)
	if err != nil {
		t.Fatal(err)
	}

	err = imp.ImportSession(session, 0, inode, device, hash, "claude")
	if err != nil {
		t.Fatal(err)
	}

	var provider string
	err = database.QueryRow("SELECT provider FROM sessions").Scan(&provider)
	if err != nil {
		t.Fatal(err)
	}
	if provider != "claude" {
		t.Errorf("Expected provider 'claude', got %q", provider)
	}
}

// TestImportEnumeratedRefreshesEditedText verifies that when an enumerated
// provider (e.g. Copilot) rewrites a message's text in place — same UUID, new
// content — a re-sync updates both the stored text and the FTS index, rather
// than discarding the new text at the ON CONFLICT(uuid) clause.
func TestImportEnumeratedRefreshesEditedText(t *testing.T) {
	tmpfile, err := os.CreateTemp("", "test-edit-*.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(tmpfile.Name()) }()
	_ = tmpfile.Close()

	database, err := db.New(tmpfile.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()

	imp := New(database)

	makeSession := func(text string, mtime time.Time) *ccsessions.ParsedSession {
		return &ccsessions.ParsedSession{
			SessionID: "copilot-session",
			Summary:   "edit test",
			FilePath:  "/tmp/copilot/copilot-session.copilot",
			FileMtime: mtime,
			Messages: []ccsessions.ParsedMessage{{
				UUID:        "msg-stable-uuid",
				Type:        "assistant",
				Sender:      "assistant",
				TextContent: text,
				Timestamp:   mtime,
				Sequence:    1,
			}},
		}
	}

	// Initial import.
	t0 := time.Date(2026, 6, 3, 10, 0, 0, 0, time.UTC)
	if _, err := imp.ImportEnumerated([]*ccsessions.ParsedSession{makeSession("aardvark original", t0)}, nil, false, "copilot"); err != nil {
		t.Fatalf("initial import: %v", err)
	}

	textContent := func() string {
		var got string
		if err := database.QueryRow(`SELECT text_content FROM messages WHERE uuid = ?`, "msg-stable-uuid").Scan(&got); err != nil {
			t.Fatalf("query text: %v", err)
		}
		return got
	}
	ftsMatches := func(term string) int {
		var n int
		if err := database.QueryRow(`SELECT count(*) FROM messages_fts WHERE messages_fts MATCH ?`, term).Scan(&n); err != nil {
			t.Fatalf("query fts %q: %v", term, err)
		}
		return n
	}

	if got := textContent(); got != "aardvark original" {
		t.Fatalf("after initial import text = %q", got)
	}
	if ftsMatches("aardvark") != 1 || ftsMatches("beluga") != 0 {
		t.Fatalf("initial FTS state wrong: aardvark=%d beluga=%d", ftsMatches("aardvark"), ftsMatches("beluga"))
	}

	// Re-import the same UUID with rewritten text and a newer mtime (so the
	// session's change-detection hash differs and the import actually runs).
	t1 := t0.Add(time.Hour)
	if _, err := imp.ImportEnumerated([]*ccsessions.ParsedSession{makeSession("beluga rewritten", t1)}, nil, false, "copilot"); err != nil {
		t.Fatalf("re-import: %v", err)
	}

	if got := textContent(); got != "beluga rewritten" {
		t.Errorf("after re-import text = %q, want %q (edit was discarded)", got, "beluga rewritten")
	}
	if ftsMatches("beluga") != 1 {
		t.Errorf("FTS does not match new text 'beluga' after edit")
	}
	if ftsMatches("aardvark") != 0 {
		t.Errorf("FTS still matches stale text 'aardvark' after edit")
	}
}
