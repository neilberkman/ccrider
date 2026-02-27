package importer

import (
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/neilberkman/ccrider/internal/core/db"
	"github.com/neilberkman/ccrider/pkg/ccsessions"
	"github.com/zeebo/blake3"
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
// fileHash: pre-computed blake3 hash from ImportDirectory (avoids double-hashing)
func (i *Importer) ImportSession(session *ccsessions.ParsedSession, existingMessageCount int, fileInode, fileDevice uint64, fileHash string) error {
	hash := fileHash

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

	// Upsert session — hash-only import means we always have current file data
	_, err = tx.Exec(`
		INSERT INTO sessions (
			session_id, project_path, summary, leaf_uuid, cwd,
			created_at, updated_at, message_count, file_hash,
			file_size, file_mtime, file_inode, file_device
		) VALUES (?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET
			project_path = excluded.project_path,
			summary = excluded.summary,
			leaf_uuid = excluded.leaf_uuid,
			cwd = excluded.cwd,
			updated_at = excluded.updated_at,
			file_hash = excluded.file_hash,
			file_size = excluded.file_size,
			file_mtime = excluded.file_mtime,
			file_inode = excluded.file_inode,
			file_device = excluded.file_device
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
		fileInode,
		fileDevice,
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
// If force is true, re-imports all sessions regardless of mtime
// If skipSubagents is true, skips files in subagents/ directories and agent-* files
// Returns the number of skipped files and an error
func (i *Importer) ImportDirectory(dirPath string, progress ProgressCallback, force bool, skipSubagents bool) (int, error) {
	// Find all .jsonl files (optionally skipping subagents)
	var files []string
	err := filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && filepath.Ext(path) == ".jsonl" {
			basename := filepath.Base(path)
			// Skip cloud sync conflict files
			if strings.Contains(basename, "Edit conflict") {
				return nil
			}
			if skipSubagents {
				if strings.Contains(path, "/subagents/") {
					return nil
				}
				if strings.HasPrefix(basename, "agent-") {
					return nil
				}
			}
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("failed to walk directory: %w", err)
	}

	// Pre-load session hashes + message counts for fast lookups
	type sessionMeta struct {
		messageCount int
		hash         string
	}
	sessionMetadata := make(map[string]sessionMeta)

	rows, err := i.db.Query(`
		SELECT session_id, COALESCE(message_count, 0), COALESCE(file_hash, '')
		FROM sessions
	`)
	if err != nil {
		return 0, fmt.Errorf("failed to load session metadata: %w", err)
	}

	for rows.Next() {
		var sid string
		var meta sessionMeta
		if err := rows.Scan(&sid, &meta.messageCount, &meta.hash); err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("failed to scan session metadata: %w", err)
		}
		sessionMetadata[sid] = meta
	}
	_ = rows.Close()

	var skipped, failed, imported int

	for _, file := range files {
		sessionID := filepath.Base(file)
		sessionID = strings.TrimSuffix(sessionID, ".jsonl")

		// blake3 hash is the sole freshness check
		currentHash, err := computeFileHash(file)
		if err != nil {
			if os.IsNotExist(err) {
				skipped++
				continue
			}
			fmt.Printf("WARN: Cannot hash file %s: %v\n", file, err)
			failed++
			continue
		}

		metadata, exists := sessionMetadata[sessionID]
		messageCount := 0

		if exists && !force && metadata.hash == currentHash {
			skipped++
			continue
		}
		if exists {
			messageCount = metadata.messageCount
		}

		// Hash differs or new file — parse and import
		session, err := ccsessions.ParseFile(file)
		if err != nil {
			fmt.Printf("WARN: Cannot parse file %s: %v\n", file, err)
			failed++
			continue
		}

		fileInode, fileDevice, _ := getFileIdentity(file)

		if err := i.ImportSession(session, messageCount, fileInode, fileDevice, currentHash); err != nil {
			fmt.Printf("WARN: Cannot import session %s: %v\n", sessionID, err)
			failed++
			continue
		}
		imported++

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

	if failed > 0 {
		fmt.Printf("\nImport completed with %d failures (see warnings above)\n", failed)
	}

	return skipped, nil
}

func computeFileHash(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = file.Close()
	}()

	h := blake3.New()
	if _, err := io.Copy(h, file); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
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

// getFileIdentity extracts platform-specific file identity info (inode, device)
// Returns (inode, device, error). On platforms without inode support, returns (0, 0, nil)
func getFileIdentity(path string) (uint64, uint64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, 0, err
	}

	// Extract platform-specific file identity
	if runtime.GOOS == "windows" {
		// Windows doesn't have inodes in the Unix sense
		// Could use fileIndex from GetFileInformationByHandle, but requires unsafe
		// For now, return 0,0 and rely on mtime/size/hash checks
		return 0, 0, nil
	}

	// Unix-like systems (Linux, macOS, BSD)
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		// Shouldn't happen on Unix, but handle gracefully
		return 0, 0, nil
	}

	return stat.Ino, uint64(stat.Dev), nil
}
