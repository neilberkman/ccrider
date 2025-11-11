package tui

import (
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/yourusername/ccrider/internal/core/db"
	"github.com/yourusername/ccrider/internal/core/importer"
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

type sessionLaunchInfoMsg struct {
	sessionID   string
	projectPath string
	lastCwd     string
	updatedAt   string
	summary     string
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

		// Parse filters from query
		filters := ParseSearchQuery(query)
		searchQuery := filters.Query
		if searchQuery == "" {
			searchQuery = query // Fallback if only filters
		}

		// Build SQL query with filters
		sqlQuery := `
			SELECT DISTINCT s.session_id
			FROM messages m
			JOIN sessions s ON m.session_id = s.id
			WHERE m.text_content LIKE '%' || ? || '%'
		`
		args := []interface{}{searchQuery}

		// Add project filter
		if filters.Project != "" {
			sqlQuery += " AND s.project_path LIKE '%' || ? || '%'"
			args = append(args, filters.Project)
		}

		// Add date filters
		if filters.HasAfter {
			sqlQuery += " AND s.updated_at >= ?"
			args = append(args, filters.AfterDate.Format("2006-01-02 15:04:05"))
		}
		if filters.HasBefore {
			sqlQuery += " AND s.updated_at <= ?"
			args = append(args, filters.BeforeDate.Format("2006-01-02 15:04:05"))
		}

		sqlQuery += " ORDER BY s.updated_at DESC LIMIT 50"

		// First, get the top 50 sessions that have matches
		sessionRows, err := database.Query(sqlQuery, args...)
		if err != nil {
			return errMsg{err}
		}

		var sessionIDs []string
		for sessionRows.Next() {
			var sid string
			if err := sessionRows.Scan(&sid); err != nil {
				sessionRows.Close()
				return errMsg{err}
			}
			sessionIDs = append(sessionIDs, sid)
		}
		sessionRows.Close()

		if len(sessionIDs) == 0 {
			return searchResultsMsg{results: []searchResult{}}
		}

		// Build placeholders for IN clause
		placeholders := make([]string, len(sessionIDs))
		matchArgs := make([]interface{}, 0, len(sessionIDs)+1)
		matchArgs = append(matchArgs, searchQuery)
		for i, sid := range sessionIDs {
			placeholders[i] = "?"
			matchArgs = append(matchArgs, sid)
		}

		// Now get matches from those sessions (up to 3 per session)
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
			  AND s.session_id IN (`+strings.Join(placeholders, ",")+`)
			ORDER BY s.updated_at DESC, m.sequence ASC
		`, matchArgs...)
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

func loadSessions(database *db.DB, filterByProject bool, projectPath string) tea.Cmd {
	return func() tea.Msg {
		query := `
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
					   AND text_content NOT LIKE 'This session is being continued%'
					   AND text_content NOT LIKE 'Resuming session from%'
					   AND text_content NOT LIKE '[Image %'
					   AND text_content NOT LIKE '%Request interrupted by user%'
					   AND text_content NOT LIKE 'Warmup'
					   AND text_content NOT LIKE 'Base directory for this skill:%'
					 ORDER BY sequence ASC
					 LIMIT 1),
					''
				) as first_message
			FROM sessions s
			WHERE (SELECT COUNT(*) FROM messages WHERE session_id = s.id) > 0`

		// Add project filter if enabled
		args := []interface{}{}
		if filterByProject && projectPath != "" {
			query += " AND s.project_path LIKE ?"
			args = append(args, "%"+projectPath+"%")
		}

		query += `
			ORDER BY s.updated_at DESC
			LIMIT 1000
		`

		rows, err := database.Query(query, args...)
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

			// Check if session matches current directory (for highlighting)
			if projectPath != "" && strings.Contains(s.Project, projectPath) {
				s.MatchesCurrentDir = true
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

// loadSessionForLaunch loads just the info needed to launch a session (no messages)
func loadSessionForLaunch(database *db.DB, sessionID string) tea.Cmd {
	return func() tea.Msg {
		var session sessionItem
		var lastCwd string
		err := database.QueryRow(`
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
		`, sessionID).Scan(&session.ID, &session.Summary, &session.Project,
			&session.MessageCount, &session.UpdatedAt, &session.CreatedAt, &lastCwd)
		if err != nil {
			return errMsg{err}
		}

		return sessionLaunchInfoMsg{
			sessionID:   session.ID,
			projectPath: session.Project,
			lastCwd:     lastCwd,
			updatedAt:   session.UpdatedAt,
			summary:     session.Summary,
		}
	}
}

func loadSessionDetail(database *db.DB, sessionID string) tea.Cmd {
	return func() tea.Msg {
		// Get session info + last cwd
		var session sessionItem
		var lastCwd string
		err := database.QueryRow(`
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
		`, sessionID).Scan(&session.ID, &session.Summary, &session.Project,
			&session.MessageCount, &session.UpdatedAt, &session.CreatedAt, &lastCwd)
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
				Session:   session,
				Messages:  messages,
				LastCwd:   lastCwd,
				UpdatedAt: session.UpdatedAt,
			},
		}
	}
}

type syncCompletedMsg struct {
	success bool
	err     error
}

func syncSessions(database *db.DB, filterByProject bool, projectPath string) tea.Cmd {
	return func() tea.Msg {
		// Get default Claude directory
		home, err := os.UserHomeDir()
		if err != nil {
			return errMsg{err}
		}
		sourcePath := filepath.Join(home, ".claude", "projects")

		// Run the sync
		imp := importer.New(database)
		if err := imp.ImportDirectory(sourcePath, nil); err != nil {
			return syncCompletedMsg{
				success: false,
				err:     err,
			}
		}

		// After sync completes, reload sessions
		return loadSessions(database, filterByProject, projectPath)()
	}
}
