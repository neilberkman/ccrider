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
	seq     uint64 // Sequence number to match against current search
}

type exportCompletedMsg struct {
	success  bool
	filePath string
	err      error
}

func performSearch(database *db.DB, query string, seq uint64) tea.Cmd {
	return func() tea.Msg {
		// Parse filters from query using centralized core parser
		filters := search.ParseQuery(query)

		// Call core search with filters
		coreResults, err := search.SearchWithFilters(database, filters)
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

		return searchResultsMsg{results: results, seq: seq}
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
				UpdatedAt:    cs.UpdatedAt.Format(time.RFC3339),
				CreatedAt:    cs.CreatedAt.Format(time.RFC3339),
				Provider:     cs.Provider,
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
			updatedAt:   session.UpdatedAt.Format(time.RFC3339),
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
			UpdatedAt:    coreDetail.UpdatedAt.Format(time.RFC3339),
			CreatedAt:    coreDetail.UpdatedAt.Format(time.RFC3339),
			Provider:     coreDetail.Provider,
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
		sources := importer.DefaultSources()

		// Count total files across all source directories
		var total int
		for _, src := range sources {
			_ = filepath.Walk(src.Path, func(path string, info os.FileInfo, err error) error {
				if err == nil && !info.IsDir() && filepath.Ext(path) == ".jsonl" {
					total++
				}
				return nil
			})
		}

		progressCh := make(chan syncProgressMsg, 100)
		progressCh <- syncProgressMsg{
			current:     0,
			total:       total,
			sessionName: "",
		}

		go func() {
			imp := importer.New(database)
			progress := &channelProgressReporter{
				total:   total,
				current: 0,
				ch:      progressCh,
			}

			for _, src := range sources {
				if _, err := imp.ImportDirectory(src.Path, progress, false, src.SkipSubagents, src.ParseFn, src.Provider); err != nil {
					fmt.Fprintf(os.Stderr, "WARN: %s sync failed: %v\n", src.Provider, err)
				}
			}

			close(progressCh)
		}()

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

