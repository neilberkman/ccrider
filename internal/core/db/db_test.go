package db

import (
	"os"
	"testing"
)

func TestNew(t *testing.T) {
	// Use temp file for test DB
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

	// Verify schema initialized
	var count int
	err = database.conn.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table'").Scan(&count)
	if err != nil {
		t.Fatalf("Failed to query schema: %v", err)
	}

	// Should have: sessions, messages, tool_uses, import_log, messages_fts, messages_fts_code
	if count < 4 {
		t.Errorf("Expected at least 4 tables, got %d", count)
	}
}

func TestNew_WALMode(t *testing.T) {
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

	// Verify WAL mode is enabled
	var journalMode string
	err = database.conn.QueryRow("PRAGMA journal_mode").Scan(&journalMode)
	if err != nil {
		t.Fatalf("Failed to query journal mode: %v", err)
	}

	if journalMode != "wal" {
		t.Errorf("Expected WAL mode, got %s", journalMode)
	}
}

func TestNew_ForeignKeys(t *testing.T) {
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

	// Verify foreign keys are enabled
	var fkEnabled int
	err = database.conn.QueryRow("PRAGMA foreign_keys").Scan(&fkEnabled)
	if err != nil {
		t.Fatalf("Failed to query foreign keys: %v", err)
	}

	if fkEnabled != 1 {
		t.Errorf("Expected foreign keys enabled (1), got %d", fkEnabled)
	}
}

func TestSchemaCreation(t *testing.T) {
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

	// Test that sessions table exists with correct columns
	var columnCount int
	err = database.conn.QueryRow("SELECT COUNT(*) FROM pragma_table_info('sessions')").Scan(&columnCount)
	if err != nil {
		t.Fatalf("Failed to query sessions columns: %v", err)
	}

	// sessions should have: id, session_id, project_path, summary, leaf_uuid, cwd, git_branch,
	// created_at, updated_at, message_count, version, imported_at, last_synced_at,
	// file_hash, file_size, file_mtime
	if columnCount < 15 {
		t.Errorf("Expected at least 15 columns in sessions table, got %d", columnCount)
	}

	// Test that messages table exists with correct columns
	err = database.conn.QueryRow("SELECT COUNT(*) FROM pragma_table_info('messages')").Scan(&columnCount)
	if err != nil {
		t.Fatalf("Failed to query messages columns: %v", err)
	}

	// messages should have: id, uuid, session_id, parent_uuid, type, sender, content,
	// text_content, timestamp, sequence, is_sidechain, cwd, git_branch, version
	if columnCount < 13 {
		t.Errorf("Expected at least 13 columns in messages table, got %d", columnCount)
	}
}

func TestFTS5Tables(t *testing.T) {
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

	// Verify messages_fts virtual table exists
	var ftsExists int
	err = database.conn.QueryRow(`
		SELECT COUNT(*) FROM sqlite_master
		WHERE type='table' AND name='messages_fts'
	`).Scan(&ftsExists)
	if err != nil {
		t.Fatalf("Failed to check FTS table: %v", err)
	}

	if ftsExists != 1 {
		t.Errorf("Expected messages_fts table to exist")
	}

	// Verify messages_fts_code virtual table exists
	err = database.conn.QueryRow(`
		SELECT COUNT(*) FROM sqlite_master
		WHERE type='table' AND name='messages_fts_code'
	`).Scan(&ftsExists)
	if err != nil {
		t.Fatalf("Failed to check FTS code table: %v", err)
	}

	if ftsExists != 1 {
		t.Errorf("Expected messages_fts_code table to exist")
	}
}

func TestIndexes(t *testing.T) {
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

	// Verify indexes exist
	var indexCount int
	err = database.conn.QueryRow(`
		SELECT COUNT(*) FROM sqlite_master
		WHERE type='index' AND tbl_name='sessions'
	`).Scan(&indexCount)
	if err != nil {
		t.Fatalf("Failed to count session indexes: %v", err)
	}

	// Should have indexes on: session_id, project_path, updated_at, git_branch
	if indexCount < 4 {
		t.Errorf("Expected at least 4 indexes on sessions, got %d", indexCount)
	}

	err = database.conn.QueryRow(`
		SELECT COUNT(*) FROM sqlite_master
		WHERE type='index' AND tbl_name='messages'
	`).Scan(&indexCount)
	if err != nil {
		t.Fatalf("Failed to count message indexes: %v", err)
	}

	// Should have indexes on: uuid, session_id, parent_uuid, timestamp
	if indexCount < 4 {
		t.Errorf("Expected at least 4 indexes on messages, got %d", indexCount)
	}
}

func TestBasicInsert(t *testing.T) {
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

	// Test inserting a session
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

	// Test inserting a message
	_, err = database.Exec(`
		INSERT INTO messages (
			uuid, session_id, type, text_content, timestamp, sequence
		) VALUES (?, ?, ?, ?, datetime('now'), ?)
	`, "msg-123", sessionID, "user", "Hello world", 1)

	if err != nil {
		t.Fatalf("Failed to insert message: %v", err)
	}

	// Verify FTS was populated via trigger
	var ftsCount int
	err = database.conn.QueryRow("SELECT COUNT(*) FROM messages_fts").Scan(&ftsCount)
	if err != nil {
		t.Fatalf("Failed to query FTS: %v", err)
	}

	if ftsCount != 1 {
		t.Errorf("Expected 1 FTS entry, got %d", ftsCount)
	}
}

func TestForeignKeyConstraint(t *testing.T) {
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

	// Try to insert a message with invalid session_id
	_, err = database.Exec(`
		INSERT INTO messages (
			uuid, session_id, type, text_content, timestamp, sequence
		) VALUES (?, ?, ?, ?, datetime('now'), ?)
	`, "msg-123", 99999, "user", "Hello", 1)

	if err == nil {
		t.Error("Expected foreign key constraint error, got nil")
	}
}

func TestCascadeDelete(t *testing.T) {
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
	`, "test-session-123", "/test/project", "Test session")

	if err != nil {
		t.Fatalf("Failed to insert session: %v", err)
	}

	sessionID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("Failed to get session ID: %v", err)
	}

	// Insert message
	_, err = database.Exec(`
		INSERT INTO messages (
			uuid, session_id, type, text_content, timestamp, sequence
		) VALUES (?, ?, ?, ?, datetime('now'), ?)
	`, "msg-123", sessionID, "user", "Hello world", 1)

	if err != nil {
		t.Fatalf("Failed to insert message: %v", err)
	}

	// Delete session
	_, err = database.Exec("DELETE FROM sessions WHERE id = ?", sessionID)
	if err != nil {
		t.Fatalf("Failed to delete session: %v", err)
	}

	// Verify messages were cascade deleted
	var msgCount int
	err = database.conn.QueryRow("SELECT COUNT(*) FROM messages WHERE session_id = ?", sessionID).Scan(&msgCount)
	if err != nil {
		t.Fatalf("Failed to count messages: %v", err)
	}

	if msgCount != 0 {
		t.Errorf("Expected 0 messages after cascade delete, got %d", msgCount)
	}

	// Verify FTS entries were also deleted
	var ftsCount int
	err = database.conn.QueryRow("SELECT COUNT(*) FROM messages_fts").Scan(&ftsCount)
	if err != nil {
		t.Fatalf("Failed to count FTS entries: %v", err)
	}

	if ftsCount != 0 {
		t.Errorf("Expected 0 FTS entries after cascade delete, got %d", ftsCount)
	}
}
