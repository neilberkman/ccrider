package importer

import (
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
	"github.com/zeebo/blake3"
)

// ParseFunc parses a session JSONL file and returns a ParsedSession.
// Different providers (Claude, Codex) supply different implementations.
type ParseFunc func(string) (*ccsessions.ParsedSession, error)

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
// provider: identifies the agent (e.g. "claude", "codex")
func (i *Importer) ImportSession(session *ccsessions.ParsedSession, existingMessageCount int, fileInode, fileDevice uint64, fileHash string, provider string) error {
	return i.importSession(session, existingMessageCount, fileInode, fileDevice, fileHash, provider, false)
}

// importSession writes one parsed session. replaceMessages atomically replaces
// the stored transcript for authoritative remote snapshots, removing messages
// that disappeared upstream and their FTS entries.
func (i *Importer) importSession(session *ccsessions.ParsedSession, existingMessageCount int, fileInode, fileDevice uint64, fileHash string, provider string, replaceMessages bool) error {
	hash := fileHash

	fileSessionID := sessionImportID(session)

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
	projectPath := session.ProjectPath
	if projectPath == "" {
		projectPath = extractProjectInitiationPath(session.Messages)
	}
	if projectPath == "" {
		// Fallback to decoding from directory name for file-based providers.
		// Remote sources use synthetic paths and must supply a real local path.
		if !replaceMessages {
			projectPath = extractProjectPath(session.FilePath)
		}
	}

	// Extract last CWD (where user was last working) for resume prompt
	lastCwd := extractLastCwd(session.Messages)

	// Compute timestamps from messages
	// Use first message for createdAt, max timestamp across ALL messages for updatedAt
	// (don't assume messages are chronologically ordered — some may have zero timestamps)
	// Normalize to UTC so the DB is consistent regardless of local timezone
	var createdAt, updatedAt time.Time
	for _, msg := range session.Messages {
		// Only real conversation turns count toward created/updated time.
		// Non-message events (pr-link, file-history-snapshot, queue-operation,
		// summary, system) carry timestamps but aren't "activity" — counting
		// them floats stale sessions to the top of last-active sorts (e.g. a
		// PR-link annotation stamped days after the last actual message).
		if msg.Type != "user" && msg.Type != "assistant" {
			continue
		}
		if msg.Timestamp.IsZero() {
			continue
		}
		if createdAt.IsZero() {
			createdAt = msg.Timestamp
		}
		if msg.Timestamp.After(updatedAt) {
			updatedAt = msg.Timestamp
		}
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
			file_size, file_mtime, file_inode, file_device, provider
		) VALUES (?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?, ?, ?)
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
			file_device = excluded.file_device,
			provider = excluded.provider
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
		provider,
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
	if replaceMessages {
		// Remote revisions are authoritative transcript snapshots. Invalidate all
		// transcript-derived data in the same transaction so a failed replacement
		// restores both the old messages and their caches.
		for _, table := range []string{"messages", "session_summaries", "summary_chunks", "session_issues", "session_files"} {
			if _, err := tx.Exec(`DELETE FROM `+table+` WHERE session_id = ?`, sessionDBID); err != nil {
				return fmt.Errorf("failed to invalidate %s for replaced session: %w", table, err)
			}
		}
		if _, err := tx.Exec(`UPDATE sessions SET llm_summary = NULL, llm_summary_at = NULL WHERE id = ?`, sessionDBID); err != nil {
			return fmt.Errorf("failed to invalidate legacy summary for replaced session: %w", err)
		}
		existingMessageCount = 0
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
			ON CONFLICT(uuid) DO UPDATE SET
				session_id = excluded.session_id,
				parent_uuid = excluded.parent_uuid,
				type = excluded.type,
				sender = excluded.sender,
				content = excluded.content,
				text_content = excluded.text_content,
				timestamp = excluded.timestamp,
				sequence = excluded.sequence,
				is_sidechain = excluded.is_sidechain,
				cwd = excluded.cwd,
				git_branch = excluded.git_branch,
				version = excluded.version
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
// parseFn controls how each JSONL file is parsed (e.g. ccsessions.ParseFile or codexsessions.ParseFile)
// provider identifies the agent for DB storage (e.g. "claude", "codex")
// Returns the number of skipped files and an error
func (i *Importer) ImportDirectory(dirPath string, progress ProgressCallback, force bool, skipSubagents bool, parseFn ParseFunc, provider string) (int, error) {
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

	// Files that need no import still count as progress — a routine sync skips
	// nearly everything, so a bar fed only by imports looks frozen.
	advance := func() {
		if progress != nil {
			progress.Skip()
		}
	}

	for _, file := range files {
		sessionID := filepath.Base(file)
		sessionID = strings.TrimSuffix(sessionID, ".jsonl")

		metadata, exists := sessionMetadata[sessionID]
		messageCount := 0
		var statInfo os.FileInfo

		if exists && !force {
			messageCount = metadata.messageCount

			// Fast path: mtime + size check (works on local disk, may miss on cloud drives)
			fileInfo, err := os.Stat(file)
			if err != nil {
				if os.IsNotExist(err) {
					skipped++
					advance()
					continue
				}
				fmt.Fprintf(os.Stderr, "WARN: Cannot stat file %s: %v\n", file, err)
				failed++
				advance()
				continue
			}
			statInfo = fileInfo

			mtimeDiff := fileInfo.ModTime().Sub(metadata.mtime)
			if mtimeDiff < 0 {
				mtimeDiff = -mtimeDiff
			}
			if !metadata.mtime.IsZero() && mtimeDiff < time.Second && fileInfo.Size() == metadata.size {
				skipped++
				advance()
				continue
			}
		}

		// BLAKE3 hash check — catches unchanged files even when mtime drifts (cloud drives)
		currentHash, err := computeFileHash(file)
		if err != nil {
			if os.IsNotExist(err) {
				skipped++
				advance()
				continue
			}
			fmt.Fprintf(os.Stderr, "WARN: Cannot hash file %s: %v\n", file, err)
			failed++
			advance()
			continue
		}

		if exists && !force && metadata.hash == currentHash {
			// Content is unchanged, so only the recorded stat drifted (a rewrite
			// that preserved bytes, a restore, a cloud-drive resync). Record the
			// current stat so the cheap fast path re-arms — otherwise this file is
			// re-read in full on every sync forever, which is what turns a routine
			// sync of a few large transcripts into a multi-second read storm.
			if statInfo != nil {
				if _, err := i.db.Exec(
					`UPDATE sessions SET file_mtime = ?, file_size = ? WHERE session_id = ?`,
					statInfo.ModTime(), statInfo.Size(), sessionID,
				); err != nil {
					fmt.Fprintf(os.Stderr, "WARN: Cannot refresh file metadata for %s: %v\n", file, err)
				}
			}
			skipped++
			advance()
			continue
		}

		session, err := parseFn(file)
		if err != nil {
			fmt.Fprintf(os.Stderr, "WARN: Cannot parse file %s: %v\n", file, err)
			failed++
			advance()
			continue
		}

		fileInode, fileDevice, _ := getFileIdentity(file)

		if err := i.ImportSession(session, messageCount, fileInode, fileDevice, currentHash, provider); err != nil {
			fmt.Fprintf(os.Stderr, "WARN: Cannot import session %s: %v\n", sessionID, err)
			failed++
			advance()
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

// ImportEnumerated imports a slice of already-parsed sessions from a
// database-backed provider (e.g. Copilot) that has no per-session files to walk.
//
// Incremental sync uses a synthetic content hash (derived from the session's
// last-updated time and message count) in place of a file hash: unchanged
// sessions are skipped. Returns the number of skipped sessions.
func (i *Importer) ImportEnumerated(sessions []*ccsessions.ParsedSession, progress ProgressCallback, force bool, provider string) (int, error) {
	// Pre-load existing session hashes for fast skip checks.
	existingHash := make(map[string]string)

	rows, err := i.db.Query(`SELECT session_id, COALESCE(file_hash, '') FROM sessions`)
	if err != nil {
		return 0, fmt.Errorf("failed to load session metadata: %w", err)
	}
	for rows.Next() {
		var sid, hash string
		if err := rows.Scan(&sid, &hash); err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("failed to scan session metadata: %w", err)
		}
		existingHash[sid] = hash
	}
	_ = rows.Close()

	var skipped, failed int

	for _, session := range sessions {
		sessionID := sessionImportID(session)

		hash := enumeratedSessionHash(sessionID, session)

		if !force && existingHash[sessionID] == hash {
			skipped++
			if progress != nil {
				progress.Skip()
			}
			continue
		}

		// Pass existingMessageCount=0: messages carry stable, content-addressed
		// UUIDs, so ImportSession's ON CONFLICT(uuid) dedups already-imported
		// messages while still inserting real new ones without relying on
		// append-only ordering (which breaks when an earlier turn is backfilled).
		if err := i.ImportSession(session, 0, 0, 0, hash, provider); err != nil {
			fmt.Fprintf(os.Stderr, "WARN: Cannot import session %s: %v\n", sessionID, err)
			failed++
			if progress != nil {
				progress.Skip()
			}
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

// ImportRemote imports remote sessions using a cheap reference list and an
// on-demand fetch function. The opaque reference revision is stored in the
// existing file_hash column so unchanged remote sessions never need a full
// export on subsequent syncs.
func (i *Importer) ImportRemote(refs []RemoteSessionRef, fetch func(RemoteSessionRef) (*ccsessions.ParsedSession, error), progress ProgressCallback, force bool, provider string) (int, error) {
	existingHash := make(map[string]string)
	rows, err := i.db.Query(`SELECT session_id, COALESCE(file_hash, '') FROM sessions`)
	if err != nil {
		return 0, fmt.Errorf("failed to load remote session metadata: %w", err)
	}
	for rows.Next() {
		var sessionID, hash string
		if err := rows.Scan(&sessionID, &hash); err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("failed to scan remote session metadata: %w", err)
		}
		existingHash[sessionID] = hash
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, fmt.Errorf("failed to read remote session metadata: %w", err)
	}
	_ = rows.Close()

	var skipped, failed int
	for _, ref := range refs {
		ref.ImportID = strings.TrimSpace(ref.ImportID)
		ref.Revision = strings.TrimSpace(ref.Revision)
		if ref.ImportID == "" || ref.Revision == "" {
			fmt.Fprintf(os.Stderr, "WARN: Cannot import remote %s session: missing id or revision\n", provider)
			failed++
			continue
		}
		if !force && existingHash[ref.ImportID] == ref.Revision {
			skipped++
			continue
		}

		session, err := fetch(ref)
		if err != nil {
			fmt.Fprintf(os.Stderr, "WARN: Cannot fetch %s session %s: %v\n", provider, ref.ImportID, err)
			failed++
			continue
		}
		if session == nil {
			fmt.Fprintf(os.Stderr, "WARN: Cannot fetch %s session %s: empty session\n", provider, ref.ImportID)
			failed++
			continue
		}

		if exportedID := strings.TrimSpace(session.SessionID); exportedID != "" && exportedID != ref.ImportID {
			fmt.Fprintf(os.Stderr, "WARN: Cannot import remote %s session %s: export contained session id %s\n", provider, ref.ImportID, exportedID)
			failed++
			continue
		}
		// The listed id is the stable source of truth for the database key.
		session.ImportID = ref.ImportID
		if session.SessionID == "" {
			session.SessionID = ref.ImportID
		}
		if err := i.importSession(session, 0, 0, 0, ref.Revision, provider, true); err != nil {
			fmt.Fprintf(os.Stderr, "WARN: Cannot import remote %s session %s: %v\n", provider, ref.ImportID, err)
			failed++
			continue
		}
		existingHash[ref.ImportID] = ref.Revision

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
		fmt.Fprintf(os.Stderr, "\nRemote import completed with %d failures (see warnings above)\n", failed)
	}
	return skipped, nil
}

// sessionImportID returns the stable database key for a parsed session.
// File-based providers retain the historical filename key so resumed-session
// chains do not collapse; providers with a shared log filename supply ImportID.
func sessionImportID(session *ccsessions.ParsedSession) string {
	if id := strings.TrimSpace(session.ImportID); id != "" {
		return id
	}
	fileSessionID := filepath.Base(session.FilePath)
	return strings.TrimSuffix(fileSessionID, filepath.Ext(fileSessionID))
}

// enumeratedSessionHash produces a change-detection hash for an enumerated
// session from its message content rather than the file mtime. Hashing each
// message's UUID and full text detects any add, remove, reorder, or in-place
// edit (including a same-length rewrite), so it doesn't depend on the source
// bumping a timestamp and won't churn if an unrelated mtime changes.
func enumeratedSessionHash(sessionID string, session *ccsessions.ParsedSession) string {
	h := blake3.New()
	_, _ = fmt.Fprintf(h, "%s|%d", sessionID, len(session.Messages))
	for _, m := range session.Messages {
		// Length-frame each field so message/text boundaries are unambiguous.
		_, _ = fmt.Fprintf(h, "|%s:%d:", m.UUID, len(m.TextContent))
		_, _ = io.WriteString(h, m.TextContent)
	}
	return hex.EncodeToString(h.Sum(nil))
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
