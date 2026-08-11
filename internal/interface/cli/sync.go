package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/neilberkman/ccrider/internal/core/config"
	"github.com/neilberkman/ccrider/internal/core/db"
	"github.com/neilberkman/ccrider/internal/core/importer"
	"github.com/neilberkman/ccrider/internal/core/session"
	"github.com/neilberkman/ccrider/pkg/ccsessions"
	"github.com/spf13/cobra"
)

var (
	syncForce   bool
	syncTimeout time.Duration
)

var syncCmd = &cobra.Command{
	Use:   "sync [path]",
	Short: "Import/sync coding agent sessions",
	Long: fmt.Sprintf(`Import sessions from %s.

With an explicit path, imports Claude Code JSONL sessions from that directory.

Performs incremental sync - only imports new or changed sessions.
Use --force to re-import all sessions (fixes stale project_path values).`,
		joinWithAnd(session.ProviderSources())),
	Args: cobra.MaximumNArgs(1),
	RunE: runSync,
}

func init() {
	syncCmd.Flags().BoolVarP(&syncForce, "force", "f", false, "Force re-import of all sessions")
	syncCmd.Flags().DurationVar(&syncTimeout, "sync-timeout", importer.DefaultRemoteSyncTimeout, "Maximum time per remote provider sync (0 disables)")
	rootCmd.AddCommand(syncCmd)
}

func runSync(cmd *cobra.Command, args []string) error {
	if syncTimeout < 0 {
		return errors.New("sync timeout must not be negative")
	}
	// Determine source path for explicit Claude JSONL imports.
	sourcePath := ""
	if len(args) > 0 {
		sourcePath = args[0]
		// Resolve symlinks (filepath.Walk doesn't follow them)
		resolved, err := filepath.EvalSymlinks(sourcePath)
		if err == nil {
			sourcePath = resolved
		}
	}

	if len(args) > 0 {
		fmt.Fprintf(os.Stderr, "Syncing Claude Code sessions from: %s\n", sourcePath)
	} else {
		fmt.Fprintf(os.Stderr, "Syncing sessions from %s\n", joinWithAnd(session.ProviderProducts()))
	}
	fmt.Fprintf(os.Stderr, "Database: %s\n\n", dbPath)

	// Ensure database directory exists
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return fmt.Errorf("failed to create db directory: %w", err)
	}

	// Open database
	database, err := db.New(dbPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer func() {
		_ = database.Close()
	}()

	// Check if we need one-time migration sync
	if !syncForce {
		needsMigrationSync, err := database.NeedsMigrationSync(session.FileIdentityProviderNames())
		if err != nil {
			return fmt.Errorf("failed to check migration status: %w", err)
		}
		if needsMigrationSync {
			fmt.Fprintln(os.Stderr, "⚡ One-time optimization: Populating file tracking data for fast incremental syncs...")
			fmt.Fprintln(os.Stderr, "   This will take a minute but makes future syncs much faster.")
			fmt.Fprintln(os.Stderr)
			syncForce = true
		}
	}

	imp := importer.New(database)

	// When the user passes an explicit path, import only Claude sessions from it;
	// otherwise import every auto-discovered provider.
	var sources []importer.Source
	if len(args) > 0 {
		sources = []importer.Source{{
			Path:          sourcePath,
			ParseFn:       ccsessions.ParseFile,
			Provider:      "claude",
			SkipSubagents: true,
		}}
	} else {
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}
		sources = importer.DefaultSources(cfg.AmpEnabled)
	}

	return importer.SyncSources(cmd.Context(), sources, syncTimeout, func(sourceCtx context.Context, src importer.Source) error {
		prepared, err := imp.PrepareSource(sourceCtx, src)
		if err != nil {
			return fmt.Errorf("failed to prepare %s sessions: %w", src.Provider, err)
		}
		if prepared.Warning != nil {
			fmt.Fprintf(os.Stderr, "WARN: %s sync skipped: %v\n", prepared.Provider, prepared.Warning)
			return nil
		}
		if prepared.Total == 0 {
			return nil
		}

		fmt.Fprintf(os.Stderr, "Syncing %s sessions from: %s\n", prepared.Provider, prepared.Path)
		stat, statErr := os.Stderr.Stat()
		interactive := statErr == nil && stat.Mode()&os.ModeCharDevice != 0
		progress := importer.NewProgressReporter(os.Stderr, prepared.Total, interactive)
		result, runErr := prepared.Run(sourceCtx, progress, syncForce)
		progress.Finish()
		for _, failure := range result.Failures {
			fmt.Fprintf(os.Stderr, "WARN: Cannot import %s session %s: %v\n", src.Provider, failure.ID, failure.Err)
		}
		if len(result.Deferred) > 0 {
			fmt.Fprintf(os.Stderr, "WARN: %s sync interrupted; deferred %d changed sessions until the next sync (%s)\n", src.Provider, len(result.Deferred), deferredIDs(result.Deferred, 5))
		}

		if result.Skipped > 0 && !syncForce {
			skipRate := float64(result.Skipped) / float64(prepared.Total) * 100
			unit := "files"
			if src.EnumerateFn != nil || src.Remote != nil {
				unit = "sessions"
			}
			fmt.Fprintf(os.Stderr, "\nSkipped %d/%d %s %s (%.1f%% unchanged)\n", result.Skipped, prepared.Total, src.Provider, unit, skipRate)
		}
		if runErr != nil {
			return fmt.Errorf("%s import failed: %w", src.Provider, runErr)
		}
		return nil
	})
}

func deferredIDs(ids []string, limit int) string {
	shown := ids
	suffix := ""
	if len(shown) > limit {
		shown = shown[:limit]
		suffix = fmt.Sprintf(", and %d more", len(ids)-limit)
	}
	return strings.Join(shown, ", ") + suffix
}
