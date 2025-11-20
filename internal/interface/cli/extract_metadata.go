package cli

import (
	"fmt"

	"github.com/neilberkman/ccrider/internal/core/db"
	"github.com/neilberkman/ccrider/internal/core/importer"
	"github.com/spf13/cobra"
)

var extractMetadataCmd = &cobra.Command{
	Use:   "extract-metadata",
	Short: "Extract metadata from existing sessions",
	Long: `Extract issue IDs and file paths from existing sessions.

This is useful for backfilling metadata after initial import.

Examples:
  ccrider extract-metadata --limit 10   Extract for up to 10 sessions
  ccrider extract-metadata --all        Extract for all sessions`,
	RunE: runExtractMetadata,
}

var (
	extractLimit int
	extractAll   bool
)

func init() {
	rootCmd.AddCommand(extractMetadataCmd)

	extractMetadataCmd.Flags().IntVar(&extractLimit, "limit", 100, "Maximum number of sessions to process")
	extractMetadataCmd.Flags().BoolVar(&extractAll, "all", false, "Process all sessions")
}

func runExtractMetadata(cmd *cobra.Command, args []string) error {
	// Open database
	database, err := db.New(dbPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer database.Close()

	// Determine limit
	limit := extractLimit
	if extractAll {
		limit = 100000 // Effectively unlimited
	}

	// Find sessions without metadata
	query := `
		SELECT id, session_id
		FROM sessions
		WHERE id NOT IN (
			SELECT DISTINCT session_id FROM session_issues
			UNION
			SELECT DISTINCT session_id FROM session_files
		)
		AND message_count > 0
		ORDER BY updated_at DESC
		LIMIT ?
	`

	rows, err := database.Query(query, limit)
	if err != nil {
		return fmt.Errorf("failed to query sessions: %w", err)
	}
	defer rows.Close()

	var sessions []struct {
		ID        int64
		SessionID string
	}

	for rows.Next() {
		var s struct {
			ID        int64
			SessionID string
		}
		if err := rows.Scan(&s.ID, &s.SessionID); err != nil {
			return fmt.Errorf("failed to scan session: %w", err)
		}
		sessions = append(sessions, s)
	}

	if len(sessions) == 0 {
		fmt.Println("No sessions need metadata extraction.")
		return nil
	}

	fmt.Printf("Extracting metadata for %d session(s)...\n\n", len(sessions))

	// Create importer (for metadata extraction)
	imp := importer.New(database)

	// Process each session
	successCount := 0
	errorCount := 0

	for i, session := range sessions {
		fmt.Printf("[%d/%d] %s... ", i+1, len(sessions), session.SessionID)

		if err := imp.ExtractMetadata(session.ID); err != nil {
			fmt.Printf("❌ Failed: %v\n", err)
			errorCount++
			continue
		}

		fmt.Printf("✓\n")
		successCount++
	}

	fmt.Printf("\n")
	fmt.Printf("Summary: %d succeeded, %d failed\n", successCount, errorCount)

	// Show stats
	issues, files, indexed, err := database.GetMetadataStats()
	if err == nil {
		fmt.Printf("\nMetadata totals: %d issues, %d files, %d sessions indexed\n", issues, files, indexed)
	}

	return nil
}
