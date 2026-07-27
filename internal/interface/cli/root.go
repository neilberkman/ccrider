package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := rootCmd.ExecuteContext(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:   "ccrider",
	Short: "Coding agent session manager",
	Long: fmt.Sprintf(`ccrider - search, browse, and resume your coding agent sessions

A fast, reliable tool for managing %s
sessions with full-text search, incremental sync, and native resume
integration.

Report issues: https://github.com/neilberkman/ccrider/issues`, joinWithAnd(session.ProviderProducts())),
	SilenceErrors: true,
	SilenceUsage:  true,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Default to TUI if no subcommand specified; fall back to help
		// when stdout is not a terminal (piped or redirected)
		if stat, err := os.Stdout.Stat(); err == nil && stat.Mode()&os.ModeCharDevice == 0 {
			return cmd.Help()
		}
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
