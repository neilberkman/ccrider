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
	searchCode  bool
)

var searchCmd = &cobra.Command{
	Use:   "search <query>",
	Short: "Search Claude Code sessions using full-text search",
	Long: `Search through all imported Claude Code sessions.

Uses FTS5 full-text search with different modes:
- Default: Natural language search with porter stemming
- --code: Code search preserving identifiers (camelCase, etc.)

Examples:
  ccrider search "authentication implementation"
  ccrider search "getUserById" --code
  ccrider search "error handling" --limit 10`,
	Args: cobra.MinimumNArgs(1),
	RunE: runSearch,
}

func init() {
	rootCmd.AddCommand(searchCmd)
	searchCmd.Flags().IntVar(&searchLimit, "limit", 50, "Maximum number of results")
	searchCmd.Flags().BoolVar(&searchCode, "code", false, "Use code search (preserves identifiers)")
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

	// Perform search
	var results []search.SearchResult
	if searchCode {
		results, err = search.SearchCode(database, query)
	} else {
		results, err = search.Search(database, query)
	}

	if err != nil {
		return fmt.Errorf("search failed: %w", err)
	}

	// Display results
	if len(results) == 0 {
		fmt.Printf("No results found for: %s\n", query)
		return nil
	}

	fmt.Printf("Found %d result(s) for: %s\n", len(results), query)
	if searchCode {
		fmt.Println("(using code search)")
	}
	fmt.Println()

	for i, r := range results {
		// Limit to searchLimit
		if i >= searchLimit {
			fmt.Printf("\n... and %d more results (use --limit to see more)\n", len(results)-searchLimit)
			break
		}

		fmt.Printf("=== Result %d ===\n", i+1)
		fmt.Printf("Session: %s\n", r.SessionID)
		if r.SessionSummary != "" {
			fmt.Printf("Summary: %s\n", r.SessionSummary)
		}
		fmt.Printf("Project: %s\n", r.ProjectPath)
		fmt.Printf("Time:    %s\n", r.Timestamp)
		fmt.Printf("\nMessage:\n%s\n", truncateMessage(r.MessageText, 300))
		fmt.Println()
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
