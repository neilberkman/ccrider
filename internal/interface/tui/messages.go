package tui

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/neilberkman/ccrider/internal/core/config"
	"github.com/neilberkman/ccrider/internal/core/db"
	"github.com/neilberkman/ccrider/internal/core/importer"
	"github.com/neilberkman/ccrider/internal/core/liveness"
	"github.com/neilberkman/ccrider/internal/core/search"
)

type errMsg struct {
	err error
}

type sessionsLoadedMsg struct {
	sessions         []sessionItem
	generation       uint64
	restoreSelection bool
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

func loadSessions(database *db.DB, filterByProject bool, projectPath string, generation uint64, restoreSelection bool) tea.Cmd {
	return func() tea.Msg {
		return loadSessionsNow(database, filterByProject, projectPath, generation, restoreSelection)
	}
}

func loadSessionsNow(database *db.DB, filterByProject bool, projectPath string, generation uint64, restoreSelection bool) tea.Msg {
	filterPath := ""
	if filterByProject {
		filterPath = projectPath
	}

	coreSessions, err := database.ListSessions(filterPath)
	if err != nil {
		return sessionsLoadFailedMsg{err: err, generation: generation}
	}

	// Best-effort liveness: a failed scan means no badges, never a failed load.
	liveIDs := make(map[string]liveness.LiveSession)
	if live, err := liveness.Scan(context.Background(), liveness.SystemSource{}, database); err == nil {
		liveIDs = liveness.BySessionID(live)
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
		_, s.Live = liveIDs[cs.SessionID]

		if projectPath != "" && strings.Contains(s.LastCwd, projectPath) {
			s.MatchesCurrentDir = true
		}
		sessions = append(sessions, s)
	}

	return sessionsLoadedMsg{sessions: sessions, generation: generation, restoreSelection: restoreSelection}
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
	ch          chan syncEvent
}

type syncFinishedMsg struct {
	warnings []string
}

type syncEvent struct {
	progress *syncProgressMsg
	finished *syncFinishedMsg
}

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
	progressCh := make(chan syncEvent, 100)
	progress := &channelProgressReporter{ch: progressCh}
	var warnings []string
	if !manager.start(func(parent context.Context) {
		imp := importer.New(database)
		cfg, _ := config.Load()
		sources := importer.DefaultSources(cfg.AmpEnabled)

		err := importer.SyncSources(parent, sources, importer.DefaultRemoteSyncTimeout, func(sourceCtx context.Context, src importer.Source) error {
			p, err := imp.PrepareSource(sourceCtx, src)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("%s sync skipped: %v", src.Provider, err))
				return nil
			}
			if p.Warning != nil {
				warnings = append(warnings, fmt.Sprintf("%s sync skipped: %v", p.Provider, p.Warning))
				return nil
			}
			progress.beginSource(p.Provider, p.Total)
			result, err := p.Run(sourceCtx, progress, false)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("%s sync failed: %v", p.Provider, err))
			}
			for index, failure := range result.Failures {
				if index == 5 {
					warnings = append(warnings, fmt.Sprintf("%s: %d additional sessions failed", p.Provider, len(result.Failures)-index))
					break
				}
				warnings = append(warnings, fmt.Sprintf("Cannot import %s session %s: %v", p.Provider, failure.ID, failure.Err))
			}
			if len(result.Deferred) > 0 {
				warnings = append(warnings, fmt.Sprintf("%s sync interrupted; deferred %d changed sessions until the next sync (%s)", p.Provider, len(result.Deferred), deferredIDs(result.Deferred, 5)))
			}
			return nil
		})
		if err != nil && parent.Err() == nil {
			warnings = append(warnings, fmt.Sprintf("sync stopped: %v", err))
		}
	}, func() {
		// The manager is inactive before completion becomes observable, so an
		// immediate follow-up sync cannot be rejected and leave the UI stuck.
		progress.complete(warnings)
		close(progressCh)
	}) {
		return nil
	}
	return syncSubscribe(progressCh)
}

type channelProgressReporter struct {
	total    int
	current  int
	provider string
	lastName string
	ch       chan syncEvent
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
// but keeps the import from stalling on the render loop. One buffer slot stays
// reserved for the completion event so warnings cannot be lost.
func (r *channelProgressReporter) send() {
	msg := syncProgressMsg{
		current:     r.current,
		total:       r.total,
		provider:    r.provider,
		sessionName: r.lastName,
	}
	if len(r.ch) >= cap(r.ch)-1 {
		return
	}
	select {
	case r.ch <- syncEvent{progress: &msg}:
	default:
	}
}

// complete uses the slot reserved by send so the terminal event is guaranteed
// to reach the UI even when it fell behind on progress updates.
func (r *channelProgressReporter) complete(warnings []string) {
	finished := syncFinishedMsg{warnings: warnings}
	r.ch <- syncEvent{finished: &finished}
}

func (r *channelProgressReporter) Finish() {}

// syncSubscribe listens to the progress channel and returns the next message
func syncSubscribe(progressCh chan syncEvent) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-progressCh
		if !ok {
			return syncFinishedMsg{}
		}
		if event.finished != nil {
			return *event.finished
		}
		msg := *event.progress
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
