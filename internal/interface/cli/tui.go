package cli

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/cbroglie/mustache"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/dustin/go-humanize"
	"github.com/spf13/cobra"
	"github.com/neilberkman/ccrider/internal/core/config"
	"github.com/neilberkman/ccrider/internal/core/db"
	"github.com/neilberkman/ccrider/internal/core/session"
	"github.com/neilberkman/ccrider/internal/interface/tui"
)

var tuiCmd = &cobra.Command{
	Use:   "tui",
	Short: "Launch interactive TUI browser",
	Long:  "Launch an interactive terminal UI for browsing and viewing Claude Code sessions",
	RunE:  runTUI,
}

func init() {
	rootCmd.AddCommand(tuiCmd)
}

func runTUI(cmd *cobra.Command, args []string) error {
	database, err := db.New(dbPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer database.Close()

	model := tui.New(database)
	p := tea.NewProgram(
		model,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)

	finalModel, err := p.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error running TUI: %v\n", err)
		os.Exit(1)
	}

	// Check if user wants to launch a session
	if m, ok := finalModel.(tui.Model); ok {
		if m.LaunchSessionID != "" {
			// Exec claude to replace this process
			return execClaude(
				m.LaunchSessionID,
				m.LaunchProjectPath,
				m.LaunchLastCwd,
				m.LaunchUpdatedAt,
				m.LaunchSummary,
				m.LaunchFork,
			)
		}
	}

	return nil
}

func execClaude(sessionID, projectPath, lastCwd, updatedAt, summary string, fork bool) error {
	// Load config to get resume prompt template
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

	// Check if we're already in the right directory
	sameDir := (lastCwd == projectPath)

	templateData := map[string]interface{}{
		"last_updated":        updatedAt,
		"last_cwd":            lastCwd,
		"time_since":          timeSince,
		"project_path":        projectPath,
		"same_directory":      sameDir,
		"different_directory": !sameDir,
	}

	// Render the resume prompt
	resumePrompt, err := mustache.Render(cfg.ResumePromptTemplate, templateData)
	if err != nil {
		// Fall back to simple prompt if template fails
		resumePrompt = fmt.Sprintf("Resuming session. You were last in: %s", lastCwd)
	}

	// Write prompt to temp file and pass via command substitution
	// This avoids all shell escaping issues
	tmpfile, err := os.CreateTemp("", "ccrider-prompt-*.txt")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(tmpfile.Name())

	if _, err := tmpfile.Write([]byte(resumePrompt)); err != nil {
		tmpfile.Close()
		return fmt.Errorf("failed to write prompt: %w", err)
	}
	tmpfile.Close()

	// Build claude command with prompt from file
	var cmd string
	flags := ""
	if len(cfg.ClaudeFlags) > 0 {
		flags = " " + strings.Join(cfg.ClaudeFlags, " ")
	}
	if fork {
		cmd = fmt.Sprintf("claude%s --resume %s --fork-session \"$(cat %s)\"", flags, sessionID, tmpfile.Name())
	} else {
		cmd = fmt.Sprintf("claude%s --resume %s \"$(cat %s)\"", flags, sessionID, tmpfile.Name())
	}

	// Resolve working directory (always projectPath, see session.ResolveWorkingDir)
	workDir := session.ResolveWorkingDir(projectPath, lastCwd)

	// Show what we're doing
	fmt.Fprintf(os.Stderr, "[ccrider] cd %s && %s\n", workDir, cmd)

	// Set terminal title before launching
	if !updatedTime.IsZero() && summary != "" {
		// Format: [resumed MM/DD HH:MM] summary
		titleTime := updatedTime.Format("01/02 15:04")
		title := fmt.Sprintf("[resumed %s] %s", titleTime, summary)
		// Set terminal title using escape sequence
		fmt.Fprintf(os.Stderr, "\033]0;%s\007", title)
	}

	// Start spinner (Claude Code can take a few seconds to start)
	spinner := session.NewSpinner("Starting Claude Code (this may take a few seconds)...")
	spinner.Start()
	defer spinner.Stop()

	// Give spinner a moment to show before exec
	time.Sleep(100 * time.Millisecond)

	// Change to working directory
	if workDir != "" {
		if err := os.Chdir(workDir); err != nil {
			return fmt.Errorf("failed to cd to %s: %w", workDir, err)
		}
	}

	// Find shell
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}

	// Exec shell with claude command (replaces current process)
	// Use -c to run the command, -l to make it a login shell (loads asdf/mise)
	return syscall.Exec(shell, []string{shell, "-l", "-c", cmd}, os.Environ())
}
