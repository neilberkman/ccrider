package db

import (
	"os"
	"testing"
)

func TestFTSSearch(t *testing.T) {
	tmpfile, err := os.CreateTemp("", "test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(tmpfile.Name()) }()
	_ = tmpfile.Close()

	database, err := New(tmpfile.Name())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { _ = database.Close() }()

	// Insert a session
	result, err := database.Exec(`
		INSERT INTO sessions (
			session_id, project_path, summary, created_at, updated_at
		) VALUES (?, ?, ?, datetime('now'), datetime('now'))
	`, "test-session-123", "/test/project", "Test session")
	if err != nil {
		t.Fatalf("Failed to insert session: %v", err)
	}

	sessionID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("Failed to get session ID: %v", err)
	}

	// Insert messages with different content
	messages := []struct {
		uuid    string
		content string
	}{
		{"msg-1", "Hello world this is a test"},
		{"msg-2", "Let's write some authentication code"},
		{"msg-3", "The getUserById function returns a user"},
		{"msg-4", "camelCaseVariable should be preserved"},
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

	// Test porter stemming search (natural language)
	t.Run("PorterStemming", func(t *testing.T) {
		// "authentication" should match "authentication" even with stemming
		rows, err := database.Query(`
			SELECT m.uuid, m.text_content
			FROM messages m
			JOIN messages_fts ON messages_fts.rowid = m.id
			WHERE messages_fts MATCH ?
		`, "authentication")
		if err != nil {
			t.Fatalf("FTS query failed: %v", err)
		}
		defer func() { _ = rows.Close() }()

		count := 0
		for rows.Next() {
			var uuid, content string
			if err := rows.Scan(&uuid, &content); err != nil {
				t.Fatalf("Scan failed: %v", err)
			}
			count++
			if uuid != "msg-2" {
				t.Errorf("Expected msg-2, got %s", uuid)
			}
		}

		if count != 1 {
			t.Errorf("Expected 1 result, got %d", count)
		}
	})

	// Test code search (unicode61 without stemming)
	t.Run("CodeSearch", func(t *testing.T) {
		// Search for camelCase - should work with unicode61
		rows, err := database.Query(`
			SELECT m.uuid, m.text_content
			FROM messages m
			JOIN messages_fts_code ON messages_fts_code.rowid = m.id
			WHERE messages_fts_code MATCH ?
		`, "camelCase*")
		if err != nil {
			t.Fatalf("FTS code query failed: %v", err)
		}
		defer func() { _ = rows.Close() }()

		count := 0
		for rows.Next() {
			var uuid, content string
			if err := rows.Scan(&uuid, &content); err != nil {
				t.Fatalf("Scan failed: %v", err)
			}
			count++
			if uuid != "msg-4" {
				t.Errorf("Expected msg-4, got %s", uuid)
			}
		}

		if count != 1 {
			t.Errorf("Expected 1 result, got %d", count)
		}
	})

	// Test phrase search
	t.Run("PhraseSearch", func(t *testing.T) {
		rows, err := database.Query(`
			SELECT m.uuid, m.text_content
			FROM messages m
			JOIN messages_fts ON messages_fts.rowid = m.id
			WHERE messages_fts MATCH ?
		`, `"Hello world"`)
		if err != nil {
			t.Fatalf("Phrase search failed: %v", err)
		}
		defer func() { _ = rows.Close() }()

		count := 0
		for rows.Next() {
			var uuid, content string
			if err := rows.Scan(&uuid, &content); err != nil {
				t.Fatalf("Scan failed: %v", err)
			}
			count++
			if uuid != "msg-1" {
				t.Errorf("Expected msg-1, got %s", uuid)
			}
		}

		if count != 1 {
			t.Errorf("Expected 1 result, got %d", count)
		}
	})

	// Test wildcard search
	t.Run("WildcardSearch", func(t *testing.T) {
		rows, err := database.Query(`
			SELECT m.uuid, m.text_content
			FROM messages m
			JOIN messages_fts_code ON messages_fts_code.rowid = m.id
			WHERE messages_fts_code MATCH ?
		`, "getUser*")
		if err != nil {
			t.Fatalf("Wildcard search failed: %v", err)
		}
		defer func() { _ = rows.Close() }()

		count := 0
		for rows.Next() {
			var uuid, content string
			if err := rows.Scan(&uuid, &content); err != nil {
				t.Fatalf("Scan failed: %v", err)
			}
			count++
			if uuid != "msg-3" {
				t.Errorf("Expected msg-3, got %s", uuid)
			}
		}

		if count != 1 {
			t.Errorf("Expected 1 result, got %d", count)
		}
	})
}

func TestFTSTriggers(t *testing.T) {
	tmpfile, err := os.CreateTemp("", "test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(tmpfile.Name()) }()
	_ = tmpfile.Close()

	database, err := New(tmpfile.Name())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { _ = database.Close() }()

	// Insert session
	result, err := database.Exec(`
		INSERT INTO sessions (
			session_id, project_path, summary, created_at, updated_at
		) VALUES (?, ?, ?, datetime('now'), datetime('now'))
	`, "test-session", "/test", "Test")
	if err != nil {
		t.Fatal(err)
	}

	sessionID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}

	// Insert message
	msgResult, err := database.Exec(`
		INSERT INTO messages (
			uuid, session_id, type, text_content, timestamp, sequence
		) VALUES (?, ?, ?, ?, datetime('now'), ?)
	`, "msg-1", sessionID, "user", "original content", 1)
	if err != nil {
		t.Fatal(err)
	}

	msgID, err := msgResult.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}

	// Verify FTS was populated
	var count int
	err = database.QueryRow("SELECT COUNT(*) FROM messages_fts").Scan(&count)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("Expected 1 FTS entry after insert, got %d", count)
	}

	// Update message
	_, err = database.Exec("UPDATE messages SET text_content = ? WHERE id = ?", "updated content", msgID)
	if err != nil {
		t.Fatal(err)
	}

	// Search for updated content
	var foundContent string
	err = database.QueryRow(`
		SELECT m.text_content
		FROM messages m
		JOIN messages_fts ON messages_fts.rowid = m.id
		WHERE messages_fts MATCH ?
	`, "updated").Scan(&foundContent)
	if err != nil {
		t.Fatalf("Failed to find updated content: %v", err)
	}

	if foundContent != "updated content" {
		t.Errorf("Expected 'updated content', got '%s'", foundContent)
	}

	// Delete message
	_, err = database.Exec("DELETE FROM messages WHERE id = ?", msgID)
	if err != nil {
		t.Fatal(err)
	}

	// Verify FTS was cleaned up
	err = database.QueryRow("SELECT COUNT(*) FROM messages_fts").Scan(&count)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("Expected 0 FTS entries after delete, got %d", count)
	}
}
