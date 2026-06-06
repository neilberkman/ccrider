package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/neilberkman/ccrider/internal/core/db"
	"github.com/neilberkman/ccrider/internal/core/importer"
	"github.com/neilberkman/ccrider/pkg/ccsessions"
	"github.com/spf13/cobra"
)

var (
	syncForce bool
)

var syncCmd = &cobra.Command{
	Use:   "sync [path]",
	Short: "Import/sync coding agent sessions",
	Long: `Import sessions from Claude Code (~/.claude/projects/), Codex CLI
(~/.codex/sessions/), and GitHub Copilot CLI (~/.copilot/session-state/),
or from a specified Claude Code directory.

Performs incremental sync - only imports new or changed sessions.
Use --force to re-import all sessions (fixes stale project_path values).`,
	Args: cobra.MaximumNArgs(1),
	RunE: runSync,
}

func init() {
	syncCmd.Flags().BoolVarP(&syncForce, "force", "f", false, "Force re-import of all sessions")
	rootCmd.AddCommand(syncCmd)
}

func runSync(cmd *cobra.Command, args []string) error {
	// Determine source path
	sourcePath := getDefaultClaudeDir()
	if len(args) > 0 {
		sourcePath = args[0]
	}

	// Resolve symlinks (filepath.Walk doesn't follow them)
	resolved, err := filepath.EvalSymlinks(sourcePath)
	if err == nil {
		sourcePath = resolved
	}

	fmt.Printf("Syncing sessions from: %s\n", sourcePath)
	fmt.Printf("Database: %s\n\n", dbPath)

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
		needsMigrationSync, err := database.NeedsMigrationSync()
		if err != nil {
			return fmt.Errorf("failed to check migration status: %w", err)
		}
		if needsMigrationSync {
			fmt.Println("⚡ One-time optimization: Populating file tracking data for fast incremental syncs...")
			fmt.Println("   This will take a minute but makes future syncs much faster.")
			fmt.Println()
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
		sources = importer.DefaultSources()
	}

	for _, src := range sources {
		prepared, err := imp.PrepareSource(src)
		if err != nil {
			return fmt.Errorf("failed to prepare %s sessions: %w", src.Provider, err)
		}
		if prepared.Total == 0 {
			continue
		}

		fmt.Printf("Syncing %s sessions from: %s\n", prepared.Provider, prepared.Path)
		progress := importer.NewProgressReporter(os.Stdout, prepared.Total)
		skipped, err := prepared.Run(progress, syncForce)
		if err != nil {
			return fmt.Errorf("%s import failed: %w", src.Provider, err)
		}
		progress.Finish()

		if skipped > 0 && !syncForce {
			skipRate := float64(skipped) / float64(prepared.Total) * 100
			unit := "files"
			if src.EnumerateFn != nil {
				unit = "sessions"
			}
			fmt.Printf("\nSkipped %d/%d %s %s (%.1f%% unchanged)\n", skipped, prepared.Total, src.Provider, unit, skipRate)
		}
	}

	return nil
}

func getDefaultClaudeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "~/.claude/projects"
	}
	return filepath.Join(home, ".claude", "projects")
}
