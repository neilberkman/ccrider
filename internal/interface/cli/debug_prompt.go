package cli

import (
	"fmt"
	"time"

	"github.com/cbroglie/mustache"
	"github.com/dustin/go-humanize"
	"github.com/neilberkman/ccrider/internal/core/config"
	"github.com/neilberkman/ccrider/internal/core/db"
	"github.com/spf13/cobra"
)

var debugPromptCmd = &cobra.Command{
	Use:   "debug-prompt <session-id>",
	Short: "Show what resume prompt would be generated for a session",
	Args:  cobra.ExactArgs(1),
	RunE:  runDebugPrompt,
}

func init() {
	rootCmd.AddCommand(debugPromptCmd)
}

func runDebugPrompt(cmd *cobra.Command, args []string) error {
	sessionID := args[0]

	database, err := db.New(dbPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer database.Close()

	// Get session info + last cwd
	var projectPath, lastCwd, updatedAt string
	err = database.QueryRow(`
		SELECT
			s.project_path,
			s.updated_at,
			COALESCE(
				(SELECT cwd FROM messages
				 WHERE session_id = s.id
				   AND cwd IS NOT NULL
				   AND cwd != ''
				   AND cwd != '/'
				 ORDER BY sequence DESC LIMIT 1),
				s.project_path
			) as last_cwd
		FROM sessions s
		WHERE s.session_id = ?
	`, sessionID).Scan(&projectPath, &updatedAt, &lastCwd)
	if err != nil {
		return fmt.Errorf("session not found: %w", err)
	}

	// Load config
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Build template data
	updatedTime, _ := time.Parse("2006-01-02 15:04:05", updatedAt)
	if updatedTime.IsZero() {
		updatedTime, _ = time.Parse(time.RFC3339, updatedAt)
	}

	timeSince := "unknown"
	if !updatedTime.IsZero() {
		timeSince = humanize.Time(updatedTime)
	}

	templateData := map[string]string{
		"last_updated": updatedAt,
		"last_cwd":     lastCwd,
		"time_since":   timeSince,
		"project_path": projectPath,
	}

	// Render prompt
	resumePrompt, err := mustache.Render(cfg.ResumePromptTemplate, templateData)
	if err != nil {
		return fmt.Errorf("failed to render template: %w", err)
	}

	// Output
	fmt.Println("=== SESSION INFO ===")
	fmt.Printf("Session ID:   %s\n", sessionID)
	fmt.Printf("Project Path: %s\n", projectPath)
	fmt.Printf("Last CWD:     %s\n", lastCwd)
	fmt.Printf("Updated At:   %s\n", updatedAt)
	fmt.Printf("Time Since:   %s\n", timeSince)
	fmt.Println()
	fmt.Println("=== TEMPLATE DATA ===")
	for k, v := range templateData {
		fmt.Printf("%s: %s\n", k, v)
	}
	fmt.Println()
	fmt.Println("=== RESUME PROMPT ===")
	fmt.Println(resumePrompt)
	fmt.Println()
	fmt.Println("=== COMMAND ===")
	fmt.Printf("cd %s && claude --resume %s \"<prompt above>\"\n", projectPath, sessionID)

	return nil
}
