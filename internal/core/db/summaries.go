package db

import (
	"database/sql"
	"fmt"

	"github.com/neilberkman/ccrider/internal/core/models"
)

// SessionSummary represents a stored summary
type SessionSummary struct {
	ID               int64
	SessionID        int64
	FullSummary      string
	SummaryVersion   int
	LastMessageCount int
	TokensApprox     int
	CreatedAt        string
	UpdatedAt        string
}

// ChunkSummary represents a stored chunk summary
type ChunkSummary struct {
	ID           int64
	SessionID    int64
	ChunkIndex   int
	MessageRange string
	Summary      string
	TokensApprox int
	CreatedAt    string
}

// SaveSummary saves a session summary and its chunks
func (db *DB) SaveSummary(sessionID int64, fullSummary string, version, messageCount, tokensApprox int, chunks []ChunkSummary) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Insert or update main summary
	_, err = tx.Exec(`
		INSERT INTO session_summaries
		(session_id, full_summary, summary_version, last_message_count, tokens_approx, updated_at)
		VALUES (?, ?, ?, ?, ?, datetime('now'))
		ON CONFLICT(session_id) DO UPDATE SET
			full_summary = excluded.full_summary,
			summary_version = excluded.summary_version,
			last_message_count = excluded.last_message_count,
			tokens_approx = excluded.tokens_approx,
			updated_at = datetime('now')
	`, sessionID, fullSummary, version, messageCount, tokensApprox)

	if err != nil {
		return fmt.Errorf("save summary: %w", err)
	}

	// Delete old chunks for this session
	_, err = tx.Exec(`DELETE FROM summary_chunks WHERE session_id = ?`, sessionID)
	if err != nil {
		return fmt.Errorf("delete old chunks: %w", err)
	}

	// Insert new chunks
	for _, chunk := range chunks {
		_, err = tx.Exec(`
			INSERT INTO summary_chunks
			(session_id, chunk_index, message_range, summary, tokens_approx)
			VALUES (?, ?, ?, ?, ?)
		`, sessionID, chunk.ChunkIndex, chunk.MessageRange, chunk.Summary, chunk.TokensApprox)

		if err != nil {
			return fmt.Errorf("save chunk %d: %w", chunk.ChunkIndex, err)
		}
	}

	return tx.Commit()
}

// GetSummary retrieves a session summary
func (db *DB) GetSummary(sessionID int64) (*SessionSummary, error) {
	var s SessionSummary
	err := db.QueryRow(`
		SELECT id, session_id, full_summary, summary_version,
		       last_message_count, tokens_approx, created_at, updated_at
		FROM session_summaries
		WHERE session_id = ?
	`, sessionID).Scan(
		&s.ID, &s.SessionID, &s.FullSummary, &s.SummaryVersion,
		&s.LastMessageCount, &s.TokensApprox, &s.CreatedAt, &s.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil // No summary exists
	}
	if err != nil {
		return nil, fmt.Errorf("get summary: %w", err)
	}

	return &s, nil
}

// GetChunks retrieves chunk summaries for a session
func (db *DB) GetChunks(sessionID int64) ([]ChunkSummary, error) {
	rows, err := db.Query(`
		SELECT id, session_id, chunk_index, message_range, summary, tokens_approx, created_at
		FROM summary_chunks
		WHERE session_id = ?
		ORDER BY chunk_index
	`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("query chunks: %w", err)
	}
	defer rows.Close()

	var chunks []ChunkSummary
	for rows.Next() {
		var c ChunkSummary
		err := rows.Scan(&c.ID, &c.SessionID, &c.ChunkIndex, &c.MessageRange,
			&c.Summary, &c.TokensApprox, &c.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("scan chunk: %w", err)
		}
		chunks = append(chunks, c)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate chunks: %w", err)
	}

	return chunks, nil
}

// ListUnsummarizedSessions returns sessions that need summarization
func (db *DB) ListUnsummarizedSessions(limit int) ([]int64, error) {
	query := `
		SELECT s.id
		FROM sessions s
		LEFT JOIN session_summaries ss ON s.id = ss.session_id
		WHERE ss.session_id IS NULL
		   OR s.message_count > ss.last_message_count
		ORDER BY s.updated_at DESC
		LIMIT ?
	`

	rows, err := db.Query(query, limit)
	if err != nil {
		return nil, fmt.Errorf("query unsummarized: %w", err)
	}
	defer rows.Close()

	var sessionIDs []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan session id: %w", err)
		}
		sessionIDs = append(sessionIDs, id)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sessions: %w", err)
	}

	return sessionIDs, nil
}

// GetSummarizationStats returns statistics about summarization
func (db *DB) GetSummarizationStats() (total, summarized, pending int, err error) {
	// Total sessions with messages
	err = db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE message_count > 0`).Scan(&total)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("count total: %w", err)
	}

	// Summarized sessions
	err = db.QueryRow(`SELECT COUNT(*) FROM session_summaries`).Scan(&summarized)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("count summarized: %w", err)
	}

	// Pending (including stale summaries)
	err = db.QueryRow(`
		SELECT COUNT(*)
		FROM sessions s
		LEFT JOIN session_summaries ss ON s.id = ss.session_id
		WHERE s.message_count > 0
		  AND (ss.session_id IS NULL OR s.message_count > ss.last_message_count)
	`).Scan(&pending)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("count pending: %w", err)
	}

	return total, summarized, pending, nil
}

// LoadSessionForSummarization loads a session with all its messages for summarization
func (db *DB) LoadSessionForSummarization(sessionID int64) (*models.Session, []models.Message, error) {
	// Load session
	var session models.Session
	var cwd, gitBranch, version sql.NullString
	var createdAt, updatedAt, importedAt, lastSyncedAt, fileMtime sql.NullTime
	err := db.QueryRow(`
		SELECT id, session_id, project_path, summary, leaf_uuid, cwd, git_branch,
		       created_at, updated_at, message_count, version, imported_at, last_synced_at,
		       file_hash, file_size, file_mtime
		FROM sessions
		WHERE id = ?
	`, sessionID).Scan(
		&session.ID, &session.SessionID, &session.ProjectPath, &session.Summary,
		&session.LeafUUID, &cwd, &gitBranch,
		&createdAt, &updatedAt, &session.MessageCount,
		&version, &importedAt, &lastSyncedAt,
		&session.FileHash, &session.FileSize, &fileMtime,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("load session: %w", err)
	}

	// Convert NULL to empty strings
	session.CWD = cwd.String
	session.GitBranch = gitBranch.String
	session.Version = version.String

	// Convert NULL to zero time
	if createdAt.Valid {
		session.CreatedAt = createdAt.Time
	}
	if updatedAt.Valid {
		session.UpdatedAt = updatedAt.Time
	}
	if importedAt.Valid {
		session.ImportedAt = importedAt.Time
	}
	if lastSyncedAt.Valid {
		session.LastSyncedAt = lastSyncedAt.Time
	}
	if fileMtime.Valid {
		session.FileMtime = fileMtime.Time
	}

	// Load messages
	rows, err := db.Query(`
		SELECT id, uuid, session_id, parent_uuid, type, sender, content,
		       text_content, timestamp, sequence, is_sidechain, cwd, git_branch, version
		FROM messages
		WHERE session_id = ?
		ORDER BY sequence ASC
	`, sessionID)
	if err != nil {
		return nil, nil, fmt.Errorf("load messages: %w", err)
	}
	defer rows.Close()

	var messages []models.Message
	for rows.Next() {
		var msg models.Message
		var msgCwd, msgGitBranch, msgVersion sql.NullString
		var msgTimestamp sql.NullTime
		var contentBytes []byte
		err := rows.Scan(
			&msg.ID, &msg.UUID, &msg.SessionID, &msg.ParentUUID, &msg.Type,
			&msg.Sender, &contentBytes, &msg.TextContent, &msgTimestamp,
			&msg.Sequence, &msg.IsSidechain, &msgCwd, &msgGitBranch, &msgVersion,
		)
		if err != nil {
			return nil, nil, fmt.Errorf("scan message: %w", err)
		}
		// Convert NULL to empty strings
		msg.CWD = msgCwd.String
		msg.GitBranch = msgGitBranch.String
		msg.Version = msgVersion.String
		// Convert NULL to zero time
		if msgTimestamp.Valid {
			msg.Timestamp = msgTimestamp.Time
		}
		// Convert []byte to json.RawMessage
		msg.Content = contentBytes
		messages = append(messages, msg)
	}

	if err = rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate messages: %w", err)
	}

	return &session, messages, nil
}
