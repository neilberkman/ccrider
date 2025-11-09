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

func loadSessions(database *db.DB) tea.Cmd {
	return func() tea.Msg {
		rows, err := database.Query(`
			SELECT
				s.session_id,
				COALESCE(s.summary, ''),
				s.project_path,
				s.message_count,
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
			SELECT session_id, COALESCE(summary, ''), project_path,
			       message_count, updated_at, created_at
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
