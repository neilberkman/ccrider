package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/neilberkman/ccrider/internal/core/db"
	"github.com/neilberkman/ccrider/internal/core/importer"
	"github.com/spf13/cobra"
)

var (
	syncForce bool
)

var syncCmd = &cobra.Command{
	Use:   "sync [path]",
	Short: "Import/sync Claude Code sessions",
	Long: `Import sessions from ~/.claude/projects/ or a specified directory.

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

	for _, src := range importer.DefaultSources() {
		// When user specified a custom path, only import Claude from that path
		if len(args) > 0 && src.Provider != "claude" {
			continue
		}
		importPath := src.Path
		if len(args) > 0 {
			importPath = sourcePath
		}

		total, err := countJSONLFiles(importPath)
		if err != nil {
			return fmt.Errorf("failed to count %s files: %w", src.Provider, err)
		}
		if total == 0 {
			continue
		}

		fmt.Printf("Syncing %s sessions from: %s\n", src.Provider, importPath)
		progress := importer.NewProgressReporter(os.Stdout, total)
		skipped, err := imp.ImportDirectory(importPath, progress, syncForce, src.SkipSubagents, src.ParseFn, src.Provider)
		if err != nil {
			return fmt.Errorf("%s import failed: %w", src.Provider, err)
		}
		progress.Finish()

		if skipped > 0 && !syncForce {
			skipRate := float64(skipped) / float64(total) * 100
			fmt.Printf("\nSkipped %d/%d %s files (%.1f%% unchanged)\n", skipped, total, src.Provider, skipRate)
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

func countJSONLFiles(dirPath string) (int, error) {
	count := 0
	err := filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && filepath.Ext(path) == ".jsonl" {
			basename := filepath.Base(path)
			if strings.Contains(basename, "Edit conflict") {
				return nil
			}
			if strings.Contains(path, "/subagents/") || strings.HasPrefix(basename, "agent-") {
				return nil
			}
			count++
		}
		return nil
	})
	return count, err
}
