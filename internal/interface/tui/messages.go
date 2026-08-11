package tui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/neilberkman/ccrider/internal/core/config"
	"github.com/neilberkman/ccrider/internal/core/db"
	"github.com/neilberkman/ccrider/internal/core/importer"
	"github.com/neilberkman/ccrider/internal/core/search"
)

type errMsg struct {
	err error
}

type sessionsLoadedMsg struct {
	sessions   []sessionItem
	generation uint64
}

type sessionsLoadFailedMsg struct {
	err        error
	generation uint64
}

type sessionDetailLoadedMsg struct {
	detail sessionDetail
}

type sessionLaunchInfoMsg struct {
	provider    string
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

func loadSessions(database *db.DB, filterByProject bool, projectPath string, generation uint64) tea.Cmd {
	return func() tea.Msg {
		return loadSessionsNow(database, filterByProject, projectPath, generation)
	}
}

func loadSessionsNow(database *db.DB, filterByProject bool, projectPath string, generation uint64) tea.Msg {
	filterPath := ""
	if filterByProject {
		filterPath = projectPath
	}

	coreSessions, err := database.ListSessions(filterPath)
	if err != nil {
		return sessionsLoadFailedMsg{err: err, generation: generation}
	}

	var sessions []sessionItem
	for _, cs := range coreSessions {
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

		if projectPath != "" && strings.Contains(s.LastCwd, projectPath) {
			s.MatchesCurrentDir = true
		}
		sessions = append(sessions, s)
	}

	return sessionsLoadedMsg{sessions: sessions, generation: generation}
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
			provider:    session.Provider,
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
	current     int
	total       int
	provider    string
	sessionName string
	ch          chan syncProgressMsg
}

type syncFinishedMsg struct{}

type syncManager struct {
	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex
	wg     sync.WaitGroup
	active bool
	closed bool
}

func newSyncManager(parent context.Context) *syncManager {
	ctx, cancel := context.WithCancel(parent)
	return &syncManager{ctx: ctx, cancel: cancel}
}

func (m *syncManager) start(work func(context.Context), onDone func()) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active || m.closed {
		return false
	}
	m.active = true
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		work(m.ctx)
		m.mu.Lock()
		m.active = false
		m.mu.Unlock()
		if onDone != nil {
			onDone()
		}
	}()
	return true
}

func (m *syncManager) close() {
	m.mu.Lock()
	m.closed = true
	m.cancel()
	m.mu.Unlock()
	m.wg.Wait()
}

// startSyncWithProgress starts one tracked sync and listens for its progress.
func startSyncWithProgress(manager *syncManager, database *db.DB) tea.Cmd {
	progressCh := make(chan syncProgressMsg, 100)
	if !manager.start(func(parent context.Context) {
		imp := importer.New(database)
		progress := &channelProgressReporter{ch: progressCh}
		cfg, _ := config.Load()
		sources := importer.DefaultSources(cfg.AmpEnabled)

		_ = importer.SyncSources(parent, sources, importer.DefaultRemoteSyncTimeout, func(sourceCtx context.Context, src importer.Source) error {
			p, err := imp.PrepareSource(sourceCtx, src)
			if err != nil {
				fmt.Fprintf(os.Stderr, "WARN: %s sync skipped: %v\n", src.Provider, err)
				return nil
			}
			if p.Warning != nil {
				fmt.Fprintf(os.Stderr, "WARN: %s sync skipped: %v\n", p.Provider, p.Warning)
				return nil
			}
			progress.beginSource(p.Provider, p.Total)
			result, err := p.Run(sourceCtx, progress, false)
			if err != nil {
				fmt.Fprintf(os.Stderr, "WARN: %s sync failed: %v\n", p.Provider, err)
			}
			for _, failure := range result.Failures {
				fmt.Fprintf(os.Stderr, "WARN: Cannot import %s session %s: %v\n", p.Provider, failure.ID, failure.Err)
			}
			if len(result.Deferred) > 0 {
				fmt.Fprintf(os.Stderr, "WARN: %s sync interrupted; deferred %d changed sessions until the next sync (%s)\n", p.Provider, len(result.Deferred), deferredIDs(result.Deferred, 5))
			}
			return nil
		})
	}, func() { close(progressCh) }) {
		return nil
	}
	return syncSubscribe(progressCh)
}

type channelProgressReporter struct {
	total    int
	current  int
	provider string
	lastName string
	ch       chan syncProgressMsg
}

func (r *channelProgressReporter) Update(sessionSummary string, firstMsg string) {
	r.current++
	r.lastName = sessionSummary
	r.send()
}

// Skip advances the counter for an unchanged file so the bar tracks the walk
// rather than freezing at 0% through thousands of skipped files.
func (r *channelProgressReporter) Skip() {
	r.current++
	r.send()
}

func (r *channelProgressReporter) beginSource(provider string, total int) {
	r.provider = provider
	r.total = total
	r.current = 0
	r.lastName = ""
	r.send()
}

// send publishes progress without blocking: every message carries the absolute
// counter, so dropping intermediate updates when the UI is behind costs nothing
// but keeps the import from stalling on the render loop.
func (r *channelProgressReporter) send() {
	select {
	case r.ch <- syncProgressMsg{
		current:     r.current,
		total:       r.total,
		provider:    r.provider,
		sessionName: r.lastName,
	}:
	default:
	}
}

func (r *channelProgressReporter) Finish() {}

// syncSubscribe listens to the progress channel and returns the next message
func syncSubscribe(progressCh chan syncProgressMsg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-progressCh
		if !ok {
			return syncFinishedMsg{}
		}
		msg.ch = progressCh
		return msg
	}
}

func syncSessions(manager *syncManager, database *db.DB) tea.Cmd {
	return startSyncWithProgress(manager, database)
}

func deferredIDs(ids []string, limit int) string {
	shown := ids
	suffix := ""
	if len(shown) > limit {
		shown = shown[:limit]
		suffix = fmt.Sprintf(", and %d more", len(ids)-limit)
	}
	return strings.Join(shown, ", ") + suffix
}
