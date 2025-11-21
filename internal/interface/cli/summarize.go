package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/neilberkman/ccrider/internal/core/daemon"
	"github.com/neilberkman/ccrider/internal/core/db"
	"github.com/neilberkman/ccrider/internal/core/llm"
	"github.com/spf13/cobra"
)

var summarizeCmd = &cobra.Command{
	Use:   "summarize",
	Short: "Summarize sessions using LLM",
	Long: `Generate AI-powered summaries of Claude Code sessions.

Examples:
  ccrider summarize --status           Show summarization statistics
  ccrider summarize --all              Summarize all unsummarized sessions
  ccrider summarize --session abc123   Summarize a specific session
  ccrider summarize --limit 10         Summarize up to 10 sessions`,
	RunE: runSummarize,
}

var (
	summarizeStatus    bool
	summarizeAll       bool
	summarizeDaemon    bool
	summarizeAggressive bool
	summarizeInterval  string
	summarizeSessionID string
	summarizeLimit     int
	summarizeModel     string
)

func init() {
	rootCmd.AddCommand(summarizeCmd)

	summarizeCmd.Flags().BoolVar(&summarizeStatus, "status", false, "Show summarization statistics")
	summarizeCmd.Flags().BoolVar(&summarizeAll, "all", false, "Summarize all unsummarized sessions")
	summarizeCmd.Flags().BoolVar(&summarizeDaemon, "daemon", false, "Run as background daemon")
	summarizeCmd.Flags().BoolVar(&summarizeAggressive, "aggressive", false, "Aggressive backfill mode (~30 sessions/min)")
	summarizeCmd.Flags().StringVar(&summarizeInterval, "interval", "5m", "Interval between daemon runs")
	summarizeCmd.Flags().StringVar(&summarizeSessionID, "session", "", "Summarize specific session by UUID")
	summarizeCmd.Flags().IntVar(&summarizeLimit, "limit", 10, "Maximum number of sessions to summarize")
	summarizeCmd.Flags().StringVar(&summarizeModel, "model", "llama-8b", "Model to use (llama-8b or qwen-1.5b)")
}

func runSummarize(cmd *cobra.Command, args []string) error {
	// Open database
	database, err := db.New(dbPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer database.Close()

	// Handle --status flag
	if summarizeStatus {
		return showSummarizationStatus(database)
	}

	// Handle --daemon flag
	if summarizeDaemon {
		return runSummarizeDaemon(database)
	}

	// Initialize LLM infrastructure
	fmt.Printf("Initializing LLM (%s)...\n", summarizeModel)

	modelManager, err := llm.NewModelManager()
	if err != nil {
		return fmt.Errorf("failed to create model manager: %w", err)
	}

	modelPath, err := modelManager.EnsureModel(summarizeModel)
	if err != nil {
		return fmt.Errorf("failed to ensure model: %w", err)
	}

	inference, err := llm.NewLLM(modelPath, summarizeModel)
	if err != nil {
		return fmt.Errorf("failed to initialize LLM: %w", err)
	}
	defer inference.Close()

	summarizer := llm.NewSummarizer(inference)

	// Determine which sessions to summarize
	var sessionIDs []int64

	if summarizeSessionID != "" {
		// Specific session
		var id int64
		err := database.QueryRow(`SELECT id FROM sessions WHERE session_id = ?`, summarizeSessionID).Scan(&id)
		if err != nil {
			return fmt.Errorf("session not found: %w", err)
		}
		sessionIDs = []int64{id}
	} else {
		// Get unsummarized sessions
		limit := summarizeLimit
		if summarizeAll {
			limit = 10000 // Effectively unlimited
		}

		sessionIDs, err = database.ListUnsummarizedSessions(limit)
		if err != nil {
			return fmt.Errorf("failed to list sessions: %w", err)
		}
	}

	if len(sessionIDs) == 0 {
		fmt.Println("No sessions need summarization.")
		return nil
	}

	fmt.Printf("Summarizing %d session(s)...\n\n", len(sessionIDs))

	// Process each session
	ctx := context.Background()
	successCount := 0
	errorCount := 0

	for i, sessionID := range sessionIDs {
		fmt.Printf("[%d/%d] ", i+1, len(sessionIDs))

		// Load session and messages
		session, messages, err := database.LoadSessionForSummarization(sessionID)
		if err != nil {
			fmt.Printf("❌ Failed to load session: %v\n", err)
			errorCount++
			continue
		}

		fmt.Printf("Session %s (%d messages)... ", session.SessionID, len(messages))

		// Generate summary
		summary, err := summarizer.SummarizeSession(ctx, session, messages)
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
		err = database.SaveSummary(
			summary.SessionID,
			summary.OneLiner,
			summary.FullSummary,
			summary.Version,
			summary.MessageCount,
			tokensApprox,
			dbChunks,
			summarizeModel,
		)
		if err != nil {
			fmt.Printf("❌ Failed to save: %v\n", err)
			errorCount++
			continue
		}

		fmt.Printf("✓\n")
		if len(summary.ChunkSummaries) > 0 {
			fmt.Printf("    Chunks: %d, Tokens: ~%d\n", len(summary.ChunkSummaries), tokensApprox)
		}
		successCount++
	}

	fmt.Printf("\n")
	fmt.Printf("Summary: %d succeeded, %d failed\n", successCount, errorCount)

	return nil
}

func showSummarizationStatus(database *db.DB) error {
	total, summarized, pending, err := database.GetSummarizationStats()
	if err != nil {
		return fmt.Errorf("failed to get stats: %w", err)
	}

	fmt.Println("Summarization Status:")
	fmt.Println("=====================")
	fmt.Printf("Total sessions:      %d\n", total)
	fmt.Printf("Summarized:          %d (%.1f%%)\n", summarized, float64(summarized)/float64(total)*100)
	fmt.Printf("Pending:             %d\n", pending)
	fmt.Println()

	if pending > 0 {
		fmt.Printf("Run 'ccrider summarize --limit %d' to summarize pending sessions.\n", pending)
	} else {
		fmt.Println("All sessions are up to date!")
	}

	return nil
}

func runSummarizeDaemon(database *db.DB) error {
	// Initialize LLM
	fmt.Printf("Initializing LLM (%s) for daemon mode...\n", summarizeModel)

	modelManager, err := llm.NewModelManager()
	if err != nil {
		return fmt.Errorf("failed to create model manager: %w", err)
	}

	modelPath, err := modelManager.EnsureModel(summarizeModel)
	if err != nil {
		return fmt.Errorf("failed to ensure model: %w", err)
	}

	inference, err := llm.NewLLM(modelPath, summarizeModel)
	if err != nil {
		return fmt.Errorf("failed to initialize LLM: %w", err)
	}
	defer inference.Close()

	// Get watch path
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home dir: %w", err)
	}
	watchPath := filepath.Join(home, ".claude", "projects")

	// Create unified daemon
	daemon, err := daemon.NewUnifiedDaemon(database, inference, watchPath, summarizeModel, summarizeAggressive)
	if err != nil {
		return fmt.Errorf("failed to create daemon: %w", err)
	}

	// Setup signal handling for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		fmt.Println("\nReceived shutdown signal...")
		cancel()
	}()

	// Start unified daemon
	fmt.Printf("Daemon started\n")
	fmt.Printf("  Model: %s\n", summarizeModel)
	fmt.Printf("  Watching: %s\n", watchPath)
	fmt.Println("Press Ctrl+C to stop")
	fmt.Println()

	return daemon.Start(ctx)
}
