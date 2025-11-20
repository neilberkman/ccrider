package db

import (
	"fmt"
)

// SessionIssue represents an issue ID mentioned in a session
type SessionIssue struct {
	SessionID         int64
	IssueID           string
	FirstMentionIndex int
	LastMentionIndex  int
	MentionCount      int
}

// SessionFile represents a file mentioned in a session
type SessionFile struct {
	SessionID         int64
	FilePath          string
	MentionCount      int
	LastModifiedIndex int
}

// SaveSessionMetadata saves issue IDs and file paths for a session
func (db *DB) SaveSessionMetadata(sessionID int64, issues []SessionIssue, files []SessionFile) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Delete existing metadata for this session
	_, err = tx.Exec(`DELETE FROM session_issues WHERE session_id = ?`, sessionID)
	if err != nil {
		return fmt.Errorf("delete old issues: %w", err)
	}

	_, err = tx.Exec(`DELETE FROM session_files WHERE session_id = ?`, sessionID)
	if err != nil {
		return fmt.Errorf("delete old files: %w", err)
	}

	// Insert issue IDs
	for _, issue := range issues {
		_, err = tx.Exec(`
			INSERT INTO session_issues
			(session_id, issue_id, first_mention_msg_index, last_mention_msg_index)
			VALUES (?, ?, ?, ?)
		`, sessionID, issue.IssueID, issue.FirstMentionIndex, issue.LastMentionIndex)

		if err != nil {
			return fmt.Errorf("insert issue %s: %w", issue.IssueID, err)
		}
	}

	// Insert file paths
	for _, file := range files {
		_, err = tx.Exec(`
			INSERT INTO session_files
			(session_id, file_path, mention_count, last_modified_msg_index)
			VALUES (?, ?, ?, ?)
		`, sessionID, file.FilePath, file.MentionCount, file.LastModifiedIndex)

		if err != nil {
			return fmt.Errorf("insert file %s: %w", file.FilePath, err)
		}
	}

	return tx.Commit()
}

// FindSessionsByIssueID finds all sessions mentioning an issue ID
func (db *DB) FindSessionsByIssueID(issueID string) ([]Session, error) {
	query := `
		SELECT DISTINCT
			s.session_id,
			COALESCE(s.summary, ''),
			s.project_path,
			(SELECT COUNT(*) FROM messages WHERE session_id = s.id) as message_count,
			s.updated_at,
			s.created_at
		FROM sessions s
		JOIN session_issues si ON s.id = si.session_id
		WHERE LOWER(si.issue_id) = LOWER(?)
		ORDER BY s.updated_at DESC
	`

	rows, err := db.Query(query, issueID)
	if err != nil {
		return nil, fmt.Errorf("query sessions by issue: %w", err)
	}
	defer rows.Close()

	var sessions []Session
	for rows.Next() {
		var s Session
		err := rows.Scan(
			&s.SessionID,
			&s.Summary,
			&s.ProjectPath,
			&s.MessageCount,
			&s.UpdatedAt,
			&s.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		sessions = append(sessions, s)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sessions: %w", err)
	}

	return sessions, nil
}

// FindSessionsByFilePath finds all sessions mentioning a file path
func (db *DB) FindSessionsByFilePath(filePath string) ([]Session, error) {
	query := `
		SELECT DISTINCT
			s.session_id,
			COALESCE(s.summary, ''),
			s.project_path,
			(SELECT COUNT(*) FROM messages WHERE session_id = s.id) as message_count,
			s.updated_at,
			s.created_at
		FROM sessions s
		JOIN session_files sf ON s.id = sf.session_id
		WHERE sf.file_path LIKE ?
		ORDER BY s.updated_at DESC
	`

	rows, err := db.Query(query, "%"+filePath+"%")
	if err != nil {
		return nil, fmt.Errorf("query sessions by file: %w", err)
	}
	defer rows.Close()

	var sessions []Session
	for rows.Next() {
		var s Session
		err := rows.Scan(
			&s.SessionID,
			&s.Summary,
			&s.ProjectPath,
			&s.MessageCount,
			&s.UpdatedAt,
			&s.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		sessions = append(sessions, s)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sessions: %w", err)
	}

	return sessions, nil
}

// GetSessionMetadata retrieves metadata for a session
func (db *DB) GetSessionMetadata(sessionID int64) ([]SessionIssue, []SessionFile, error) {
	// Get issues
	issueRows, err := db.Query(`
		SELECT session_id, issue_id, first_mention_msg_index, last_mention_msg_index
		FROM session_issues
		WHERE session_id = ?
		ORDER BY first_mention_msg_index
	`, sessionID)
	if err != nil {
		return nil, nil, fmt.Errorf("query issues: %w", err)
	}
	defer issueRows.Close()

	var issues []SessionIssue
	for issueRows.Next() {
		var issue SessionIssue
		err := issueRows.Scan(
			&issue.SessionID,
			&issue.IssueID,
			&issue.FirstMentionIndex,
			&issue.LastMentionIndex,
		)
		if err != nil {
			return nil, nil, fmt.Errorf("scan issue: %w", err)
		}
		issues = append(issues, issue)
	}

	// Get files
	fileRows, err := db.Query(`
		SELECT session_id, file_path, mention_count, last_modified_msg_index
		FROM session_files
		WHERE session_id = ?
		ORDER BY mention_count DESC
	`, sessionID)
	if err != nil {
		return nil, nil, fmt.Errorf("query files: %w", err)
	}
	defer fileRows.Close()

	var files []SessionFile
	for fileRows.Next() {
		var file SessionFile
		err := fileRows.Scan(
			&file.SessionID,
			&file.FilePath,
			&file.MentionCount,
			&file.LastModifiedIndex,
		)
		if err != nil {
			return nil, nil, fmt.Errorf("scan file: %w", err)
		}
		files = append(files, file)
	}

	return issues, files, nil
}

// GetMetadataStats returns statistics about extracted metadata
func (db *DB) GetMetadataStats() (issues, files, sessions int, err error) {
	// Count unique issue IDs
	err = db.QueryRow(`SELECT COUNT(DISTINCT issue_id) FROM session_issues`).Scan(&issues)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("count issues: %w", err)
	}

	// Count unique file paths
	err = db.QueryRow(`SELECT COUNT(DISTINCT file_path) FROM session_files`).Scan(&files)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("count files: %w", err)
	}

	// Count sessions with metadata
	err = db.QueryRow(`
		SELECT COUNT(DISTINCT session_id)
		FROM (
			SELECT session_id FROM session_issues
			UNION
			SELECT session_id FROM session_files
		)
	`).Scan(&sessions)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("count sessions: %w", err)
	}

	return issues, files, sessions, nil
}
