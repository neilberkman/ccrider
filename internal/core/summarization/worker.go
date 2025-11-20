package summarization

import (
	"context"
	"fmt"
	"time"

	"github.com/neilberkman/ccrider/internal/core/db"
	"github.com/neilberkman/ccrider/internal/core/llm"
)

// Worker handles background summarization of sessions
type Worker struct {
	db         *db.DB
	summarizer *llm.Summarizer
	interval   time.Duration
}

// NewWorker creates a new background summarization worker
func NewWorker(database *db.DB, summarizer *llm.Summarizer, interval time.Duration) *Worker {
	return &Worker{
		db:         database,
		summarizer: summarizer,
		interval:   interval,
	}
}

// Start begins the background summarization loop
func (w *Worker) Start(ctx context.Context) error {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	// Initial pass
	fmt.Println("Starting background summarization worker...")
	if err := w.ProcessStale(ctx); err != nil {
		fmt.Printf("Initial summarization error: %v\n", err)
	}

	for {
		select {
		case <-ctx.Done():
			fmt.Println("Shutting down summarization worker...")
			return ctx.Err()
		case <-ticker.C:
			if err := w.ProcessStale(ctx); err != nil {
				fmt.Printf("Summarization error: %v\n", err)
			}
		}
	}
}

// ProcessStale processes all sessions that need summarization
func (w *Worker) ProcessStale(ctx context.Context) error {
	// Get unsummarized sessions (limit to 10 per batch)
	sessionIDs, err := w.db.ListUnsummarizedSessions(10)
	if err != nil {
		return fmt.Errorf("list unsummarized: %w", err)
	}

	if len(sessionIDs) == 0 {
		return nil
	}

	fmt.Printf("[%s] Summarizing %d session(s)...\n", time.Now().Format("15:04:05"), len(sessionIDs))

	successCount := 0
	errorCount := 0

	for i, sessionID := range sessionIDs {
		// Load session and messages
		session, messages, err := w.db.LoadSessionForSummarization(sessionID)
		if err != nil {
			fmt.Printf("  [%d/%d] ❌ Failed to load session: %v\n", i+1, len(sessionIDs), err)
			errorCount++
			continue
		}

		// Skip sessions with no messages
		if len(messages) == 0 {
			continue
		}

		fmt.Printf("  [%d/%d] Session %s (%d messages)... ", i+1, len(sessionIDs), session.SessionID, len(messages))

		// Generate summary
		summary, err := w.summarizer.SummarizeSession(ctx, session, messages)
		if err != nil {
			fmt.Printf("❌ Failed: %v\n", err)
			errorCount++
			continue
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
		err = w.db.SaveSummary(
			summary.SessionID,
			summary.FullSummary,
			summary.Version,
			summary.MessageCount,
			tokensApprox,
			dbChunks,
		)
		if err != nil {
			fmt.Printf("❌ Failed to save: %v\n", err)
			errorCount++
			continue
		}

		fmt.Printf("✓\n")
		successCount++
	}

	if successCount > 0 || errorCount > 0 {
		fmt.Printf("[%s] Summary: %d succeeded, %d failed\n", time.Now().Format("15:04:05"), successCount, errorCount)
	}

	return nil
}

// GetStats returns statistics about summarization progress
func (w *Worker) GetStats() (total, summarized, pending int, err error) {
	return w.db.GetSummarizationStats()
}
