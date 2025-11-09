package cli

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/yourusername/ccrider/internal/core/db"
)

var (
	listLimit   int
	listProject string
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List Claude Code sessions",
	Long: `List all imported Claude Code sessions in reverse chronological order.

Shows session summaries, project paths, message counts, and timestamps.

Examples:
  ccrider list
  ccrider list --limit 10
  ccrider list --project /path/to/project`,
	RunE: runList,
}

func init() {
	rootCmd.AddCommand(listCmd)
	listCmd.Flags().IntVar(&listLimit, "limit", 20, "Maximum number of sessions to display")
	listCmd.Flags().StringVar(&listProject, "project", "", "Filter by project path")
}

func runList(cmd *cobra.Command, args []string) error {
	// Open database
	database, err := db.New(dbPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer func() {
		_ = database.Close()
	}()

	// Build query
	query := `SELECT session_id, summary, project_path, message_count, updated_at, created_at
	          FROM sessions`
	var queryArgs []interface{}

	if listProject != "" {
		query += " WHERE project_path = ?"
		queryArgs = append(queryArgs, listProject)
	}

	query += " ORDER BY updated_at DESC LIMIT ?"
	queryArgs = append(queryArgs, listLimit)

	// Execute query
	rows, err := database.Query(query, queryArgs...)
	if err != nil {
		return fmt.Errorf("failed to query sessions: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	// Process results
	sessions := []sessionInfo{}
	for rows.Next() {
		var s sessionInfo
		var updatedAt, createdAt sql.NullString

		err := rows.Scan(&s.sessionID, &s.summary, &s.projectPath, &s.messageCount, &updatedAt, &createdAt)
		if err != nil {
			return fmt.Errorf("failed to scan row: %w", err)
		}

		// Parse timestamps
		if updatedAt.Valid {
			if t, err := time.Parse(time.RFC3339, updatedAt.String); err == nil {
				s.updatedAt = t
			}
		}
		if createdAt.Valid {
			if t, err := time.Parse(time.RFC3339, createdAt.String); err == nil {
				s.createdAt = t
			}
		}

		sessions = append(sessions, s)
	}

	if err = rows.Err(); err != nil {
		return fmt.Errorf("error iterating rows: %w", err)
	}

	// Display results
	if len(sessions) == 0 {
		if listProject != "" {
			fmt.Printf("No sessions found for project: %s\n", listProject)
		} else {
			fmt.Println("No sessions found. Run 'ccrider sync' to import sessions.")
		}
		return nil
	}

	fmt.Printf("Showing %d session(s)", len(sessions))
	if listProject != "" {
		fmt.Printf(" for project: %s", listProject)
	}
	fmt.Println()
	fmt.Println()

	for i, s := range sessions {
		fmt.Printf("[%d] %s\n", i+1, s.sessionID)
		if s.summary.Valid && s.summary.String != "" {
			summary := truncateSummary(s.summary.String, 80)
			fmt.Printf("    Summary: %s\n", summary)
		}
		fmt.Printf("    Project: %s\n", s.projectPath)
		fmt.Printf("    Messages: %d\n", s.messageCount)
		if !s.updatedAt.IsZero() {
			fmt.Printf("    Updated: %s\n", formatTimestamp(s.updatedAt))
		}
		if !s.createdAt.IsZero() {
			fmt.Printf("    Created: %s\n", formatTimestamp(s.createdAt))
		}
		fmt.Println()
	}

	return nil
}

type sessionInfo struct {
	sessionID    string
	summary      sql.NullString
	projectPath  string
	messageCount int
	updatedAt    time.Time
	createdAt    time.Time
}

// truncateSummary truncates long summaries for display
func truncateSummary(summary string, maxLen int) string {
	// Remove newlines and excessive whitespace
	summary = strings.ReplaceAll(summary, "\n", " ")
	summary = strings.Join(strings.Fields(summary), " ")

	if len(summary) <= maxLen {
		return summary
	}

	// Find a good break point (end of word)
	truncated := summary[:maxLen]
	lastSpace := strings.LastIndex(truncated, " ")
	if lastSpace > maxLen-20 {
		truncated = truncated[:lastSpace]
	}

	return truncated + "..."
}

// formatTimestamp formats a timestamp in a human-friendly way
func formatTimestamp(t time.Time) string {
	now := time.Now()
	diff := now.Sub(t)

	// Less than a minute
	if diff < time.Minute {
		return "just now"
	}

	// Less than an hour
	if diff < time.Hour {
		mins := int(diff.Minutes())
		if mins == 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", mins)
	}

	// Less than a day
	if diff < 24*time.Hour {
		hours := int(diff.Hours())
		if hours == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", hours)
	}

	// Less than a week
	if diff < 7*24*time.Hour {
		days := int(diff.Hours() / 24)
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	}

	// Less than a month
	if diff < 30*24*time.Hour {
		weeks := int(diff.Hours() / 24 / 7)
		if weeks == 1 {
			return "1 week ago"
		}
		return fmt.Sprintf("%d weeks ago", weeks)
	}

	// Show formatted date
	if t.Year() == now.Year() {
		return t.Format("Jan 2")
	}

	return t.Format("Jan 2, 2006")
}
