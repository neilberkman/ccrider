package importer

import (
	"crypto/sha256"
	"database/sql"
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
func (i *Importer) ImportSession(session *ccsessions.ParsedSession, existingMessageCount int, fileInode, fileDevice uint64) error {
	// Compute file hash (this is the most expensive operation, done last in ImportDirectory checks)
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
			file_size, file_mtime, file_inode, file_device
		) VALUES (?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET
			project_path = excluded.project_path,
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
			END,
			file_inode = CASE
				WHEN excluded.file_mtime > sessions.file_mtime THEN excluded.file_inode
				WHEN sessions.file_inode IS NULL THEN excluded.file_inode
				ELSE sessions.file_inode
			END,
			file_device = CASE
				WHEN excluded.file_mtime > sessions.file_mtime THEN excluded.file_device
				WHEN sessions.file_device IS NULL THEN excluded.file_device
				ELSE sessions.file_device
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
			// Skip subagent files if requested
			if skipSubagents {
				// Skip files in subagents/ directories
				if strings.Contains(path, "/subagents/") {
					return nil
				}
				// Skip files named agent-*
				basename := filepath.Base(path)
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

	// Pre-load all session metadata for fast lookups (avoids N queries)
	sessionMetadata := make(map[string]struct {
		mtime        time.Time
		size         int64
		inode        uint64
		device       uint64
		messageCount int
		hash         string
	})

	rows, err := i.db.Query(`
		SELECT session_id, file_mtime, file_size, file_inode, file_device, COALESCE(message_count, 0), COALESCE(file_hash, '')
		FROM sessions
	`)
	if err != nil {
		return 0, fmt.Errorf("failed to load session metadata: %w", err)
	}

	for rows.Next() {
		var sid string
		var mtimeStr string
		var size sql.NullInt64
		var inode sql.NullInt64
		var device sql.NullInt64
		var msgCount int

		var hash string
		if err := rows.Scan(&sid, &mtimeStr, &size, &inode, &device, &msgCount, &hash); err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("failed to scan session metadata: %w", err)
		}

		// Try multiple time formats (DB may have different formats from different sync runs)
		var mtime time.Time
		var err error

		// Try ISO 8601 format first (2026-01-24T15:07:52.813484196-08:00)
		mtime, err = time.Parse(time.RFC3339Nano, mtimeStr)
		if err != nil {
			// Try Go time.String() format (2026-01-21 23:38:52.285893225 -0800 PST)
			mtime, err = time.Parse("2006-01-02 15:04:05.999999999 -0700 MST", mtimeStr)
			if err != nil {
				// Both formats failed - this shouldn't happen, but log and continue
				fmt.Printf("WARN: Failed to parse mtime for %s: %v (raw: %s)\n", sid, err, mtimeStr)
			}
		}

		sessionMetadata[sid] = struct {
			mtime        time.Time
			size         int64
			inode        uint64
			device       uint64
			messageCount int
			hash         string
		}{
			mtime:        mtime,
			size:         size.Int64,
			inode:        uint64(inode.Int64),
			device:       uint64(device.Int64),
			messageCount: msgCount,
			hash:         hash,
		}
	}
	_ = rows.Close()

	// Import each file with detailed counters
	var (
		skipped           = 0
		failed            = 0
		skippedMtimeSize  = 0
		skippedHash       = 0
		parsed            = 0
		hashed            = 0
		imported          = 0
	)

	for _, file := range files {
		// Get file info for multi-level change detection
		fileInfo, err := os.Stat(file)
		if err != nil {
			// Silently skip files that were deleted between Walk and Stat (race condition)
			if os.IsNotExist(err) {
				skipped++
				continue
			}
			// Real error - log it
			fmt.Printf("WARN: Cannot stat file %s: %v\n", file, err)
			failed++
			continue
		}
		fileMtime := fileInfo.ModTime()
		fileSize := fileInfo.Size()

		// Get platform-specific file identity (inode/device on Unix)
		fileInode, fileDevice, err := getFileIdentity(file)
		if err != nil {
			// Failed to get file identity - continue with mtime/size checks only
			fileInode, fileDevice = 0, 0
		}

		// Extract session ID from filename
		sessionID := filepath.Base(file)
		sessionID = strings.TrimSuffix(sessionID, ".jsonl")

		// Multi-level change detection: Check against pre-loaded metadata
		metadata, exists := sessionMetadata[sessionID]
		messageCount := 0

		if exists && !force {
			messageCount = metadata.messageCount

			// Level 1: Check mtime + size (sufficient to prove file unchanged)
			// If BOTH match (within 1 second for mtime), file content is guaranteed unchanged
			// We allow 1s tolerance because mtime precision varies across filesystems
			mtimeDiff := fileMtime.Sub(metadata.mtime)
			if mtimeDiff < 0 {
				mtimeDiff = -mtimeDiff
			}
			if !metadata.mtime.IsZero() && mtimeDiff < time.Second && fileSize == metadata.size {
				// File is unchanged - skip parsing
				// NOTE: We don't check inode because:
				// 1. Filesystems can reassign inodes (especially network/cloud storage)
				// 2. mtime + size is already a perfect cache key
				// 3. Inode-only changes without mtime change are impossible (OS updates mtime on write)
				skipped++
				skippedMtimeSize++
				continue
			}

			// Level 4: Check if file is older than DB (clock skew / manipulation)
			// This is suspicious but not necessarily wrong - continue to parse/hash
			// The hash check in ImportSession will catch actual changes
		}
		// else: new session or force mode - need to import

		// Before parsing, check hash (fast check to avoid expensive parse)
		currentHash, err := computeFileHash(file)
		hashed++
		if err != nil {
			fmt.Printf("WARN: Cannot hash file %s: %v\n", file, err)
			failed++
			continue
		}

		// If we have this session and hash matches, skip (file unchanged despite mtime/size/inode differences)
		if exists {
			var dbHash string
			err := i.db.QueryRow(`SELECT file_hash FROM sessions WHERE session_id = ?`, sessionID).Scan(&dbHash)
			if err == nil && dbHash == currentHash {
				// Hash matches - file is actually unchanged
				skipped++
				skippedHash++
				continue
			}
		}

		// Hash is different or new file - need to parse and import
		session, err := ccsessions.ParseFile(file)
		parsed++
		if err != nil {
			fmt.Printf("WARN: Cannot parse file %s: %v\n", file, err)
			failed++
			continue
		}

		if err := i.ImportSession(session, messageCount, fileInode, fileDevice); err != nil {
			fmt.Printf("WARN: Cannot import session %s: %v\n", sessionID, err)
			failed++
			continue
		}
		imported++

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

	// Report summary only if there were failures
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
