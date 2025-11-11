package cli

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
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
	defer func() { _ = database.Close() }()

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
	// Build claude command with config flags and resume prompt
	cmd, err := session.BuildResumeCommand(sessionID, projectPath, lastCwd, updatedAt, fork)
	if err != nil {
		return err
	}

	// Extract temp file path from command for cleanup
	// Command format: claude --resume <id> "$(cat /tmp/ccrider-prompt-*.txt)"
	var tmpfilePath string
	if start := strings.Index(cmd, "$(cat "); start != -1 {
		start += 6 // len("$(cat ")
		if end := strings.Index(cmd[start:], ")"); end != -1 {
			tmpfilePath = cmd[start : start+end]
		}
	}
	if tmpfilePath != "" {
		defer func() { _ = os.Remove(tmpfilePath) }()
	}

	// Parse updatedTime for terminal title
	updatedTime, _ := time.Parse("2006-01-02 15:04:05", updatedAt)
	if updatedTime.IsZero() {
		updatedTime, _ = time.Parse(time.RFC3339, updatedAt)
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
