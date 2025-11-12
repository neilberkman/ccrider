package db

import (
	"database/sql"
	"time"
)

// Session represents a session returned from ListSessions
type Session struct {
	SessionID    string
	Summary      string
	ProjectPath  string
	MessageCount int
	UpdatedAt    time.Time
	CreatedAt    time.Time
}

// ListSessions returns all sessions, optionally filtered by project path
// Sessions with no meaningful content (warmup-only, etc) are excluded
func (db *DB) ListSessions(projectPath string) ([]Session, error) {
	query := `
		SELECT
			s.session_id,
			CASE
				WHEN s.summary LIKE '<user%prompt%>' OR s.summary = '<user_prompt>' OR TRIM(COALESCE(s.summary, '')) = ''
				THEN COALESCE(
					(SELECT
						CASE
							WHEN text_content LIKE '<%'
							THEN LTRIM(SUBSTR(text_content, INSTR(text_content, '>') + 1), char(10) || char(13) || char(9) || ' ')
							ELSE text_content
						END
					 FROM messages
					 WHERE session_id = s.id
					   AND type = 'user'
					   AND text_content NOT LIKE 'This session is being continued%'
					   AND text_content NOT LIKE 'Resuming session from%'
					   AND text_content NOT LIKE '[Image %'
					   AND text_content NOT LIKE '%Request interrupted by user%'
					   AND text_content NOT LIKE 'Warmup'
					   AND text_content NOT LIKE 'Base directory for this skill:%'
					   AND LENGTH(LTRIM(
					     CASE
					       WHEN text_content LIKE '<%'
					       THEN LTRIM(SUBSTR(text_content, INSTR(text_content, '>') + 1), char(10) || char(13) || char(9) || ' ')
					       ELSE text_content
					     END,
					     char(10) || char(13) || char(9) || ' '
					   )) > 0
					 ORDER BY sequence ASC
					 LIMIT 1),
					''
				)
				ELSE s.summary
			END as summary,
			s.project_path,
			(SELECT COUNT(*) FROM messages WHERE session_id = s.id) as actual_message_count,
			s.updated_at,
			s.created_at,
			'' as first_message
		FROM sessions s
		WHERE (SELECT COUNT(*) FROM messages WHERE session_id = s.id) > 0
		  -- Exclude sessions with no meaningful content
		  AND NOT (
			  -- Has bad/empty summary
			  (s.summary LIKE '<user%prompt%>' OR s.summary = '<user_prompt>' OR TRIM(s.summary) = '')
			  -- AND no meaningful user messages
			  AND NOT EXISTS (
				  SELECT 1 FROM messages
				  WHERE session_id = s.id
					AND type = 'user'
					AND TRIM(text_content) != ''
					AND text_content NOT LIKE 'This session is being continued%'
					AND text_content NOT LIKE 'Resuming session from%'
					AND text_content NOT LIKE '[Image %'
					AND text_content NOT LIKE '%Request interrupted by user%'
					AND text_content NOT LIKE 'Warmup'
					AND text_content NOT LIKE 'Base directory for this skill:%'
			  )
		  )`

	args := []interface{}{}
	if projectPath != "" {
		query += " AND s.project_path LIKE ?"
		args = append(args, "%"+projectPath+"%")
	}

	query += `
		ORDER BY s.updated_at DESC
		LIMIT 1000
	`

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []Session
	for rows.Next() {
		var s Session
		var unusedFirstMessage string // from SELECT but not used
		err := rows.Scan(
			&s.SessionID,
			&s.Summary,
			&s.ProjectPath,
			&s.MessageCount,
			&s.UpdatedAt,
			&s.CreatedAt,
			&unusedFirstMessage,
		)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, s)
	}

	return sessions, rows.Err()
}

// GetSessionLaunchInfo returns the minimal info needed to launch/resume a session
func (db *DB) GetSessionLaunchInfo(sessionID string) (*Session, string, error) {
	query := `
		SELECT
			s.session_id,
			COALESCE(s.summary, ''),
			s.project_path,
			(SELECT COUNT(*) FROM messages WHERE session_id = s.id) as actual_message_count,
			s.updated_at,
			s.created_at,
			COALESCE(
				(SELECT cwd FROM messages
				 WHERE session_id = s.id
				   AND cwd IS NOT NULL
				   AND cwd != ''
				   AND cwd != '/'
				 ORDER BY sequence DESC LIMIT 1),
				s.project_path
			) as last_cwd
		FROM sessions s
		WHERE s.session_id = ?
	`

	var session Session
	var lastCwd string
	err := db.QueryRow(query, sessionID).Scan(
		&session.SessionID,
		&session.Summary,
		&session.ProjectPath,
		&session.MessageCount,
		&session.UpdatedAt,
		&session.CreatedAt,
		&lastCwd,
	)
	if err != nil {
		return nil, "", err
	}

	return &session, lastCwd, nil
}

// GetSessionDetail returns full details for a single session
func (db *DB) GetSessionDetail(sessionID string) (*SessionDetail, error) {
	// First get the session metadata
	query := `
		SELECT
			session_id,
			COALESCE(summary, ''),
			project_path,
			(SELECT COUNT(*) FROM messages WHERE session_id = s.id) as message_count,
			cwd,
			updated_at
		FROM sessions s
		WHERE session_id = ?
	`

	var detail SessionDetail
	var cwd sql.NullString
	err := db.QueryRow(query, sessionID).Scan(
		&detail.SessionID,
		&detail.Summary,
		&detail.ProjectPath,
		&detail.MessageCount,
		&cwd,
		&detail.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	detail.CWD = cwd.String

	// Get all messages for this session
	messagesQuery := `
		SELECT
			type,
			sender,
			text_content,
			timestamp
		FROM messages
		WHERE session_id = (SELECT id FROM sessions WHERE session_id = ?)
		ORDER BY sequence ASC
	`

	rows, err := db.Query(messagesQuery, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var msg SessionMessage
		err := rows.Scan(&msg.Type, &msg.Sender, &msg.Content, &msg.Timestamp)
		if err != nil {
			return nil, err
		}
		detail.Messages = append(detail.Messages, msg)
	}

	return &detail, rows.Err()
}

// SessionDetail represents full session information including messages
type SessionDetail struct {
	SessionID    string
	Summary      string
	ProjectPath  string
	MessageCount int
	CWD          string
	UpdatedAt    time.Time
	Messages     []SessionMessage
}

// SessionMessage represents a single message in a session
type SessionMessage struct {
	Type      string
	Sender    string
	Content   string
	Timestamp time.Time
}
