package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/neilberkman/ccrider/internal/core/db"
	"github.com/neilberkman/ccrider/internal/core/llm"
	"github.com/neilberkman/ccrider/internal/core/search"
	"github.com/spf13/cobra"
)

var resumeCmd = &cobra.Command{
	Use:   "resume [query]",
	Short: "Find and resume a Claude Code session",
	Long: `Use AI-powered search to find relevant sessions and resume them quickly.

The search uses a three-tier approach:
1. Exact match (issue ID, file path)
2. Keyword search (FTS5)
3. AI-powered search (LLM over summaries)

Examples:
  ccrider resume "postgres migration bug"
  ccrider resume "ena-6530"
  ccrider resume "authentication fixes"
  ccrider resume "schema.go" --auto`,
	Args: cobra.MinimumNArgs(1),
	RunE: runResume,
}

var (
	resumeAuto  bool
	resumeModel string
)

func init() {
	rootCmd.AddCommand(resumeCmd)

	resumeCmd.Flags().BoolVar(&resumeAuto, "auto", false, "Auto-select best match without prompt")
	resumeCmd.Flags().StringVar(&resumeModel, "model", "llama-8b", "Model to use for AI search")
}

func runResume(cmd *cobra.Command, args []string) error {
	query := strings.Join(args, " ")

	// Open database
	database, err := db.New(dbPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer database.Close()

	// Initialize LLM (may not be needed if exact/FTS5 match succeeds)
	var inference *llm.LLM
	var modelManager *llm.ModelManager

	// Create searcher (without LLM for now)
	searcher := search.NewSmartSearcher(database, nil)

	// Perform search
	fmt.Printf("Searching for: \"%s\"\n", query)

	ctx := context.Background()
	results, err := searcher.Search(ctx, query)

	// If search failed and it's not exact/FTS5, try with LLM
	if err != nil && strings.Contains(err.Error(), "LLM not initialized") {
		fmt.Println("Initializing AI search...")

		modelManager, err = llm.NewModelManager()
		if err != nil {
			return fmt.Errorf("failed to create model manager: %w", err)
		}

		modelPath, err := modelManager.EnsureModel(resumeModel)
		if err != nil {
			return fmt.Errorf("failed to ensure model: %w", err)
		}

		inference, err = llm.NewLLM(modelPath, resumeModel)
		if err != nil {
			return fmt.Errorf("failed to initialize LLM: %w", err)
		}
		defer inference.Close()

		// Create new searcher with LLM
		searcher = search.NewSmartSearcher(database, inference)

		// Retry search
		results, err = searcher.Search(ctx, query)
		if err != nil {
			return fmt.Errorf("search failed: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("search failed: %w", err)
	}

	if len(results) == 0 {
		fmt.Println("No matching sessions found.")
		return nil
	}

	// Display results
	fmt.Printf("\nFound %d session(s) [method: %s]:\n\n", len(results), results[0].Method)

	displayCount := len(results)
	if displayCount > 5 {
		displayCount = 5
	}

	for i := 0; i < displayCount; i++ {
		r := results[i]
		fmt.Printf("%d. %s\n", i+1, r.Session.SessionID)
		fmt.Printf("   Project: %s\n", r.Session.ProjectPath)

		if r.Session.Summary != "" {
			summary := r.Session.Summary
			if len(summary) > 150 {
				summary = summary[:147] + "..."
			}
			// Clean up summary for display
			summary = strings.ReplaceAll(summary, "\n", " ")
			fmt.Printf("   Summary: %s\n", summary)
		}

		if r.Relevance < 1.0 {
			fmt.Printf("   Relevance: %.0f%%\n", r.Relevance*100)
		}

		fmt.Println()
	}

	// Select session
	var selection int
	if resumeAuto {
		selection = 1
		fmt.Printf("Auto-selecting: %s\n", results[0].Session.SessionID)
	} else {
		fmt.Printf("Select session to resume (1-%d, 0 to cancel): ", displayCount)
		_, err := fmt.Scanf("%d", &selection)
		if err != nil || selection < 1 || selection > displayCount {
			fmt.Println("Cancelled.")
			return nil
		}
	}

	// Get session launch info
	selectedSession := results[selection-1].Session
	sessionInfo, lastCwd, err := database.GetSessionLaunchInfo(selectedSession.SessionID)
	if err != nil {
		return fmt.Errorf("failed to get session info: %w", err)
	}

	// Launch Claude Code with resume
	fmt.Printf("\nResuming session %s...\n", selectedSession.SessionID)
	return launchClaude(sessionInfo.SessionID, lastCwd)
}

func launchClaude(sessionID, workingDir string) error {
	// Build command
	cmd := exec.Command("claude", "code", "--resume", sessionID)

	// Set working directory if available
	if workingDir != "" {
		cmd.Dir = workingDir
	}

	// Connect to stdio
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Run
	return cmd.Run()
}
