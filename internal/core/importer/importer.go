package importer

import (
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
// fileHash: pre-computed BLAKE3 hash from ImportDirectory (avoids double-hashing)
func (i *Importer) ImportSession(session *ccsessions.ParsedSession, existingMessageCount int, fileInode, fileDevice uint64, fileHash string) error {
	hash := fileHash

	// Use filename as DB key (not parsed sessionId which points to previous
	// file in resumed session chains, causing hash thrashing)
	fileSessionID := filepath.Base(session.FilePath)
	fileSessionID = strings.TrimSuffix(fileSessionID, filepath.Ext(fileSessionID))

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

	// Upsert session — hash-changed means file-changed, so overwrite unconditionally
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
		fileSessionID,
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
	err = tx.QueryRow("SELECT id FROM sessions WHERE session_id = ?", fileSessionID).Scan(&sessionDBID)
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
			INSERT INTO messages (
				uuid, session_id, parent_uuid, type, sender,
				content, text_content, timestamp, sequence,
				is_sidechain, cwd, git_branch, version
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(uuid) DO UPDATE SET session_id = excluded.session_id
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

	// Pre-load all session metadata for fast lookups (avoids N queries)
	type sessionMeta struct {
		mtime        time.Time
		size         int64
		messageCount int
		hash         string
	}
	sessionMetadata := make(map[string]sessionMeta)

	rows, err := i.db.Query(`
		SELECT session_id, file_mtime, file_size, COALESCE(message_count, 0), COALESCE(file_hash, '')
		FROM sessions
	`)
	if err != nil {
		return 0, fmt.Errorf("failed to load session metadata: %w", err)
	}

	for rows.Next() {
		var sid string
		var mtimeStr string
		var size sql.NullInt64
		var msgCount int
		var hash string
		if err := rows.Scan(&sid, &mtimeStr, &size, &msgCount, &hash); err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("failed to scan session metadata: %w", err)
		}

		var mtime time.Time
		mtime, err = time.Parse(time.RFC3339Nano, mtimeStr)
		if err != nil {
			mtime, _ = time.Parse("2006-01-02 15:04:05.999999999 -0700 MST", mtimeStr)
		}

		sessionMetadata[sid] = sessionMeta{
			mtime:        mtime,
			size:         size.Int64,
			messageCount: msgCount,
			hash:         hash,
		}
	}
	_ = rows.Close()

	var skipped, failed int

	for _, file := range files {
		sessionID := filepath.Base(file)
		sessionID = strings.TrimSuffix(sessionID, ".jsonl")

		metadata, exists := sessionMetadata[sessionID]
		messageCount := 0

		if exists && !force {
			messageCount = metadata.messageCount

			// Fast path: mtime + size check (works on local disk, may miss on cloud drives)
			fileInfo, err := os.Stat(file)
			if err != nil {
				if os.IsNotExist(err) {
					skipped++
					continue
				}
				fmt.Fprintf(os.Stderr, "WARN: Cannot stat file %s: %v\n", file, err)
				failed++
				continue
			}

			mtimeDiff := fileInfo.ModTime().Sub(metadata.mtime)
			if mtimeDiff < 0 {
				mtimeDiff = -mtimeDiff
			}
			if !metadata.mtime.IsZero() && mtimeDiff < time.Second && fileInfo.Size() == metadata.size {
				skipped++
				continue
			}
		}

		// BLAKE3 hash check — catches unchanged files even when mtime drifts (cloud drives)
		currentHash, err := computeFileHash(file)
		if err != nil {
			if os.IsNotExist(err) {
				skipped++
				continue
			}
			fmt.Fprintf(os.Stderr, "WARN: Cannot hash file %s: %v\n", file, err)
			failed++
			continue
		}

		if exists && !force && metadata.hash == currentHash {
			skipped++
			continue
		}

		session, err := ccsessions.ParseFile(file)
		if err != nil {
			fmt.Fprintf(os.Stderr, "WARN: Cannot parse file %s: %v\n", file, err)
			failed++
			continue
		}

		fileInode, fileDevice, _ := getFileIdentity(file)

		if err := i.ImportSession(session, messageCount, fileInode, fileDevice, currentHash); err != nil {
			fmt.Fprintf(os.Stderr, "WARN: Cannot import session %s: %v\n", sessionID, err)
			failed++
			continue
		}

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
		fmt.Fprintf(os.Stderr, "\nImport completed with %d failures (see warnings above)\n", failed)
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
