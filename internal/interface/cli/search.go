package cli

import (
	"fmt"
	"strings"

	"github.com/neilberkman/ccrider/internal/core/db"
	"github.com/neilberkman/ccrider/internal/core/search"
	"github.com/spf13/cobra"
)

var (
	searchLimit int
)

var searchCmd = &cobra.Command{
	Use:   "search <query>",
	Short: "Search Claude Code sessions using full-text search",
	Long: `Search through all imported Claude Code sessions.

Uses FTS5 full-text search with porter stemming for natural language.
Results are grouped by session and show matching message snippets.

Examples:
  ccrider search "authentication implementation"
  ccrider search "ENA-7030"
  ccrider search "error handling" --limit 10`,
	Args: cobra.MinimumNArgs(1),
	RunE: runSearch,
}

func init() {
	rootCmd.AddCommand(searchCmd)
	searchCmd.Flags().IntVar(&searchLimit, "limit", 50, "Maximum number of sessions to show")
}

func runSearch(cmd *cobra.Command, args []string) error {
	// Join all args as query
	query := strings.Join(args, " ")

	// Open database
	database, err := db.New(dbPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer func() {
		_ = database.Close()
	}()

	// Use unified search backend (same as TUI/MCP)
	filters := search.SearchFilters{
		Query: query,
		// CLI doesn't support project/date filters yet, but using unified backend
		// means we can add them easily later
	}

	sessionResults, err := search.SearchWithFilters(database, filters)
	if err != nil {
		return fmt.Errorf("search failed: %w", err)
	}

	// Display results grouped by session
	if len(sessionResults) == 0 {
		fmt.Printf("No results found for: %s\n", query)
		return nil
	}

	// Count total matches across all sessions
	totalMatches := 0
	for _, s := range sessionResults {
		totalMatches += len(s.Matches)
	}

	fmt.Printf("Found %d session(s) with %d match(es) for: %s\n", len(sessionResults), totalMatches, query)
	fmt.Println()

	sessionCount := 0
	for _, session := range sessionResults {
		// Limit to searchLimit sessions
		if sessionCount >= searchLimit {
			fmt.Printf("\n... and %d more sessions (use --limit to see more)\n", len(sessionResults)-searchLimit)
			break
		}
		sessionCount++

		fmt.Printf("=== Session %d ===\n", sessionCount)
		fmt.Printf("ID:      %s\n", session.SessionID)
		if session.SessionSummary != "" {
			fmt.Printf("Summary: %s\n", session.SessionSummary)
		} else {
			fmt.Printf("Summary: [No summary - showing first match]\n")
		}
		fmt.Printf("Project: %s\n", session.ProjectPath)
		fmt.Printf("Updated: %s\n", session.UpdatedAt)
		fmt.Printf("Matches: %d\n", len(session.Matches))
		fmt.Println()

		// Show up to 3 matches per session
		matchLimit := 3
		if len(session.Matches) > matchLimit {
			fmt.Printf("Showing first %d of %d matches:\n", matchLimit, len(session.Matches))
		}
		for i, match := range session.Matches {
			if i >= matchLimit {
				break
			}
			fmt.Printf("  Match %d:\n", i+1)
			fmt.Printf("  %s\n", truncateMessage(match.MessageText, 200))
			fmt.Println()
		}
	}

	return nil
}

// truncateMessage truncates long messages for display
func truncateMessage(msg string, maxLen int) string {
	if len(msg) <= maxLen {
		return msg
	}

	// Find a good break point (end of word)
	truncated := msg[:maxLen]
	lastSpace := strings.LastIndexAny(truncated, " \n\t")
	if lastSpace > maxLen-50 {
		truncated = truncated[:lastSpace]
	}

	return truncated + "..."
}
