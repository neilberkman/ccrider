package export

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/neilberkman/ccrider/internal/core/db"
)

// GenerateMarkdown produces a complete markdown export of a session.
func GenerateMarkdown(database *db.DB, sessionID string) (string, error) {
	detail, err := database.GetSessionDetail(sessionID)
	if err != nil {
		return "", fmt.Errorf("session not found: %w", err)
	}

	var b strings.Builder

	// Header
	b.WriteString("# ")
	b.WriteString(detail.Summary)
	b.WriteString("\n\n")

	// Metadata
	b.WriteString("**Session ID:** `")
	b.WriteString(detail.SessionID)
	b.WriteString("`  \n")
	b.WriteString("**Project:** `")
	b.WriteString(detail.ProjectPath)
	b.WriteString("`  \n")
	b.WriteString("**Updated:** ")
	b.WriteString(detail.UpdatedAt.Format("Jan 02, 2006 15:04:05"))
	b.WriteString("  \n")
	b.WriteString("**Messages:** ")
	b.WriteString(fmt.Sprintf("%d", detail.MessageCount))
	b.WriteString("\n\n")
	b.WriteString("---\n\n")

	// Messages
	for _, msg := range detail.Messages {
		// Skip summary entries
		if msg.Type == "summary" {
			continue
		}

		// Determine label
		label := strings.ToUpper(msg.Type)
		if msg.Sender != "" {
			label = strings.ToUpper(msg.Sender)
		}

		// Message header
		b.WriteString("**")
		b.WriteString(label)
		b.WriteString("**")
		b.WriteString(" _")
		b.WriteString(msg.Timestamp.Format("Jan 02, 2006 15:04:05"))
		b.WriteString("_\n\n")

		// Content
		if msg.Content != "" {
			b.WriteString(msg.Content)
			b.WriteString("\n\n")
		}

		b.WriteString("---\n\n")
	}

	return b.String(), nil
}

// DefaultFilename returns the standard export filename for a session ID.
func DefaultFilename(sessionID string) string {
	shortID := sessionID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	return fmt.Sprintf("session-%s.md", shortID)
}

// RepoExportPath returns the repo-local export path for a session,
// or empty string if projectPath is empty.
func RepoExportPath(sessionID, projectPath string) string {
	if projectPath == "" {
		return ""
	}
	return filepath.Join(projectPath, ".ccrider", "exports", DefaultFilename(sessionID))
}

// WriteExport writes content to filePath, creating parent directories.
// If force is false and the file already exists, returns an error.
func WriteExport(content, filePath string, force bool) error {
	// Create parent directories
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	// Check for existing file
	if !force {
		if _, err := os.Stat(filePath); err == nil {
			return fmt.Errorf("file already exists: %s (use force to overwrite)", filePath)
		}
	}

	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write export: %w", err)
	}

	return nil
}

// FormatTimestamp parses various timestamp formats and returns a human-readable string.
func FormatTimestamp(ts string) string {
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

	return ts
}
