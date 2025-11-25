package importer

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/neilberkman/ccrider/internal/core/db"
	"github.com/neilberkman/ccrider/pkg/ccsessions"
)

// Importer handles importing sessions into the database
type Importer struct {
	db *db.DB
}

// New creates a new importer
func New(database *db.DB) *Importer {
	return &Importer{db: database}
}

// ImportSession imports a single parsed session, optionally skipping already-imported messages
// existingMessageCount: number of messages we already have for this session (0 for new sessions)
func (i *Importer) ImportSession(session *ccsessions.ParsedSession, existingMessageCount int) error {
	// Compute file hash
	hash, err := computeFileHash(session.FilePath)
	if err != nil {
		return fmt.Errorf("failed to hash file: %w", err)
	}

	// Begin transaction
	tx, err := i.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	// Extract project path from FIRST message CWD (where session was initiated)
	// This is the directory where `claude` was launched, NOT where user was last working
	projectPath := extractProjectInitiationPath(session.Messages)
	if projectPath == "" {
		// Fallback to decoding from directory name (legacy behavior)
		projectPath = extractProjectPath(session.FilePath)
	}

	// Extract last CWD (where user was last working) for resume prompt
	lastCwd := extractLastCwd(session.Messages)

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
	// NOTE: We set message_count to 0 initially, will update with actual count after filtering messages
	_, err = tx.Exec(`
		INSERT INTO sessions (
			session_id, project_path, summary, leaf_uuid, cwd,
			created_at, updated_at, message_count, file_hash,
			file_size, file_mtime
		) VALUES (?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET
			summary = CASE
				WHEN excluded.file_mtime > sessions.file_mtime THEN excluded.summary
				ELSE sessions.summary
			END,
			leaf_uuid = CASE
				WHEN excluded.file_mtime > sessions.file_mtime THEN excluded.leaf_uuid
				ELSE sessions.leaf_uuid
			END,
			cwd = CASE
				WHEN excluded.file_mtime > sessions.file_mtime THEN excluded.cwd
				ELSE sessions.cwd
			END,
			updated_at = CASE
				WHEN excluded.file_mtime > sessions.file_mtime THEN excluded.updated_at
				ELSE sessions.updated_at
			END,
			-- message_count is updated after processing messages, not from parsed file
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
		lastCwd,
		createdAt,
		updatedAt,
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
	// Skip messages we already have based on existingMessageCount
	messagesInserted := 0
	processedCount := 0
	actualMessageCount := 0 // Track actual messages we'll insert (after all filtering)

	for _, msg := range session.Messages {
		// Skip messages with no text content (tool_use/tool_result only)
		trimmed := strings.TrimSpace(msg.TextContent)
		if trimmed == "" {
			continue
		}

		// Count messages we're processing (after filtering empty ones)
		processedCount++

		// If we already have this message, skip inserting it
		if processedCount <= existingMessageCount {
			// We already have this message in DB - skip INSERT
			actualMessageCount++
			continue
		}

		// This is a new message we don't have yet - insert it

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
		actualMessageCount++ // Count every message we process (whether inserted or already existed)
	}

	// Update the session's message_count with the ACTUAL count (not the bogus parsed value)
	_, err = tx.Exec(`UPDATE sessions SET message_count = ? WHERE id = ?`, actualMessageCount, sessionDBID)
	if err != nil {
		return fmt.Errorf("failed to update message count: %w", err)
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
func (i *Importer) ImportDirectory(dirPath string, progress ProgressCallback) error {
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
	skipped := 0
	for _, file := range files {
		// Get file info for mtime check
		fileInfo, err := os.Stat(file)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to stat %s: %v\n", file, err)
			continue
		}
		fileMtime := fileInfo.ModTime()

		// Extract session ID from filename
		sessionID := filepath.Base(file)
		sessionID = strings.TrimSuffix(sessionID, ".jsonl")

		// Check if we have this session and if our copy is up-to-date
		var dbMtime sql.NullTime
		var messageCount int
		err = i.db.QueryRow(`
			SELECT file_mtime, COALESCE(message_count, 0)
			FROM sessions
			WHERE session_id = ?
		`, sessionID).Scan(&dbMtime, &messageCount)

		if err == nil && dbMtime.Valid {
			// We have this session - check if file changed
			if !fileMtime.After(dbMtime.Time) {
				// File hasn't been modified since we last imported - skip
				skipped++
				continue
			}
		}
		// else: new session or no mtime, need to import

		// Parse and import (passing existing message count for incremental import)
		session, err := ccsessions.ParseFile(file)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to parse %s: %v\n", file, err)
			continue
		}

		if err := i.ImportSession(session, messageCount); err != nil {
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

	// Note: Don't print "Skipped X files" - that's an interface concern (core should be silent)

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

// extractProjectInitiationPath finds the FIRST non-empty CWD from messages
// This is where `claude` was launched and where the session file is stored
func extractProjectInitiationPath(messages []ccsessions.ParsedMessage) string {
	// Iterate forwards to find first CWD (where session initiated)
	for i := 0; i < len(messages); i++ {
		if messages[i].CWD != "" {
			return messages[i].CWD
		}
	}
	return ""
}

// extractLastCwd finds the LAST non-empty CWD from messages
// This is where the user was last working (for resume prompt)
func extractLastCwd(messages []ccsessions.ParsedMessage) string {
	// Iterate backwards to find most recent CWD
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].CWD != "" {
			return messages[i].CWD
		}
	}
	return ""
}

func extractProjectPath(filePath string) string {
	// LEGACY: Extract from ~/.claude/projects/-Users-neil-xuku-invoice/session.jsonl
	// This is buggy for paths with dashes/underscores, use extractProjectPathFromMessages instead
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
