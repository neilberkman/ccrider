package importer

import (
	"os"
	"testing"

	"github.com/neilberkman/ccrider/internal/core/db"
	"github.com/neilberkman/ccrider/pkg/ccsessions"
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

	// Import it
	err = imp.ImportSession(session, 0)
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

	err = imp.ImportSession(session1, 0)
	if err != nil {
		t.Fatalf("ImportSession() error = %v", err)
	}

	// Import the same session again (simulating resumed session)
	err = imp.ImportSession(session1, 0)
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

	err = imp.ImportSession(session, 0)
	if err != nil {
		t.Fatalf("ImportSession() error = %v", err)
	}

	// Verify session was imported with correct sessionId (not filename)
	var sessionID string
	err = database.QueryRow("SELECT session_id FROM sessions").Scan(&sessionID)
	if err != nil {
		t.Fatal(err)
	}

	if sessionID != "test-session-123" {
		t.Errorf("Expected session_id 'test-session-123', got %s", sessionID)
	}
}
