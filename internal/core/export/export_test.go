package export

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/neilberkman/ccrider/internal/core/db"
)

func setupTestDB(t *testing.T) *db.DB {
	t.Helper()
	tmpfile, err := os.CreateTemp("", "test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(tmpfile.Name()) })

	database, err := db.New(tmpfile.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })

	// Insert a test session with messages
	_, err = database.Exec(`
		INSERT INTO sessions (session_id, summary, project_path, cwd, created_at, updated_at)
		VALUES ('test-session-1234', 'Fix auth bug', '/home/user/myrepo', '/home/user/myrepo', '2026-01-15 10:00:00', '2026-01-15 11:00:00')
	`)
	if err != nil {
		t.Fatal(err)
	}

	_, err = database.Exec(`
		INSERT INTO messages (session_id, uuid, type, sender, text_content, timestamp, sequence)
		VALUES
			(1, 'msg-uuid-1', 'user', '', 'How do I fix the auth bug?', '2026-01-15 10:00:00', 1),
			(1, 'msg-uuid-2', 'assistant', '', 'Let me look at the code.', '2026-01-15 10:01:00', 2),
			(1, 'msg-uuid-3', 'summary', '', 'Session summary', '2026-01-15 10:02:00', 3)
	`)
	if err != nil {
		t.Fatal(err)
	}

	return database
}

func TestGenerateMarkdown(t *testing.T) {
	database := setupTestDB(t)

	content, err := GenerateMarkdown(database, "test-session-1234")
	if err != nil {
		t.Fatalf("GenerateMarkdown() error: %v", err)
	}

	// Check header
	if !contains(content, "# Fix auth bug") {
		t.Error("missing session summary header")
	}

	// Check metadata
	if !contains(content, "**Session ID:** `test-session-1234`") {
		t.Error("missing session ID")
	}
	if !contains(content, "**Project:** `/home/user/myrepo`") {
		t.Error("missing project path")
	}

	// Check messages are included
	if !contains(content, "How do I fix the auth bug?") {
		t.Error("missing user message content")
	}
	if !contains(content, "Let me look at the code.") {
		t.Error("missing assistant message content")
	}

	// Check summary entries are excluded
	if contains(content, "Session summary") {
		t.Error("summary message should be excluded from export")
	}

	// Check message labels
	if !contains(content, "**USER**") {
		t.Error("missing USER label")
	}
	if !contains(content, "**ASSISTANT**") {
		t.Error("missing ASSISTANT label")
	}
}

func TestGenerateMarkdown_NotFound(t *testing.T) {
	database := setupTestDB(t)

	_, err := GenerateMarkdown(database, "nonexistent-session")
	if err == nil {
		t.Fatal("expected error for nonexistent session")
	}
}

func TestDefaultFilename(t *testing.T) {
	tests := []struct {
		sessionID string
		want      string
	}{
		{"abcdef1234567890", "session-abcdef12.md"},
		{"short", "session-short.md"},
		{"12345678", "session-12345678.md"},
		{"123456789", "session-12345678.md"},
	}

	for _, tt := range tests {
		got := DefaultFilename(tt.sessionID)
		if got != tt.want {
			t.Errorf("DefaultFilename(%q) = %q, want %q", tt.sessionID, got, tt.want)
		}
	}
}

func TestRepoExportPath(t *testing.T) {
	got := RepoExportPath("abcdef1234567890", "/home/user/myrepo")
	want := filepath.Join("/home/user/myrepo", ".ccrider", "exports", "session-abcdef12.md")
	if got != want {
		t.Errorf("RepoExportPath() = %q, want %q", got, want)
	}
}

func TestRepoExportPath_EmptyProject(t *testing.T) {
	got := RepoExportPath("abcdef1234567890", "")
	if got != "" {
		t.Errorf("RepoExportPath() with empty project = %q, want empty", got)
	}
}

func TestWriteExport(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "subdir", "test-export.md")

	err := WriteExport("# Test Content", filePath, false)
	if err != nil {
		t.Fatalf("WriteExport() error: %v", err)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("failed to read written file: %v", err)
	}

	if string(data) != "# Test Content" {
		t.Errorf("file content = %q, want %q", string(data), "# Test Content")
	}
}

func TestWriteExport_NoOverwrite(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "existing.md")

	// Create existing file
	os.WriteFile(filePath, []byte("original"), 0644)

	err := WriteExport("new content", filePath, false)
	if err == nil {
		t.Fatal("expected error when file exists and force=false")
	}

	// Original content should be preserved
	data, _ := os.ReadFile(filePath)
	if string(data) != "original" {
		t.Error("original file content was overwritten")
	}
}

func TestWriteExport_ForceOverwrite(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "existing.md")

	// Create existing file
	os.WriteFile(filePath, []byte("original"), 0644)

	err := WriteExport("new content", filePath, true)
	if err != nil {
		t.Fatalf("WriteExport() with force error: %v", err)
	}

	data, _ := os.ReadFile(filePath)
	if string(data) != "new content" {
		t.Errorf("file content = %q, want %q", string(data), "new content")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
