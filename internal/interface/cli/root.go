package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var (
	dbPath string
)

// Execute runs the CLI
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:   "ccrider",
	Short: "Claude Code session manager",
	Long: `ccrider - search, browse, and resume your Claude Code sessions

A fast, reliable tool for managing Claude Code sessions with full-text search,
incremental sync, and native resume integration.`,
	Version: "0.1.0",
	RunE: func(cmd *cobra.Command, args []string) error {
		// Default to TUI if no subcommand specified
		return tuiCmd.RunE(cmd, args)
	},
}

func init() {
	// Global flags
	home, err := os.UserHomeDir()
	if err != nil {
		home = "~"
	}
	defaultDB := filepath.Join(home, ".config", "ccrider", "sessions.db")

	rootCmd.PersistentFlags().StringVar(&dbPath, "db", defaultDB, "Database path")
}
