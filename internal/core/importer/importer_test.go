package importer

import (
	"os"
	"testing"

	"github.com/yourusername/ccrider/internal/core/db"
	"github.com/yourusername/ccrider/pkg/ccsessions"
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
	err = imp.ImportSession(session)
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
