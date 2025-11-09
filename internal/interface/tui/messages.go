package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/yourusername/ccrider/internal/core/db"
)

type errMsg struct {
	err error
}

type sessionsLoadedMsg struct {
	sessions []sessionItem
}

type sessionDetailLoadedMsg struct {
	detail sessionDetail
}

type searchResultsMsg struct {
	results []searchResult
}

func performSearch(database *db.DB, query string) tea.Cmd {
	return func() tea.Msg {
		// Minimum 2 characters to search (avoid useless single-char results)
		if len(query) < 2 {
			return searchResultsMsg{results: nil}
		}

		rows, err := database.Query(`
			SELECT
				s.session_id,
				COALESCE(s.summary, '') as summary,
				s.project_path,
				s.updated_at,
				m.type as message_type,
				SUBSTR(m.text_content, 1, 200) as snippet,
				m.sequence
			FROM messages m
			JOIN sessions s ON m.session_id = s.id
			WHERE m.text_content LIKE '%' || ? || '%'
			ORDER BY s.updated_at DESC, m.sequence ASC
			LIMIT 200
		`, query)
		if err != nil {
			return errMsg{err}
		}
		defer rows.Close()

		// Group matches by session
		sessionMap := make(map[string]*searchResult)
		seenMessages := make(map[string]map[int]bool) // sessionID -> sequence -> seen
		var sessionOrder []string

		for rows.Next() {
			var sessionID, summary, project, updatedAt, msgType, snippet string
			var sequence int
			if err := rows.Scan(&sessionID, &summary, &project, &updatedAt,
				&msgType, &snippet, &sequence); err != nil {
				return errMsg{err}
			}

			// Create or get existing session result
			result, exists := sessionMap[sessionID]
			if !exists {
				result = &searchResult{
					SessionID: sessionID,
					Summary:   summary,
					Project:   project,
					UpdatedAt: updatedAt,
					Matches:   []matchInfo{},
				}
				sessionMap[sessionID] = result
				sessionOrder = append(sessionOrder, sessionID)
				seenMessages[sessionID] = make(map[int]bool)
			}

			// Skip if we've already seen this message
			if seenMessages[sessionID][sequence] {
				continue
			}

			// Add this match to the session (limit to 3 distinct messages per session)
			if len(result.Matches) < 3 {
				result.Matches = append(result.Matches, matchInfo{
					MessageType: msgType,
					Snippet:     snippet,
					Sequence:    sequence,
				})
				seenMessages[sessionID][sequence] = true
			}
		}

		// Convert map to slice in order
		var results []searchResult
		for _, sessionID := range sessionOrder {
			results = append(results, *sessionMap[sessionID])
		}

		// Limit to 50 sessions
		if len(results) > 50 {
			results = results[:50]
		}

		return searchResultsMsg{results: results}
	}
}

func loadSessions(database *db.DB) tea.Cmd {
	return func() tea.Msg {
		rows, err := database.Query(`
			SELECT
				s.session_id,
				COALESCE(s.summary, ''),
				s.project_path,
				(SELECT COUNT(*) FROM messages WHERE session_id = s.id) as actual_message_count,
				s.updated_at,
				s.created_at,
				COALESCE(
					(SELECT text_content
					 FROM messages
					 WHERE session_id = s.id
					   AND type = 'user'
					   AND TRIM(text_content) != ''
					 ORDER BY sequence ASC
					 LIMIT 1),
					''
				) as first_message
			FROM sessions s
			WHERE (SELECT COUNT(*) FROM messages WHERE session_id = s.id) > 0
			ORDER BY s.updated_at DESC
			LIMIT 1000
		`)
		if err != nil {
			return errMsg{err}
		}
		defer rows.Close()

		var sessions []sessionItem
		for rows.Next() {
			var s sessionItem
			var firstMsg string
			if err := rows.Scan(&s.ID, &s.Summary, &s.Project,
				&s.MessageCount, &s.UpdatedAt, &s.CreatedAt, &firstMsg); err != nil {
				return errMsg{err}
			}

			// If no summary, use first line of first user message
			if s.Summary == "" && firstMsg != "" {
				s.Summary = firstLine(firstMsg, 80)
			}

			sessions = append(sessions, s)
		}

		return sessionsLoadedMsg{sessions}
	}
}

func firstLine(s string, maxLen int) string {
	// Find first newline or max length
	for i, r := range s {
		if r == '\n' || i >= maxLen {
			if i > maxLen {
				return s[:maxLen] + "..."
			}
			return s[:i]
		}
	}
	if len(s) > maxLen {
		return s[:maxLen] + "..."
	}
	return s
}

func loadSessionDetail(database *db.DB, sessionID string) tea.Cmd {
	return func() tea.Msg {
		// Get session info
		var session sessionItem
		err := database.QueryRow(`
			SELECT
				session_id,
				COALESCE(summary, ''),
				project_path,
				(SELECT COUNT(*) FROM messages WHERE session_id = sessions.id) as actual_message_count,
				updated_at,
				created_at
			FROM sessions
			WHERE session_id = ?
		`, sessionID).Scan(&session.ID, &session.Summary, &session.Project,
			&session.MessageCount, &session.UpdatedAt, &session.CreatedAt)
		if err != nil {
			return errMsg{err}
		}

		// Get messages
		rows, err := database.Query(`
			SELECT type, COALESCE(text_content, ''), timestamp
			FROM messages
			WHERE session_id = (SELECT id FROM sessions WHERE session_id = ?)
			ORDER BY sequence ASC
		`, sessionID)
		if err != nil {
			return errMsg{err}
		}
		defer rows.Close()

		var messages []messageItem
		for rows.Next() {
			var m messageItem
			if err := rows.Scan(&m.Type, &m.Content, &m.Timestamp); err != nil {
				return errMsg{err}
			}
			messages = append(messages, m)
		}

		return sessionDetailLoadedMsg{
			detail: sessionDetail{
				Session:  session,
				Messages: messages,
			},
		}
	}
}
