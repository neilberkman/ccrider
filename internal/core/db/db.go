package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// DB wraps a SQLite database connection
type DB struct {
	conn *sql.DB
}

// New creates a new database connection and initializes schema
func New(dbPath string) (*DB, error) {
	// Ensure parent directory exists
	dbDir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dbDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create database directory: %w", err)
	}

	// Open with WAL mode for concurrent reads
	dsn := dbPath + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Set connection pool settings
	conn.SetMaxOpenConns(1) // SQLite only supports one writer
	conn.SetMaxIdleConns(1)
	conn.SetConnMaxLifetime(time.Hour)

	db := &DB{conn: conn}

	// Initialize schema
	if err := db.initSchema(); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("failed to initialize schema: %w", err)
	}

	// Run migrations for existing databases
	if err := db.migrate(); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	return db, nil
}

// Close closes the database connection
func (db *DB) Close() error {
	return db.conn.Close()
}

// Begin starts a new transaction
func (db *DB) Begin() (*sql.Tx, error) {
	return db.conn.Begin()
}

// Exec executes a query
func (db *DB) Exec(query string, args ...interface{}) (sql.Result, error) {
	return db.conn.Exec(query, args...)
}

// Query executes a query that returns rows
func (db *DB) Query(query string, args ...interface{}) (*sql.Rows, error) {
	return db.conn.Query(query, args...)
}

// QueryRow executes a query that returns a single row
func (db *DB) QueryRow(query string, args ...interface{}) *sql.Row {
	return db.conn.QueryRow(query, args...)
}

// UpdateLLMSummary updates the LLM-generated summary for a session (legacy, use SaveSessionSummary)
func (db *DB) UpdateLLMSummary(sessionID string, summary string) error {
	_, err := db.conn.Exec(`
		UPDATE sessions
		SET llm_summary = ?, llm_summary_at = CURRENT_TIMESTAMP
		WHERE session_id = ?
	`, summary, sessionID)
	return err
}

// SessionSummary represents a complete session summary with chunks
type SessionSummary struct {
	SessionID      int64
	OneLine        string
	Full           string
	Version        int
	MessageCount   int
	TokensApprox   int
	ChunkSummaries []ChunkSummary
}

// ChunkSummary represents a summary of a message chunk
type ChunkSummary struct {
	ChunkIndex   int
	MessageStart int
	MessageEnd   int
	Summary      string
	TokensApprox int
}

// SaveSessionSummary saves a hierarchical session summary with chunks
func (db *DB) SaveSessionSummary(summary SessionSummary) error {
	tx, err := db.conn.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Upsert main summary
	_, err = tx.Exec(`
		INSERT INTO session_summaries
		(session_id, one_line_summary, full_summary, summary_version, last_message_count, tokens_approx, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(session_id) DO UPDATE SET
			one_line_summary = excluded.one_line_summary,
			full_summary = excluded.full_summary,
			summary_version = session_summaries.summary_version + 1,
			last_message_count = excluded.last_message_count,
			tokens_approx = excluded.tokens_approx,
			updated_at = CURRENT_TIMESTAMP
	`, summary.SessionID, summary.OneLine, summary.Full, summary.Version, summary.MessageCount, summary.TokensApprox)
	if err != nil {
		return fmt.Errorf("upsert summary: %w", err)
	}

	// Insert/update chunk summaries
	for _, chunk := range summary.ChunkSummaries {
		_, err = tx.Exec(`
			INSERT INTO summary_chunks
			(session_id, chunk_index, message_start, message_end, summary, tokens_approx)
			VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(session_id, chunk_index) DO UPDATE SET
				message_start = excluded.message_start,
				message_end = excluded.message_end,
				summary = excluded.summary,
				tokens_approx = excluded.tokens_approx
		`, summary.SessionID, chunk.ChunkIndex, chunk.MessageStart, chunk.MessageEnd, chunk.Summary, chunk.TokensApprox)
		if err != nil {
			return fmt.Errorf("upsert chunk %d: %w", chunk.ChunkIndex, err)
		}
	}

	// Also update the sessions table for backwards compat
	_, err = tx.Exec(`
		UPDATE sessions
		SET llm_summary = ?, llm_summary_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, summary.OneLine, summary.SessionID)
	if err != nil {
		return fmt.Errorf("update sessions table: %w", err)
	}

	return tx.Commit()
}

// GetSessionSummary retrieves a session summary by session ID
func (db *DB) GetSessionSummary(sessionID int64) (*SessionSummary, error) {
	var s SessionSummary
	err := db.conn.QueryRow(`
		SELECT session_id, one_line_summary, full_summary, summary_version, last_message_count, tokens_approx
		FROM session_summaries WHERE session_id = ?
	`, sessionID).Scan(&s.SessionID, &s.OneLine, &s.Full, &s.Version, &s.MessageCount, &s.TokensApprox)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	// Load chunks
	rows, err := db.conn.Query(`
		SELECT chunk_index, message_start, message_end, summary, tokens_approx
		FROM summary_chunks WHERE session_id = ? ORDER BY chunk_index
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var c ChunkSummary
		if err := rows.Scan(&c.ChunkIndex, &c.MessageStart, &c.MessageEnd, &c.Summary, &c.TokensApprox); err != nil {
			return nil, err
		}
		s.ChunkSummaries = append(s.ChunkSummaries, c)
	}

	return &s, nil
}

// SessionIssue represents an issue ID found in a session
type SessionIssue struct {
	SessionID       int64
	IssueID         string
	FirstMentionSeq int
	LastMentionSeq  int
	MentionCount    int
}

// SaveSessionIssues saves extracted issue IDs for a session
func (db *DB) SaveSessionIssues(sessionID int64, issues []SessionIssue) error {
	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Clear existing issues for this session
	_, err = tx.Exec(`DELETE FROM session_issues WHERE session_id = ?`, sessionID)
	if err != nil {
		return err
	}

	for _, issue := range issues {
		_, err = tx.Exec(`
			INSERT INTO session_issues (session_id, issue_id, issue_id_lower, first_mention_seq, last_mention_seq, mention_count)
			VALUES (?, ?, LOWER(?), ?, ?, ?)
		`, sessionID, issue.IssueID, issue.IssueID, issue.FirstMentionSeq, issue.LastMentionSeq, issue.MentionCount)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

// FindSessionsByIssueID finds sessions mentioning a specific issue
func (db *DB) FindSessionsByIssueID(issueID string) ([]int64, error) {
	rows, err := db.conn.Query(`
		SELECT DISTINCT session_id FROM session_issues
		WHERE issue_id_lower = LOWER(?)
		ORDER BY last_mention_seq DESC
	`, issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// SessionFile represents a file path found in a session
type SessionFile struct {
	SessionID       int64
	FilePath        string
	FileName        string
	MentionCount    int
	FirstMentionSeq int
	LastMentionSeq  int
}

// SaveSessionFiles saves extracted file paths for a session
func (db *DB) SaveSessionFiles(sessionID int64, files []SessionFile) error {
	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Clear existing files for this session
	_, err = tx.Exec(`DELETE FROM session_files WHERE session_id = ?`, sessionID)
	if err != nil {
		return err
	}

	for _, f := range files {
		_, err = tx.Exec(`
			INSERT INTO session_files (session_id, file_path, file_name, mention_count, first_mention_seq, last_mention_seq)
			VALUES (?, ?, ?, ?, ?, ?)
		`, sessionID, f.FilePath, f.FileName, f.MentionCount, f.FirstMentionSeq, f.LastMentionSeq)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

// FindSessionsByFilePath finds sessions mentioning a specific file
func (db *DB) FindSessionsByFilePath(filePath string) ([]int64, error) {
	rows, err := db.conn.Query(`
		SELECT DISTINCT session_id FROM session_files
		WHERE file_path = ? OR file_name = ?
		ORDER BY last_mention_seq DESC
	`, filePath, filePath)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// ListUnsummarizedSessions returns sessions needing summarization
func (db *DB) ListUnsummarizedSessions(limit int) ([]struct {
	ID           int64
	SessionID    string
	ProjectPath  string
	MessageCount int
}, error) {
	rows, err := db.conn.Query(`
		SELECT s.id, s.session_id, s.project_path, s.message_count
		FROM sessions s
		LEFT JOIN session_summaries ss ON s.id = ss.session_id
		WHERE ss.session_id IS NULL
		   OR s.message_count > ss.last_message_count
		ORDER BY s.updated_at DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []struct {
		ID           int64
		SessionID    string
		ProjectPath  string
		MessageCount int
	}
	for rows.Next() {
		var s struct {
			ID           int64
			SessionID    string
			ProjectPath  string
			MessageCount int
		}
		if err := rows.Scan(&s.ID, &s.SessionID, &s.ProjectPath, &s.MessageCount); err != nil {
			return nil, err
		}
		sessions = append(sessions, s)
	}
	return sessions, nil
}
