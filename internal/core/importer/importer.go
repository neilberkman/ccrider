package importer

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yourusername/ccrider/internal/core/db"
	"github.com/yourusername/ccrider/pkg/ccsessions"
)

// Importer handles importing sessions into the database
type Importer struct {
	db *db.DB
}

// New creates a new importer
func New(database *db.DB) *Importer {
	return &Importer{db: database}
}

// ImportSession imports a single parsed session
func (i *Importer) ImportSession(session *ccsessions.ParsedSession) error {
	// Compute file hash
	hash, err := computeFileHash(session.FilePath)
	if err != nil {
		return fmt.Errorf("failed to hash file: %w", err)
	}

	// Check if already imported
	var exists bool
	err = i.db.QueryRow("SELECT EXISTS(SELECT 1 FROM import_log WHERE file_hash = ?)", hash).Scan(&exists)
	if err != nil {
		return fmt.Errorf("failed to check import log: %w", err)
	}

	if exists {
		// File already imported - skip for now
		return nil
	}

	// Begin transaction
	tx, err := i.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	// Extract project path from file path
	projectPath := extractProjectPath(session.FilePath)

	// Compute timestamps from messages
	var createdAt, updatedAt time.Time
	if len(session.Messages) > 0 {
		createdAt = session.Messages[0].Timestamp
		updatedAt = session.Messages[len(session.Messages)-1].Timestamp
	}
	if createdAt.IsZero() {
		createdAt = session.FileMtime
	}
	if updatedAt.IsZero() {
		updatedAt = session.FileMtime
	}

	// Upsert session - update if this file is newer than existing
	_, err = tx.Exec(`
		INSERT INTO sessions (
			session_id, project_path, summary, leaf_uuid,
			created_at, updated_at, message_count, file_hash,
			file_size, file_mtime
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET
			summary = CASE
				WHEN excluded.file_mtime > sessions.file_mtime THEN excluded.summary
				ELSE sessions.summary
			END,
			leaf_uuid = CASE
				WHEN excluded.file_mtime > sessions.file_mtime THEN excluded.leaf_uuid
				ELSE sessions.leaf_uuid
			END,
			updated_at = CASE
				WHEN excluded.file_mtime > sessions.file_mtime THEN excluded.updated_at
				ELSE sessions.updated_at
			END,
			message_count = CASE
				WHEN excluded.file_mtime > sessions.file_mtime THEN excluded.message_count
				ELSE sessions.message_count
			END,
			file_hash = CASE
				WHEN excluded.file_mtime > sessions.file_mtime THEN excluded.file_hash
				ELSE sessions.file_hash
			END,
			file_size = CASE
				WHEN excluded.file_mtime > sessions.file_mtime THEN excluded.file_size
				ELSE sessions.file_size
			END,
			file_mtime = CASE
				WHEN excluded.file_mtime > sessions.file_mtime THEN excluded.file_mtime
				ELSE sessions.file_mtime
			END
	`,
		session.SessionID,
		projectPath,
		session.Summary,
		session.LeafUUID,
		createdAt,
		updatedAt,
		len(session.Messages),
		hash,
		session.FileSize,
		session.FileMtime,
	)
	if err != nil {
		return fmt.Errorf("failed to upsert session: %w", err)
	}

	// Get the session DB ID (either newly inserted or existing)
	var sessionDBID int64
	err = tx.QueryRow("SELECT id FROM sessions WHERE session_id = ?", session.SessionID).Scan(&sessionDBID)
	if err != nil {
		return fmt.Errorf("failed to get session ID: %w", err)
	}

	// Insert messages (use INSERT OR IGNORE to skip duplicates from resumed sessions)
	messagesInserted := 0
	foundSubstantiveUser := false

	for _, msg := range session.Messages {
		// Skip messages with no text content (tool_use/tool_result only)
		trimmed := strings.TrimSpace(msg.TextContent)
		if trimmed == "" {
			continue
		}

		// Skip all messages until we find the first substantive user message
		// (warmups, greetings, etc. are all noise before actual conversation)
		if !foundSubstantiveUser {
			if msg.Type == "user" && trimmed != "Warmup" && len(trimmed) > 10 {
				// Found first real user message - start including from here
				foundSubstantiveUser = true
			} else {
				// Skip everything before first substantive user message
				continue
			}
		}

		result, err := tx.Exec(`
			INSERT OR IGNORE INTO messages (
				uuid, session_id, parent_uuid, type, sender,
				content, text_content, timestamp, sequence,
				is_sidechain, cwd, git_branch, version
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`,
			msg.UUID,
			sessionDBID,
			msg.ParentUUID,
			msg.Type,
			msg.Sender,
			string(msg.Content),
			msg.TextContent,
			msg.Timestamp,
			msg.Sequence,
			msg.IsSidechain,
			msg.CWD,
			msg.GitBranch,
			msg.Version,
		)
		if err != nil {
			return fmt.Errorf("failed to insert message %s: %w", msg.UUID, err)
		}

		// Check if the message was actually inserted
		rowsAffected, err := result.RowsAffected()
		if err == nil && rowsAffected > 0 {
			messagesInserted++
		}
	}

	// Record import
	_, err = tx.Exec(`
		INSERT INTO import_log (file_path, file_hash, sessions_imported, messages_imported, status)
		VALUES (?, ?, 1, ?, 'success')
	`, session.FilePath, hash, messagesInserted)
	if err != nil {
		return fmt.Errorf("failed to record import: %w", err)
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit: %w", err)
	}

	return nil
}

// ImportDirectory imports all sessions from a directory tree
func (i *Importer) ImportDirectory(dirPath string, progress *ProgressReporter) error {
	// Find all .jsonl files
	var files []string
	err := filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && filepath.Ext(path) == ".jsonl" {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to walk directory: %w", err)
	}

	// Import each file
	for _, file := range files {
		session, err := ccsessions.ParseFile(file)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to parse %s: %v\n", file, err)
			continue
		}

		if err := i.ImportSession(session); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to import %s: %v\n", file, err)
			continue
		}

		// Update progress
		if progress != nil {
			firstMsg := ""
			if len(session.Messages) > 0 {
				firstMsg = session.Messages[0].TextContent
				if len(firstMsg) > 100 {
					firstMsg = firstMsg[:97] + "..."
				}
			}
			progress.Update(session.Summary, firstMsg)
		}
	}

	return nil
}

func computeFileHash(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = file.Close()
	}()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

func extractProjectPath(filePath string) string {
	// Extract from ~/.claude/projects/-Users-neil-xuku-invoice/session.jsonl
	// Returns /Users/neil/xuku/invoice
	dir := filepath.Dir(filePath)
	base := filepath.Base(dir)

	// Decode the project path
	if len(base) > 0 && base[0] == '-' {
		// Remove leading dash and replace remaining dashes with slashes
		decoded := base[1:]
		// Replace "-" with "/" to reconstruct the path
		decoded = strings.ReplaceAll(decoded, "-", "/")
		return "/" + decoded
	}

	return dir
}
