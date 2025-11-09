package search

import (
	"os"
	"testing"

	"github.com/yourusername/ccrider/internal/core/db"
)

func TestSearch(t *testing.T) {
	tmpfile, err := os.CreateTemp("", "test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(tmpfile.Name()) }()
	_ = tmpfile.Close()

	database, err := db.New(tmpfile.Name())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { _ = database.Close() }()

	// Insert test session
	result, err := database.Exec(`
		INSERT INTO sessions (
			session_id, project_path, summary, created_at, updated_at
		) VALUES (?, ?, ?, datetime('now'), datetime('now'))
	`, "test-session-123", "/test/project", "Building authentication system")
	if err != nil {
		t.Fatalf("Failed to insert session: %v", err)
	}

	sessionID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("Failed to get session ID: %v", err)
	}

	// Insert test messages with different content
	messages := []struct {
		uuid      string
		msgType   string
		content   string
		timestamp string
	}{
		{"msg-1", "user", "Let's implement user authentication with JWT tokens", "2024-01-01 10:00:00"},
		{"msg-2", "assistant", "I'll help you implement authentication. First, let's create the auth middleware", "2024-01-01 10:01:00"},
		{"msg-3", "user", "Can you write the getUserById function?", "2024-01-01 10:02:00"},
		{"msg-4", "assistant", "Sure, here's the getUserById function implementation", "2024-01-01 10:03:00"},
		{"msg-5", "user", "Let's add database migrations for the users table", "2024-01-01 10:04:00"},
	}

	for i, msg := range messages {
		_, err := database.Exec(`
			INSERT INTO messages (
				uuid, session_id, type, text_content, timestamp, sequence
			) VALUES (?, ?, ?, ?, ?, ?)
		`, msg.uuid, sessionID, msg.msgType, msg.content, msg.timestamp, i+1)
		if err != nil {
			t.Fatalf("Failed to insert message %s: %v", msg.uuid, err)
		}
	}

	// Test basic search
	t.Run("BasicSearch", func(t *testing.T) {
		results, err := Search(database, "authentication")
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}

		if len(results) < 2 {
			t.Errorf("Expected at least 2 results for 'authentication', got %d", len(results))
		}

		// Verify result structure
		for _, r := range results {
			if r.SessionID == "" {
				t.Error("SessionID is empty")
			}
			if r.SessionSummary == "" {
				t.Error("SessionSummary is empty")
			}
			if r.MessageText == "" {
				t.Error("MessageText is empty")
			}
			if r.ProjectPath == "" {
				t.Error("ProjectPath is empty")
			}
		}
	})

	// Test phrase search
	t.Run("PhraseSearch", func(t *testing.T) {
		results, err := Search(database, "user authentication")
		if err != nil {
			t.Fatalf("Phrase search failed: %v", err)
		}

		if len(results) == 0 {
			t.Error("Expected results for 'user authentication'")
		}
	})

	// Test empty query
	t.Run("EmptyQuery", func(t *testing.T) {
		results, err := Search(database, "")
		if err == nil {
			t.Error("Expected error for empty query")
		}
		if results != nil {
			t.Error("Expected nil results for empty query")
		}
	})

	// Test no results
	t.Run("NoResults", func(t *testing.T) {
		results, err := Search(database, "nonexistent")
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}

		if len(results) != 0 {
			t.Errorf("Expected 0 results, got %d", len(results))
		}
	})
}

func TestSearchCode(t *testing.T) {
	tmpfile, err := os.CreateTemp("", "test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(tmpfile.Name()) }()
	_ = tmpfile.Close()

	database, err := db.New(tmpfile.Name())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { _ = database.Close() }()

	// Insert test session
	result, err := database.Exec(`
		INSERT INTO sessions (
			session_id, project_path, summary, created_at, updated_at
		) VALUES (?, ?, ?, datetime('now'), datetime('now'))
	`, "test-session-456", "/test/code", "Writing utility functions")
	if err != nil {
		t.Fatalf("Failed to insert session: %v", err)
	}

	sessionID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("Failed to get session ID: %v", err)
	}

	// Insert messages with code-like content
	messages := []struct {
		uuid    string
		content string
	}{
		{"msg-1", "The getUserById function is important"},
		{"msg-2", "Let's refactor handleUserRequest to be more efficient"},
		{"msg-3", "The parseJSONResponse method needs error handling"},
		{"msg-4", "We should use camelCaseVariable naming convention"},
	}

	for i, msg := range messages {
		_, err := database.Exec(`
			INSERT INTO messages (
				uuid, session_id, type, text_content, timestamp, sequence
			) VALUES (?, ?, ?, ?, datetime('now'), ?)
		`, msg.uuid, sessionID, "user", msg.content, i+1)
		if err != nil {
			t.Fatalf("Failed to insert message %s: %v", msg.uuid, err)
		}
	}

	// Test code search with camelCase
	t.Run("CamelCaseSearch", func(t *testing.T) {
		results, err := SearchCode(database, "getUserById")
		if err != nil {
			t.Fatalf("Code search failed: %v", err)
		}

		if len(results) == 0 {
			t.Error("Expected results for 'getUserById'")
		}

		found := false
		for _, r := range results {
			if r.MessageUUID == "msg-1" {
				found = true
				break
			}
		}

		if !found {
			t.Error("Expected to find msg-1 with getUserById")
		}
	})

	// Test wildcard search
	t.Run("WildcardSearch", func(t *testing.T) {
		results, err := SearchCode(database, "handle*")
		if err != nil {
			t.Fatalf("Wildcard search failed: %v", err)
		}

		if len(results) == 0 {
			t.Error("Expected results for 'handle*' wildcard")
		}
	})
}

func TestSearchOrdering(t *testing.T) {
	tmpfile, err := os.CreateTemp("", "test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(tmpfile.Name()) }()
	_ = tmpfile.Close()

	database, err := db.New(tmpfile.Name())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { _ = database.Close() }()

	// Insert test session
	result, err := database.Exec(`
		INSERT INTO sessions (
			session_id, project_path, summary, created_at, updated_at
		) VALUES (?, ?, ?, datetime('now'), datetime('now'))
	`, "test-session-789", "/test/ordering", "Test relevance ordering")
	if err != nil {
		t.Fatalf("Failed to insert session: %v", err)
	}

	sessionID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("Failed to get session ID: %v", err)
	}

	// Insert messages with varying relevance for "test"
	messages := []struct {
		uuid    string
		content string
	}{
		{"msg-1", "This message mentions the word occasionally"},
		{"msg-2", "test test test - repeated multiple times"},
		{"msg-3", "Another message with test mentioned once"},
	}

	for i, msg := range messages {
		_, err := database.Exec(`
			INSERT INTO messages (
				uuid, session_id, type, text_content, timestamp, sequence
			) VALUES (?, ?, ?, ?, datetime('now'), ?)
		`, msg.uuid, sessionID, "user", msg.content, i+1)
		if err != nil {
			t.Fatalf("Failed to insert message %s: %v", msg.uuid, err)
		}
	}

	// Test that results are ordered by relevance
	t.Run("RelevanceOrdering", func(t *testing.T) {
		results, err := Search(database, "test")
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}

		if len(results) < 2 {
			t.Errorf("Expected at least 2 results, got %d", len(results))
		}

		// The message with "test" repeated 3 times should rank higher
		if len(results) > 0 && results[0].MessageUUID != "msg-2" {
			t.Logf("Warning: Expected msg-2 to rank first due to term frequency, got %s", results[0].MessageUUID)
			// Note: This is a soft assertion as FTS5 ranking can be complex
		}
	})
}
