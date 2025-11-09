package cli

import (
	"fmt"
	"os"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
	"github.com/yourusername/ccrider/internal/core/db"
	"github.com/yourusername/ccrider/internal/interface/tui"
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
			return execClaude(m.LaunchSessionID, m.LaunchProjectPath, m.LaunchFork)
		}
	}

	return nil
}

func execClaude(sessionID, projectPath string, fork bool) error {
	// Build claude command
	var cmd string
	if fork {
		cmd = fmt.Sprintf("claude --resume %s --fork-session", sessionID)
	} else {
		cmd = fmt.Sprintf("claude --resume %s", sessionID)
	}

	// Debug: print what we're doing
	fmt.Fprintf(os.Stderr, "[ccrider] cd %s && %s\n", projectPath, cmd)

	// Change to project directory
	if projectPath != "" {
		if err := os.Chdir(projectPath); err != nil {
			return fmt.Errorf("failed to cd to %s: %w", projectPath, err)
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
