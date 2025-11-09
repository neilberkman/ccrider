package cli

import (
	"fmt"
	"os"
	"os/exec"
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
	// Find claude binary
	claudePath, err := exec.LookPath("claude")
	if err != nil {
		return fmt.Errorf("claude not found in PATH: %w", err)
	}

	// Build args
	args := []string{"claude", "--resume", sessionID}
	if fork {
		args = append(args, "--fork-session")
	}

	// Change to project directory
	if projectPath != "" {
		if err := os.Chdir(projectPath); err != nil {
			return fmt.Errorf("failed to cd to %s: %w", projectPath, err)
		}
	}

	// Exec claude (replaces current process)
	return syscall.Exec(claudePath, args, os.Environ())
}
