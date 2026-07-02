package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/neilberkman/ccrider/internal/core/session"
	"github.com/spf13/cobra"
)

var (
	dbPath      string
	versionInfo string
)

// SetVersion sets the version information from build-time ldflags
func SetVersion(version, commit, date string) {
	versionInfo = fmt.Sprintf("%s (commit: %s, built: %s)", version, commit, date)
	rootCmd.Version = versionInfo
}

// Execute runs the CLI
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:   "ccrider",
	Short: "Coding agent session manager",
	Long: fmt.Sprintf(`ccrider - search, browse, and resume your coding agent sessions

A fast, reliable tool for managing %s
sessions with full-text search, incremental sync, and native resume
integration.`, joinWithAnd(session.ProviderProducts())),
	RunE: func(cmd *cobra.Command, args []string) error {
		// Default to TUI if no subcommand specified
		return tuiCmd.RunE(cmd, args)
	},
}

// joinWithAnd renders items as a natural-language list: "a, b, and c".
func joinWithAnd(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	case 2:
		return items[0] + " and " + items[1]
	default:
		return strings.Join(items[:len(items)-1], ", ") + ", and " + items[len(items)-1]
	}
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
