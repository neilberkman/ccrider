package daemon

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/neilberkman/ccrider/internal/core/db"
	"github.com/neilberkman/ccrider/internal/core/importer"
	"github.com/neilberkman/ccrider/internal/core/llm"
	"github.com/neilberkman/ccrider/pkg/ccsessions"
)

// UnifiedDaemon handles file watching, auto-sync, and auto-summarization
type UnifiedDaemon struct {
	database   *db.DB
	llm        *llm.LLM
	summarizer *llm.Summarizer
	watcher    *fsnotify.Watcher
	watchPath  string
	modelName  string
	stats      *DaemonStats
	aggressive bool   // Aggressive backfill mode
	pauseFile  string // Path to pause state file
}

// DaemonStats tracks daemon activity
type DaemonStats struct {
	StartTime        time.Time
	SessionsSynced   int
	SessionsSummarized int
	LastSync         time.Time
	LastSummary      time.Time
	Errors           int
}

// NewUnifiedDaemon creates a new unified daemon
func NewUnifiedDaemon(database *db.DB, inference *llm.LLM, watchPath string, modelName string, aggressive bool) (*UnifiedDaemon, error) {
	// Create file watcher
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to create file watcher: %w", err)
	}

	// Verify watch path exists
	if _, err := os.Stat(watchPath); err != nil {
		watcher.Close()
		return nil, fmt.Errorf("watch path does not exist: %s", watchPath)
	}

	// Get pause file path
	home, err := os.UserHomeDir()
	if err != nil {
		watcher.Close()
		return nil, fmt.Errorf("failed to get home dir: %w", err)
	}
	pauseFile := filepath.Join(home, ".config", "ccrider", "daemon", "paused")

	return &UnifiedDaemon{
		database:   database,
		llm:        inference,
		summarizer: llm.NewSummarizer(inference),
		watcher:    watcher,
		watchPath:  watchPath,
		modelName:  modelName,
		aggressive: aggressive,
		pauseFile:  pauseFile,
		stats: &DaemonStats{
			StartTime: time.Now(),
		},
	}, nil
}

// Start runs the unified daemon
func (d *UnifiedDaemon) Start(ctx context.Context) error {
	log.Printf("Unified daemon starting...")
	log.Printf("  Watching: %s", d.watchPath)
	log.Printf("  LLM ready for summarization")

	// Add watch for all project directories
	if err := d.setupWatches(); err != nil {
		return fmt.Errorf("failed to setup watches: %w", err)
	}

	// Do initial sync
	log.Printf("Performing initial sync...")
	if err := d.syncAll(); err != nil {
		log.Printf("Warning: initial sync failed: %v", err)
	} else {
		log.Printf("Initial sync complete")
	}

	// Auto-summarize any unsummarized sessions
	go d.summarizeUnsummarized(ctx)

	// Watch for file changes
	for {
		select {
		case <-ctx.Done():
			log.Printf("Daemon shutting down gracefully...")
			d.watcher.Close()
			return nil

		case event, ok := <-d.watcher.Events:
			if !ok {
				return fmt.Errorf("watcher closed unexpectedly")
			}

			if d.shouldProcessEvent(event) {
				log.Printf("File event: %s %s", event.Op, event.Name)
				d.handleFileEvent(ctx, event)
			}

		case err, ok := <-d.watcher.Errors:
			if !ok {
				return fmt.Errorf("watcher error channel closed")
			}
			log.Printf("Watcher error: %v", err)
			d.stats.Errors++
		}
	}
}

// setupWatches adds watches for all project directories
func (d *UnifiedDaemon) setupWatches() error {
	// Walk the projects directory and watch all subdirectories
	return filepath.Walk(d.watchPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Watch directories only
		if info.IsDir() {
			log.Printf("Watching: %s", path)
			if err := d.watcher.Add(path); err != nil {
				return fmt.Errorf("failed to watch %s: %w", path, err)
			}
		}

		return nil
	})
}

// shouldProcessEvent determines if we should process this file event
func (d *UnifiedDaemon) shouldProcessEvent(event fsnotify.Event) bool {
	// Only care about .jsonl files
	if !strings.HasSuffix(event.Name, ".jsonl") {
		return false
	}

	// Process writes and creates
	return event.Op&fsnotify.Write == fsnotify.Write ||
	       event.Op&fsnotify.Create == fsnotify.Create
}

// handleFileEvent processes a file change event
func (d *UnifiedDaemon) handleFileEvent(ctx context.Context, event fsnotify.Event) {
	// Give the file a moment to finish writing
	time.Sleep(100 * time.Millisecond)

	// Sync this specific session
	if err := d.syncSession(event.Name); err != nil {
		log.Printf("Error syncing %s: %v", event.Name, err)
		d.stats.Errors++
		return
	}

	d.stats.SessionsSynced++
	d.stats.LastSync = time.Now()

	// Auto-summarize the session
	if err := d.summarizeSession(ctx, event.Name); err != nil {
		log.Printf("Error summarizing %s: %v", event.Name, err)
		d.stats.Errors++
		return
	}

	d.stats.SessionsSummarized++
	d.stats.LastSummary = time.Now()
	log.Printf("✓ Synced and summarized: %s", filepath.Base(event.Name))
}

// syncSession syncs a single session file
func (d *UnifiedDaemon) syncSession(filePath string) error {
	// Parse the session file
	session, err := ccsessions.ParseFile(filePath)
	if err != nil {
		return fmt.Errorf("parse failed: %w", err)
	}

	imp := importer.New(d.database)

	// Import with existingMessageCount=0 (will be handled by importer's deduplication)
	err = imp.ImportSession(session, 0)
	if err != nil {
		return fmt.Errorf("import failed: %w", err)
	}

	return nil
}

// syncAll performs a full sync of all sessions
func (d *UnifiedDaemon) syncAll() error {
	imp := importer.New(d.database)

	// nil progress callback for silent background sync
	err := imp.ImportDirectory(d.watchPath, nil)
	if err != nil {
		return err
	}

	// Update stats (we don't track exact count from ImportDirectory, but it succeeded)
	d.stats.LastSync = time.Now()

	return nil
}

// summarizeSession summarizes a single session
func (d *UnifiedDaemon) summarizeSession(ctx context.Context, filePath string) error {
	// Extract session ID from filename
	sessionID := strings.TrimSuffix(filepath.Base(filePath), ".jsonl")

	// Get session from database
	var id int64
	err := d.database.QueryRow(`SELECT id FROM sessions WHERE session_id = ?`, sessionID).Scan(&id)
	if err != nil {
		return fmt.Errorf("session not found: %w", err)
	}

	// Check if already summarized
	var existingSummary string
	err = d.database.QueryRow(`SELECT summary_text FROM session_summaries WHERE session_id = ?`, sessionID).Scan(&existingSummary)
	if err == nil && existingSummary != "" {
		// Already summarized, skip
		return nil
	}

	// Load session and messages
	session, messages, err := d.database.LoadSessionForSummarization(id)
	if err != nil {
		return fmt.Errorf("failed to load session: %w", err)
	}

	// Generate summary
	summary, err := d.summarizer.SummarizeSession(ctx, session, messages)
	if err != nil {
		return fmt.Errorf("summarization failed: %w", err)
	}

	// Convert llm.ChunkSummary to db.ChunkSummary
	dbChunks := make([]db.ChunkSummary, len(summary.ChunkSummaries))
	for i, chunk := range summary.ChunkSummaries {
		dbChunks[i] = db.ChunkSummary{
			SessionID:    summary.SessionID,
			ChunkIndex:   chunk.Index,
			MessageRange: chunk.MessageRange,
			Summary:      chunk.Summary,
			TokensApprox: chunk.TokensApprox,
		}
	}

	// Save to database
	tokensApprox := len(summary.FullSummary) / 4
	err = d.database.SaveSummary(
		summary.SessionID,
		summary.OneLiner,
		summary.FullSummary,
		summary.Version,
		summary.MessageCount,
		tokensApprox,
		dbChunks,
		d.modelName,
	)
	if err != nil {
		return fmt.Errorf("failed to save summary: %w", err)
	}

	return nil
}

// summarizeUnsummarized runs periodic summarization of unsummarized sessions
func (d *UnifiedDaemon) summarizeUnsummarized(ctx context.Context) {
	// Wait a bit for initial sync to complete
	time.Sleep(2 * time.Second)

	// Determine backfill interval based on mode
	var interval time.Duration
	if d.aggressive {
		interval = 2 * time.Second // Aggressive: ~30 sessions/minute
		log.Printf("Backfill mode: AGGRESSIVE (2s/session)")
	} else {
		interval = 60 * time.Second // Gentle: ~1 session/minute
		log.Printf("Backfill mode: GENTLE (60s/session)")
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Check if paused
			if d.isPaused() {
				continue // Skip this tick
			}
			d.processSingleSession(ctx)
		}
	}
}

// isPaused checks if daemon is paused (pause file exists)
func (d *UnifiedDaemon) isPaused() bool {
	_, err := os.Stat(d.pauseFile)
	return err == nil
}

// processSingleSession processes one unsummarized session
func (d *UnifiedDaemon) processSingleSession(ctx context.Context) {
	sessionIDs, err := d.database.ListUnsummarizedSessions(1)
	if err != nil {
		log.Printf("Error listing unsummarized sessions: %v", err)
		return
	}

	if len(sessionIDs) == 0 {
		return // No work to do
	}

	sessionID := sessionIDs[0]

	// Load session and messages
	session, messages, err := d.database.LoadSessionForSummarization(sessionID)
	if err != nil {
		log.Printf("Error loading session %d: %v", sessionID, err)
		d.stats.Errors++
		return
	}

	// Skip truly empty sessions (0 messages in DB)
	if len(messages) == 0 {
		log.Printf("Skipping empty session %s (claimed %d messages, actually 0)", session.SessionID, session.MessageCount)
		err := d.database.SaveSummary(
			sessionID,
			"[Empty session]",
			"This session contains no messages.",
			1,
			session.MessageCount, // Use the session's count to prevent re-querying
			0,
			[]db.ChunkSummary{},
			d.modelName,
		)
		if err != nil {
			log.Printf("Failed to save empty session marker for %s: %v", session.SessionID, err)
		}
		return
	}

	// Generate summary
	summary, err := d.summarizer.SummarizeSession(ctx, session, messages)
	if err != nil {
		// This is a bug that needs fixing, not a skip condition
		log.Printf("ERROR: Failed to summarize session %s (%d messages): %v", session.SessionID, len(messages), err)
		d.stats.Errors++
		return
	}

	// Convert llm.ChunkSummary to db.ChunkSummary
	dbChunks := make([]db.ChunkSummary, len(summary.ChunkSummaries))
	for i, chunk := range summary.ChunkSummaries {
		dbChunks[i] = db.ChunkSummary{
			SessionID:    summary.SessionID,
			ChunkIndex:   chunk.Index,
			MessageRange: chunk.MessageRange,
			Summary:      chunk.Summary,
			TokensApprox: chunk.TokensApprox,
		}
	}

	// Save to database
	tokensApprox := len(summary.FullSummary) / 4
	err = d.database.SaveSummary(
		summary.SessionID,
		summary.OneLiner,
		summary.FullSummary,
		summary.Version,
		summary.MessageCount,
		tokensApprox,
		dbChunks,
		d.modelName,
	)
	if err != nil {
		log.Printf("Error saving summary for %s: %v", session.SessionID, err)
		d.stats.Errors++
		return
	}

	d.stats.SessionsSummarized++
	d.stats.LastSummary = time.Now()
	log.Printf("✓ Summarized session %s", session.SessionID)
}

// GetStats returns current daemon statistics
func (d *UnifiedDaemon) GetStats() *DaemonStats {
	return d.stats
}
