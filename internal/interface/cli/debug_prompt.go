package cli

import (
	"fmt"

	"github.com/dustin/go-humanize"
	"github.com/neilberkman/ccrider/internal/core/config"
	"github.com/neilberkman/ccrider/internal/core/db"
	coresession "github.com/neilberkman/ccrider/internal/core/session"
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
	defer func() { _ = database.Close() }()

	// Use core function to get session launch info
	session, lastCwd, err := database.GetSessionLaunchInfo(sessionID)
	if err != nil {
		return fmt.Errorf("session not found: %w", err)
	}

	projectPath := session.ProjectPath
	updatedAt := session.UpdatedAt.Format("2006-01-02 15:04:05")

	// Load config
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Build template data (display only — the render below goes through core
	// so this command shows exactly what a real resume would send)
	updatedTime := coresession.ParseSessionTime(updatedAt)

	timeSince := "unknown"
	if !updatedTime.IsZero() {
		timeSince = humanize.Time(updatedTime)
	}

	sameDir := lastCwd == projectPath
	templateData := map[string]interface{}{
		"last_updated":        updatedAt,
		"last_cwd":            lastCwd,
		"time_since":          timeSince,
		"project_path":        projectPath,
		"same_directory":      sameDir,
		"different_directory": !sameDir,
	}

	// Render prompt via the same core path every interface uses
	resumePrompt := coresession.RenderResumePrompt(cfg.ResumePromptTemplate, projectPath, lastCwd, updatedAt)

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
		fmt.Printf("%s: %v\n", k, v)
	}
	fmt.Println()
	fmt.Println("=== RESUME PROMPT ===")
	fmt.Println(resumePrompt)
	fmt.Println()
	fmt.Println("=== COMMAND ===")
	flags := coresession.ProviderFlags(cfg, session.Provider)
	spec := coresession.BuildResumeSpec(session.Provider, sessionID, false, flags)
	command := coresession.ResumeCommandIn(projectPath, session.Provider, sessionID, "", false, flags)
	if spec.AcceptsPrompt {
		fmt.Printf("%s \"<prompt above>\"\n", command)
	} else {
		fmt.Println(command)
	}

	return nil
}
