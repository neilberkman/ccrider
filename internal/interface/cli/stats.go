package cli

import (
	"fmt"
	"os"

	"github.com/neilberkman/ccrider/internal/core/db"
	"github.com/spf13/cobra"
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

	// Use core function to get stats
	stats, err := database.GetStats()
	if err != nil {
		return fmt.Errorf("failed to get stats: %w", err)
	}

	// Interface concern: Format and display stats
	fmt.Println("Database Statistics")
	fmt.Println("===================")
	fmt.Println()

	fmt.Printf("Total Sessions:    %d\n", stats.TotalSessions)
	fmt.Printf("Total Messages:    %d\n", stats.TotalMessages)
	fmt.Printf("Total Tool Uses:   %d\n", stats.TotalToolUses)

	fmt.Println()

	// Display date range if we have sessions
	if stats.TotalSessions > 0 {
		if !stats.OldestSession.IsZero() {
			fmt.Printf("Oldest Session:    %s\n", stats.OldestSession.Format("Jan 2, 2006 3:04 PM"))
		}

		if !stats.NewestSession.IsZero() {
			fmt.Printf("Newest Session:    %s\n", stats.NewestSession.Format("Jan 2, 2006 3:04 PM"))
		}

		fmt.Println()

		// Display most active project if available
		if stats.MostActiveProject != "" {
			fmt.Printf("Most Active Project:\n")
			fmt.Printf("  Path:     %s\n", stats.MostActiveProject)
			fmt.Printf("  Sessions: %d\n", stats.MostActiveProjectCount)
			fmt.Println()
		}
	}

	// Database file size (interface concern - file system info)
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
