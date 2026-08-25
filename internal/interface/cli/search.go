package cli

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dustin/go-humanize"
	"github.com/neilberkman/ccrider/internal/core/db"
	"github.com/neilberkman/ccrider/internal/core/search"
	"github.com/spf13/cobra"
)

var (
	searchLimit int
)

var searchCmd = &cobra.Command{
	Use:   "search <query>",
	Short: "Search coding agent sessions using full-text search",
	Long: `Search through all imported coding agent sessions.

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

	// Parse query for filters (project:, after:, before:, date:)
	filters := search.ParseQuery(query)

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
		if session.SessionSummary != "" {
			fmt.Printf("%s\n", session.SessionSummary)
		} else {
			fmt.Printf("[No summary]\n")
		}
		fmt.Printf("ID: %s\n", session.SessionID)
		fmt.Printf("%s | %d msgs | %s | %d matches\n",
			session.LastCwd, session.MessageCount, formatTimeAgo(session.UpdatedAt), len(session.Matches))
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

// wordBreakWindow is how far truncateMessage will look back from the cut for
// a word boundary. Beyond this, snapping back would discard so much of the
// message that a mid-word cut reads better.
const wordBreakWindow = 50

// truncateMessage shortens a message to maxLen bytes for display, preferring
// to break at a word boundary near the cut. The result is always valid UTF-8:
// the cut never lands inside a multi-byte rune. A maxLen at or below zero
// yields an empty string, so a caller working from a narrow terminal cannot
// produce a negative width.
func truncateMessage(msg string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	if len(msg) <= maxLen {
		return msg
	}

	// Back off to a rune boundary so a multi-byte character is never split.
	end := maxLen
	for end > 0 && !utf8.RuneStart(msg[end]) {
		end--
	}
	truncated := msg[:end]

	// Find a good break point (end of word). LastIndexAny returns -1 when the
	// window holds no whitespace, and a break at position 0 would leave
	// nothing but the ellipsis, so both keep the hard cut.
	lastSpace := strings.LastIndexAny(truncated, " \n\t")
	if lastSpace > 0 && lastSpace > len(truncated)-wordBreakWindow {
		truncated = truncated[:lastSpace]
	}

	return truncated + "..."
}

// formatTimeAgo formats a timestamp as relative time (e.g., "2 hours ago")
func formatTimeAgo(t string) string {
	parsed, err := time.Parse(time.RFC3339, t)
	if err != nil {
		// Try without timezone
		parsed, err = time.Parse("2006-01-02 15:04:05", t)
		if err != nil {
			return t
		}
	}
	return humanize.Time(parsed)
}
