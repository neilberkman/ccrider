package db

import (
	"time"
)

// Session represents a session returned from ListSessions
type Session struct {
	SessionID    string
	Summary      string
	ProjectPath  string
	LastCwd      string // Last working directory from messages
	MessageCount int
	UpdatedAt    time.Time
	CreatedAt    time.Time
	Provider     string // claude, codex, etc.
}

// ListSessions returns all sessions, optionally filtered by project path and provider.
// Sessions with no meaningful content (warmup-only, etc) are excluded.
func (db *DB) ListSessions(projectPath string, provider ...string) ([]Session, error) {
	query := `
		SELECT
			s.session_id,
			COALESCE(
				NULLIF(ss.one_line_summary, ''),
				NULLIF(s.llm_summary, ''),
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
				END,
				''
			) as summary,
			s.project_path,
			COALESCE(
				(SELECT cwd FROM messages
				 WHERE session_id = s.id
				   AND cwd IS NOT NULL
				   AND cwd != ''
				 ORDER BY sequence DESC
				 LIMIT 1),
				s.cwd,
				s.project_path
			) as last_cwd,
			(SELECT COUNT(*) FROM messages WHERE session_id = s.id) as actual_message_count,
			s.updated_at,
			s.created_at,
			COALESCE(s.provider, 'claude') as provider
		FROM sessions s
		LEFT JOIN session_summaries ss ON s.id = ss.session_id
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
	if len(provider) > 0 && provider[0] != "" {
		query += " AND COALESCE(s.provider, 'claude') = ?"
		args = append(args, provider[0])
	}

	query += `
		ORDER BY s.updated_at DESC
		LIMIT 1000
	`

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var sessions []Session
	for rows.Next() {
		var s Session
		err := rows.Scan(
			&s.SessionID,
			&s.Summary,
			&s.ProjectPath,
			&s.LastCwd,
			&s.MessageCount,
			&s.UpdatedAt,
			&s.CreatedAt,
			&s.Provider,
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
			) as last_cwd,
			COALESCE(s.provider, 'claude') as provider
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
		&session.Provider,
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
			COALESCE(
				(SELECT cwd FROM messages
				 WHERE session_id = s.id
				   AND cwd IS NOT NULL
				   AND cwd != ''
				   AND cwd != '/'
				 ORDER BY sequence DESC LIMIT 1),
				s.project_path
			) as last_cwd,
			updated_at,
			COALESCE(s.provider, 'claude') as provider
		FROM sessions s
		WHERE session_id = ?
	`

	var detail SessionDetail
	err := db.QueryRow(query, sessionID).Scan(
		&detail.SessionID,
		&detail.Summary,
		&detail.ProjectPath,
		&detail.MessageCount,
		&detail.LastCwd,
		&detail.UpdatedAt,
		&detail.Provider,
	)
	if err != nil {
		return nil, err
	}

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
	defer func() { _ = rows.Close() }()

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
	LastCwd      string // Last working directory from messages
	UpdatedAt    time.Time
	Messages     []SessionMessage
	Provider     string // claude, codex, etc.
}

// SessionMessage represents a single message in a session
type SessionMessage struct {
	Type      string
	Sender    string
	Content   string
	Timestamp time.Time
	Sequence  int
}

// RecoveryContext contains information needed to start a recovery session
// when the original session file is missing
type RecoveryContext struct {
	SessionID    string
	Summary      string
	ProjectPath  string
	LastCwd      string
	MessageCount int
	UpdatedAt    time.Time
	FirstMsgs    []SessionMessage // First N messages for context
	LastMsgs     []SessionMessage // Last N messages for context
}

// GetRecoveryContext retrieves context from an old session for recovery mode.
// This is used when the session file is missing but we have data in CCRider's database.
func (db *DB) GetRecoveryContext(sessionID string, contextMsgs int) (*RecoveryContext, error) {
	if contextMsgs <= 0 {
		contextMsgs = 5
	}

	// Get session metadata
	session, lastCwd, err := db.GetSessionLaunchInfo(sessionID)
	if err != nil {
		return nil, err
	}

	ctx := &RecoveryContext{
		SessionID:    session.SessionID,
		Summary:      session.Summary,
		ProjectPath:  session.ProjectPath,
		LastCwd:      lastCwd,
		MessageCount: session.MessageCount,
		UpdatedAt:    session.UpdatedAt,
	}

	// Get first N messages
	firstQuery := `
		SELECT type, sender, text_content, timestamp, sequence
		FROM messages
		WHERE session_id = (SELECT id FROM sessions WHERE session_id = ?)
		ORDER BY sequence ASC
		LIMIT ?
	`
	rows, err := db.Query(firstQuery, sessionID, contextMsgs)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var msg SessionMessage
		if err := rows.Scan(&msg.Type, &msg.Sender, &msg.Content, &msg.Timestamp, &msg.Sequence); err != nil {
			return nil, err
		}
		ctx.FirstMsgs = append(ctx.FirstMsgs, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Get last N messages (different from first)
	lastQuery := `
		SELECT type, sender, text_content, timestamp, sequence
		FROM messages
		WHERE session_id = (SELECT id FROM sessions WHERE session_id = ?)
		ORDER BY sequence DESC
		LIMIT ?
	`
	rows2, err := db.Query(lastQuery, sessionID, contextMsgs)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows2.Close() }()

	var lastMsgs []SessionMessage
	for rows2.Next() {
		var msg SessionMessage
		if err := rows2.Scan(&msg.Type, &msg.Sender, &msg.Content, &msg.Timestamp, &msg.Sequence); err != nil {
			return nil, err
		}
		lastMsgs = append(lastMsgs, msg)
	}
	if err := rows2.Err(); err != nil {
		return nil, err
	}

	// Reverse to get chronological order
	for i, j := 0, len(lastMsgs)-1; i < j; i, j = i+1, j-1 {
		lastMsgs[i], lastMsgs[j] = lastMsgs[j], lastMsgs[i]
	}
	ctx.LastMsgs = lastMsgs

	return ctx, nil
}

// GetSessionMessagesOptions specifies filtering options for GetSessionMessages
type GetSessionMessagesOptions struct {
	LastN          int // Return last N messages (tail mode)
	AroundSequence int // Return messages around this sequence number
	ContextSize    int // Messages before/after AroundSequence (default 10)
}

// GetSessionMessages returns messages from a session with optional filtering
// - LastN > 0: returns last N messages
// - AroundSequence > 0: returns messages around that sequence (±ContextSize)
// - Neither: returns all messages
func (db *DB) GetSessionMessages(sessionID string, opts GetSessionMessagesOptions) ([]SessionMessage, int, error) {
	// First get total message count
	var totalCount int
	err := db.QueryRow(`
		SELECT COUNT(*) FROM messages
		WHERE session_id = (SELECT id FROM sessions WHERE session_id = ?)
	`, sessionID).Scan(&totalCount)
	if err != nil {
		return nil, 0, err
	}

	// Build query based on options
	var query string
	var args []interface{}

	if opts.LastN > 0 {
		// Tail mode: get last N messages
		query = `
			SELECT type, sender, text_content, timestamp, sequence
			FROM messages
			WHERE session_id = (SELECT id FROM sessions WHERE session_id = ?)
			ORDER BY sequence DESC
			LIMIT ?
		`
		args = []interface{}{sessionID, opts.LastN}
	} else if opts.AroundSequence > 0 {
		// Context mode: get messages around a sequence number
		contextSize := opts.ContextSize
		if contextSize <= 0 {
			contextSize = 10
		}
		startSeq := opts.AroundSequence - contextSize
		if startSeq < 0 {
			startSeq = 0
		}
		endSeq := opts.AroundSequence + contextSize

		query = `
			SELECT type, sender, text_content, timestamp, sequence
			FROM messages
			WHERE session_id = (SELECT id FROM sessions WHERE session_id = ?)
			  AND sequence >= ? AND sequence <= ?
			ORDER BY sequence ASC
		`
		args = []interface{}{sessionID, startSeq, endSeq}
	} else {
		// All messages
		query = `
			SELECT type, sender, text_content, timestamp, sequence
			FROM messages
			WHERE session_id = (SELECT id FROM sessions WHERE session_id = ?)
			ORDER BY sequence ASC
		`
		args = []interface{}{sessionID}
	}

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()

	var messages []SessionMessage
	for rows.Next() {
		var msg SessionMessage
		err := rows.Scan(&msg.Type, &msg.Sender, &msg.Content, &msg.Timestamp, &msg.Sequence)
		if err != nil {
			return nil, 0, err
		}
		messages = append(messages, msg)
	}

	// For LastN, we selected DESC so reverse to get chronological order
	if opts.LastN > 0 {
		for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
			messages[i], messages[j] = messages[j], messages[i]
		}
	}

	return messages, totalCount, rows.Err()
}
