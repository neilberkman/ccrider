package cli

import (
	"database/sql"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/neilberkman/ccrider/internal/core/db"
)

var statsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Show database statistics",
	Long: `Display comprehensive statistics about the ccrider database.

Shows session counts, message counts, tool usage, date ranges, and storage info.`,
	RunE: runStats,
}

func init() {
	rootCmd.AddCommand(statsCmd)
}

func runStats(cmd *cobra.Command, args []string) error {
	// Open database
	database, err := db.New(dbPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer func() {
		_ = database.Close()
	}()

	fmt.Println("Database Statistics")
	fmt.Println("===================")
	fmt.Println()

	// Total sessions
	var totalSessions int
	err = database.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&totalSessions)
	if err != nil {
		return fmt.Errorf("failed to count sessions: %w", err)
	}
	fmt.Printf("Total Sessions:    %d\n", totalSessions)

	// Total messages
	var totalMessages int
	err = database.QueryRow("SELECT COUNT(*) FROM messages").Scan(&totalMessages)
	if err != nil {
		return fmt.Errorf("failed to count messages: %w", err)
	}
	fmt.Printf("Total Messages:    %d\n", totalMessages)

	// Total tool uses
	var totalToolUses int
	err = database.QueryRow("SELECT COUNT(*) FROM tool_uses").Scan(&totalToolUses)
	if err != nil {
		return fmt.Errorf("failed to count tool uses: %w", err)
	}
	fmt.Printf("Total Tool Uses:   %d\n", totalToolUses)

	fmt.Println()

	// Date range (only if we have sessions)
	if totalSessions > 0 {
		var minCreated, maxUpdated sql.NullString
		err = database.QueryRow("SELECT MIN(created_at), MAX(updated_at) FROM sessions").Scan(&minCreated, &maxUpdated)
		if err != nil {
			return fmt.Errorf("failed to get date range: %w", err)
		}

		if minCreated.Valid {
			if t := parseTimestamp(minCreated.String); !t.IsZero() {
				fmt.Printf("Oldest Session:    %s\n", t.Format("Jan 2, 2006 3:04 PM"))
			}
		}

		if maxUpdated.Valid {
			if t := parseTimestamp(maxUpdated.String); !t.IsZero() {
				fmt.Printf("Newest Session:    %s\n", t.Format("Jan 2, 2006 3:04 PM"))
			}
		}

		fmt.Println()

		// Most active project
		var mostActiveProject sql.NullString
		var mostActiveCount int
		err = database.QueryRow(`
			SELECT project_path, COUNT(*) as count
			FROM sessions
			GROUP BY project_path
			ORDER BY count DESC
			LIMIT 1
		`).Scan(&mostActiveProject, &mostActiveCount)

		if err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("failed to get most active project: %w", err)
		}

		if mostActiveProject.Valid {
			fmt.Printf("Most Active Project:\n")
			fmt.Printf("  Path:     %s\n", mostActiveProject.String)
			fmt.Printf("  Sessions: %d\n", mostActiveCount)
			fmt.Println()
		}
	}

	// Database file size
	fileInfo, err := os.Stat(dbPath)
	if err != nil {
		return fmt.Errorf("failed to stat database file: %w", err)
	}

	fmt.Printf("Database Location: %s\n", dbPath)
	fmt.Printf("Database Size:     %s\n", formatBytes(fileInfo.Size()))

	return nil
}

// formatBytes formats bytes into human-readable format
func formatBytes(bytes int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)

	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.2f GB", float64(bytes)/float64(GB))
	case bytes >= MB:
		return fmt.Sprintf("%.2f MB", float64(bytes)/float64(MB))
	case bytes >= KB:
		return fmt.Sprintf("%.2f KB", float64(bytes)/float64(KB))
	default:
		return fmt.Sprintf("%d bytes", bytes)
	}
}

// parseTimestamp attempts to parse timestamps from various formats
func parseTimestamp(s string) time.Time {
	// Try common formats
	formats := []string{
		time.RFC3339,
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999999999 -0700 MST", // Go default format
		"2006-01-02 15:04:05",
	}

	for _, format := range formats {
		if t, err := time.Parse(format, s); err == nil {
			return t
		}
	}

	return time.Time{}
}
