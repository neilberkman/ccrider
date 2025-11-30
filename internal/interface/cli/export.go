package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/neilberkman/ccrider/internal/core/db"
)

var (
	exportOutput string
)

var exportCmd = &cobra.Command{
	Use:   "export <session-id>",
	Short: "Export a session to markdown",
	Long: `Export a Claude Code session to a markdown file.

By default exports to current directory as session-<id>.md.
Use --output to specify a custom path.

Examples:
  ccrider export 0ccfddc4-00e7-443a-bb82-58ede5936619
  ccrider export 0ccfddc4-00e7-443a-bb82-58ede5936619 --output ~/exported-session.md
  ccrider export 0ccfddc4-00e7-443a-bb82-58ede5936619 -o session.md`,
	Args: cobra.ExactArgs(1),
	RunE: runExport,
}

func init() {
	rootCmd.AddCommand(exportCmd)
	exportCmd.Flags().StringVarP(&exportOutput, "output", "o", "", "Output file path (default: session-<id>.md in current directory)")
}

func runExport(cmd *cobra.Command, args []string) error {
	sessionID := args[0]

	// Open database
	database, err := db.New(dbPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer func() {
		_ = database.Close()
	}()

	// Get current working directory
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	// Determine output path
	outputPath := exportOutput
	if outputPath == "" {
		// Generate default filename in current directory
		shortID := sessionID
		if len(shortID) > 8 {
			shortID = shortID[:8]
		}
		outputPath = filepath.Join(cwd, fmt.Sprintf("session-%s.md", shortID))
	} else if !filepath.IsAbs(outputPath) {
		// Make relative paths absolute to current directory
		outputPath = filepath.Join(cwd, outputPath)
	}

	// Query session data
	var sessionInternalID int64
	var summary, project, createdAt, updatedAt string
	var messageCount int
	err = database.QueryRow(`
		SELECT
			id,
			COALESCE(summary, ''),
			project_path,
			created_at,
			updated_at,
			(SELECT COUNT(*) FROM messages WHERE session_id = sessions.id) as message_count
		FROM sessions
		WHERE session_id = ?
	`, sessionID).Scan(&sessionInternalID, &summary, &project, &createdAt, &updatedAt, &messageCount)
	if err != nil {
		return fmt.Errorf("session not found: %w", err)
	}

	// Get all messages
	rows, err := database.Query(`
		SELECT type, COALESCE(sender, ''), COALESCE(text_content, ''), timestamp, sequence
		FROM messages
		WHERE session_id = ?
		ORDER BY sequence ASC
	`, sessionInternalID)
	if err != nil {
		return fmt.Errorf("failed to query messages: %w", err)
	}
	defer func() { _ = rows.Close() }()

	// Build markdown content
	var b strings.Builder

	// Header
	b.WriteString("# ")
	b.WriteString(summary)
	b.WriteString("\n\n")

	// Metadata
	b.WriteString("**Session ID:** `")
	b.WriteString(sessionID)
	b.WriteString("`  \n")
	b.WriteString("**Project:** `")
	b.WriteString(project)
	b.WriteString("`  \n")
	b.WriteString("**Created:** ")
	b.WriteString(formatTimestampForExport(createdAt))
	b.WriteString("  \n")
	b.WriteString("**Updated:** ")
	b.WriteString(formatTimestampForExport(updatedAt))
	b.WriteString("  \n")
	b.WriteString("**Messages:** ")
	b.WriteString(fmt.Sprintf("%d", messageCount))
	b.WriteString("\n\n")
	b.WriteString("---\n\n")

	// Messages
	for rows.Next() {
		var msgType, sender, content, timestamp string
		var sequence int
		if err := rows.Scan(&msgType, &sender, &content, &timestamp, &sequence); err != nil {
			continue
		}

		// Skip summary entries
		if msgType == "summary" {
			continue
		}

		// Determine label
		label := strings.ToUpper(msgType)
		if sender != "" {
			label = strings.ToUpper(sender)
		}

		// Message header
		b.WriteString("**")
		b.WriteString(label)
		b.WriteString("**")
		b.WriteString(" _")
		b.WriteString(formatTimestampForExport(timestamp))
		b.WriteString("_\n\n")

		// Content (no truncation)
		if content != "" {
			b.WriteString(content)
			b.WriteString("\n\n")
		}

		b.WriteString("---\n\n")
	}

	// Write to file
	if err := os.WriteFile(outputPath, []byte(b.String()), 0644); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	fmt.Printf("Exported session to: %s\n", outputPath)
	return nil
}

func formatTimestampForExport(ts string) string {
	// Try parsing various formats
	formats := []string{
		"2006-01-02T15:04:05.999Z07:00",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
	}

	for _, format := range formats {
		if t, err := time.Parse(format, ts); err == nil {
			return t.Format("Jan 02, 2006 15:04:05")
		}
	}

	// If parsing fails, return as-is
	return ts
}
