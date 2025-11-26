package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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

type exportCompletedMsg struct {
	success  bool
	filePath string
	err      error
}

type exportPromptMsg struct {
	sessionID string
}

func performSearch(database *db.DB, query string) tea.Cmd {
	return func() tea.Msg {
		// Strip any quotes from the query (user shouldn't need to escape)
		query = strings.Trim(query, "\"")
		query = strings.ReplaceAll(query, "\\\"", "\"")

		// Parse filters from query (interface concern - normalizing user input)
		tuiFilters := ParseSearchQuery(query)
		searchQuery := tuiFilters.Query
		if searchQuery == "" {
			searchQuery = query // Fallback if only filters
		}

		// Convert TUI filters to core filters
		coreFilters := search.SearchFilters{
			Query:       searchQuery,
			ProjectPath: tuiFilters.Project,
		}
		if tuiFilters.HasAfter {
			coreFilters.AfterDate = tuiFilters.AfterDate.Format(time.RFC3339)
		}
		if tuiFilters.HasBefore {
			coreFilters.BeforeDate = tuiFilters.BeforeDate.Format(time.RFC3339)
		}

		// Call core search with filters (business logic in core)
		coreResults, err := search.SearchWithFilters(database, coreFilters)
		if err != nil {
			return errMsg{err}
		}

		// Convert core types to interface types (interface concern - presentation)
		var results []searchResult
		for _, coreSession := range coreResults {
			result := searchResult{
				SessionID: coreSession.SessionID,
				Summary:   coreSession.SessionSummary,
				Project:   coreSession.ProjectPath,
				UpdatedAt: coreSession.UpdatedAt,
				Matches:   []matchInfo{},
			}

			// Limit to 3 matches per session for display (interface concern)
			matchLimit := 3
			if len(coreSession.Matches) > matchLimit {
				coreSession.Matches = coreSession.Matches[:matchLimit]
			}

			for _, match := range coreSession.Matches {
				result.Matches = append(result.Matches, matchInfo{
					MessageType: "message",
					Snippet:     match.MessageText,
					Sequence:    0,
				})
			}

			results = append(results, result)
		}

		// Limit to 50 sessions for display (interface concern - pagination)
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
				LastCwd:      cs.LastCwd,
				MessageCount: cs.MessageCount,
				UpdatedAt:    cs.UpdatedAt.Format("2006-01-02 15:04:05"),
				CreatedAt:    cs.CreatedAt.Format("2006-01-02 15:04:05"),
			}

			// Check if session's last cwd matches current directory (for highlighting)
			if projectPath != "" && strings.Contains(s.LastCwd, projectPath) {
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
		// Use core function to get session launch info
		session, lastCwd, err := database.GetSessionLaunchInfo(sessionID)
		if err != nil {
			return errMsg{err}
		}

		return sessionLaunchInfoMsg{
			sessionID:   session.SessionID,
			projectPath: session.ProjectPath,
			lastCwd:     lastCwd,
			updatedAt:   session.UpdatedAt.Format("2006-01-02 15:04:05"),
			summary:     session.Summary,
		}
	}
}

func loadSessionDetail(database *db.DB, sessionID string) tea.Cmd {
	return func() tea.Msg {
		// Use core function to get full session detail
		coreDetail, err := database.GetSessionDetail(sessionID)
		if err != nil {
			return errMsg{err}
		}

		// Convert core types to interface types (interface concern - presentation)
		session := sessionItem{
			ID:           coreDetail.SessionID,
			Summary:      coreDetail.Summary,
			Project:      coreDetail.ProjectPath,
			MessageCount: coreDetail.MessageCount,
			UpdatedAt:    coreDetail.UpdatedAt.Format("2006-01-02 15:04:05"),
			CreatedAt:    coreDetail.UpdatedAt.Format("2006-01-02 15:04:05"), // Use UpdatedAt as fallback
		}

		var messages []messageItem
		for _, coreMsg := range coreDetail.Messages {
			messages = append(messages, messageItem{
				Type:      coreMsg.Type,
				Content:   coreMsg.Content,
				Timestamp: coreMsg.Timestamp.Format(time.RFC3339),
			})
		}

		return sessionDetailLoadedMsg{
			detail: sessionDetail{
				Session:   session,
				Messages:  messages,
				LastCwd:   coreDetail.LastCwd,
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

// exportSession performs a quick export to current directory
func exportSession(database *db.DB, sessionID string) tea.Cmd {
	return func() tea.Msg {
		return exportSessionToPath(database, sessionID, "")()
	}
}

// exportSessionToPath exports a session to a specific path (or auto-generates filename if empty)
func exportSessionToPath(database *db.DB, sessionID string, filePath string) tea.Cmd {
	return func() tea.Msg {
		// Get current working directory
		cwd, err := os.Getwd()
		if err != nil {
			return exportCompletedMsg{
				success: false,
				err:     err,
			}
		}

		// If no file path specified, generate default filename in current directory
		if filePath == "" {
			// Generate default filename
			now := filepath.Join(cwd, generateDefaultFilename(sessionID))
			filePath = now
		} else if !filepath.IsAbs(filePath) {
			// Make relative paths absolute to current directory
			filePath = filepath.Join(cwd, filePath)
		}

		// Query session data
		var sessionInternalID int64
		var summary, project, createdAt, updatedAt string
		var messageCount int
		err = database.QueryRow(`
			SELECT
				id,
				COALESCE(summary, ''),
				project_path,
				created_at,
				updated_at,
				(SELECT COUNT(*) FROM messages WHERE session_id = sessions.id) as message_count
			FROM sessions
			WHERE session_id = ?
		`, sessionID).Scan(&sessionInternalID, &summary, &project, &createdAt, &updatedAt, &messageCount)
		if err != nil {
			return exportCompletedMsg{
				success: false,
				err:     err,
			}
		}

		// Get all messages
		rows, err := database.Query(`
			SELECT type, COALESCE(sender, ''), COALESCE(text_content, ''), timestamp, sequence
			FROM messages
			WHERE session_id = ?
			ORDER BY sequence ASC
		`, sessionInternalID)
		if err != nil {
			return exportCompletedMsg{
				success: false,
				err:     err,
			}
		}
		defer rows.Close()

		// Build markdown content
		var b strings.Builder

		// Header
		b.WriteString("# ")
		b.WriteString(summary)
		b.WriteString("\n\n")

		// Metadata
		b.WriteString("**Session ID:** `")
		b.WriteString(sessionID)
		b.WriteString("`  \n")
		b.WriteString("**Project:** `")
		b.WriteString(project)
		b.WriteString("`  \n")
		b.WriteString("**Created:** ")
		b.WriteString(formatTimestampForExport(createdAt))
		b.WriteString("  \n")
		b.WriteString("**Updated:** ")
		b.WriteString(formatTimestampForExport(updatedAt))
		b.WriteString("  \n")
		b.WriteString("**Messages:** ")
		b.WriteString(fmt.Sprintf("%d", messageCount))
		b.WriteString("\n\n")
		b.WriteString("---\n\n")

		// Messages
		for rows.Next() {
			var msgType, sender, content, timestamp string
			var sequence int
			if err := rows.Scan(&msgType, &sender, &content, &timestamp, &sequence); err != nil {
				continue
			}

			// Skip summary entries
			if msgType == "summary" {
				continue
			}

			// Determine label
			label := strings.ToUpper(msgType)
			if sender != "" {
				label = strings.ToUpper(sender)
			}

			// Message header
			b.WriteString("**")
			b.WriteString(label)
			b.WriteString("**")
			b.WriteString(" _")
			b.WriteString(formatTimestampForExport(timestamp))
			b.WriteString("_\n\n")

			// Content (no truncation)
			if content != "" {
				b.WriteString(content)
				b.WriteString("\n\n")
			}

			b.WriteString("---\n\n")
		}

		// Write to file
		if err := os.WriteFile(filePath, []byte(b.String()), 0644); err != nil {
			return exportCompletedMsg{
				success: false,
				err:     err,
			}
		}

		return exportCompletedMsg{
			success:  true,
			filePath: filePath,
		}
	}
}

func generateDefaultFilename(sessionID string) string {
	// Use first 8 chars of session ID for uniqueness
	shortID := sessionID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	return fmt.Sprintf("session-%s.md", shortID)
}

func formatTimestampForExport(ts string) string {
	// Try parsing various formats
	formats := []string{
		"2006-01-02T15:04:05.999Z07:00",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
	}

	for _, format := range formats {
		if t, err := time.Parse(format, ts); err == nil {
			return t.Format("Jan 02, 2006 15:04:05")
		}
	}

	// If parsing fails, return as-is
	return ts
}
