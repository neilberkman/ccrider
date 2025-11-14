package tui

import (
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/neilberkman/ccrider/internal/core/db"
	"github.com/neilberkman/ccrider/internal/core/importer"
	"github.com/neilberkman/ccrider/internal/core/search"
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
		// Strip any quotes from the query (user shouldn't need to escape)
		query = strings.Trim(query, "\"")
		query = strings.ReplaceAll(query, "\\\"", "\"")

		// Minimum 2 characters to search (avoid useless single-char results)
		if len(query) < 2 {
			return searchResultsMsg{results: nil}
		}

		// Parse filters from query (interface concern - normalizing user input)
		filters := ParseSearchQuery(query)
		searchQuery := filters.Query
		if searchQuery == "" {
			searchQuery = query // Fallback if only filters
		}

		// Call core search function (uses FTS5)
		coreResults, err := search.Search(database, searchQuery)
		if err != nil {
			return errMsg{err}
		}

		// Interface concern: Apply date/project filters and group by session
		sessionMap := make(map[string]*searchResult)
		var sessionOrder []string

		for _, coreResult := range coreResults {
			// Apply project filter if specified
			if filters.Project != "" && !strings.Contains(coreResult.ProjectPath, filters.Project) {
				continue
			}

			// Apply date filters if specified
			if filters.HasAfter || filters.HasBefore {
				// Parse timestamp from core result
				// For now, skip date filtering on individual messages
				// TODO: Apply date filters properly
			}

			// Group by session (interface concern - presentation)
			sessionID := coreResult.SessionID
			result, exists := sessionMap[sessionID]
			if !exists {
				result = &searchResult{
					SessionID: sessionID,
					Summary:   coreResult.SessionSummary,
					Project:   coreResult.ProjectPath,
					UpdatedAt: coreResult.Timestamp,
					Matches:   []matchInfo{},
				}
				sessionMap[sessionID] = result
				sessionOrder = append(sessionOrder, sessionID)
			}

			// Add this match (limit to 3 per session)
			if len(result.Matches) < 3 {
				// Extract message type from UUID or default to "message"
				msgType := "message"

				result.Matches = append(result.Matches, matchInfo{
					MessageType: msgType,
					Snippet:     coreResult.MessageText,
					Sequence:    0, // Core doesn't provide sequence
				})
			}
		}

		// Convert map to slice in order
		var results []searchResult
		for _, sessionID := range sessionOrder {
			results = append(results, *sessionMap[sessionID])
		}

		// Limit to 50 sessions (interface concern - pagination)
		if len(results) > 50 {
			results = results[:50]
		}

		return searchResultsMsg{results: results}
	}
}

func loadSessions(database *db.DB, filterByProject bool, projectPath string) tea.Cmd {
	return func() tea.Msg {
		// Use core function to get sessions
		filterPath := ""
		if filterByProject {
			filterPath = projectPath
		}

		coreSessions, err := database.ListSessions(filterPath)
		if err != nil {
			return errMsg{err}
		}

		// Convert core sessions to TUI session items (interface-specific presentation)
		var sessions []sessionItem
		for _, cs := range coreSessions {
			// Core already handles summary fallback, just format for display
			summary := cs.Summary
			if summary != "" {
				summary = firstLine(summary, 80)
			}

			s := sessionItem{
				ID:           cs.SessionID,
				Summary:      summary,
				Project:      cs.ProjectPath,
				MessageCount: cs.MessageCount,
				UpdatedAt:    cs.UpdatedAt.Format("2006-01-02 15:04:05"),
				CreatedAt:    cs.CreatedAt.Format("2006-01-02 15:04:05"),
			}

			// Check if session matches current directory (for highlighting - interface concern)
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

type syncProgressMsg struct {
	current         int
	total           int
	sessionName     string
	ch              chan syncProgressMsg
	db              *db.DB
	filterByProject bool
	projectPath     string
}

// StartSyncWithProgress initiates a sync and returns a command that listens for progress
func startSyncWithProgress(database *db.DB, filterByProject bool, projectPath string) tea.Cmd {
	return func() tea.Msg {
		// Get default Claude directory
		home, _ := os.UserHomeDir()
		sourcePath := filepath.Join(home, ".claude", "projects")

		// Count total files first
		var files []string
		filepath.Walk(sourcePath, func(path string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() && filepath.Ext(path) == ".jsonl" {
				files = append(files, path)
			}
			return nil
		})

		total := len(files)

		// Send initial progress message with total
		// This will be sent IMMEDIATELY before any import starts
		// We'll use a subscription pattern - send progress via tea.Program
		progressCh := make(chan syncProgressMsg, 100)

		// Send initial message with total count immediately
		// This ensures the progress bar shows up right away
		progressCh <- syncProgressMsg{
			current:     0,
			total:       total,
			sessionName: "",
		}

		// Start sync in background goroutine
		go func() {
			imp := importer.New(database)
			progress := &channelProgressReporter{
				total:   total,
				current: 0,
				ch:      progressCh,
			}

			imp.ImportDirectory(sourcePath, progress)
			close(progressCh)
		}()

		// This goroutine will send progress updates to the TUI
		// But we can't return multiple messages from one Cmd
		// So we'll use a different pattern: subscribe to the channel
		return syncSubscribe(progressCh, database, filterByProject, projectPath)()
	}
}

type channelProgressReporter struct {
	total   int
	current int
	ch      chan syncProgressMsg
}

func (r *channelProgressReporter) Update(sessionSummary string, firstMsg string) {
	r.current++
	// Send progress update via channel immediately - no polling!
	r.ch <- syncProgressMsg{
		current:     r.current,
		total:       r.total,
		sessionName: sessionSummary,
	}
}

func (r *channelProgressReporter) Finish() {}

// syncSubscribe listens to the progress channel and returns the next message
func syncSubscribe(progressCh chan syncProgressMsg, database *db.DB, filterByProject bool, projectPath string) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-progressCh
		if !ok {
			// Channel closed, sync is done
			return loadSessions(database, filterByProject, projectPath)()
		}
		// Add the channel and db info so we can chain the next subscription
		msg.ch = progressCh
		msg.db = database
		msg.filterByProject = filterByProject
		msg.projectPath = projectPath
		return msg
	}
}

func syncSessions(database *db.DB, filterByProject bool, projectPath string) tea.Cmd {
	return startSyncWithProgress(database, filterByProject, projectPath)
}
