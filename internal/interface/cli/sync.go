package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/neilberkman/ccrider/internal/core/db"
	"github.com/neilberkman/ccrider/internal/core/importer"
	"github.com/spf13/cobra"
)

var (
	syncForce          bool
	syncSkipSubagents  bool
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
	syncCmd.Flags().BoolVar(&syncSkipSubagents, "skip-subagents", true, "Skip subagent session files (default: true)")
	rootCmd.AddCommand(syncCmd)
}

func runSync(cmd *cobra.Command, args []string) error {
	// Determine source path
	sourcePath := getDefaultClaudeDir()
	if len(args) > 0 {
		sourcePath = args[0]
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

	// Count total files for progress
	total, err := countJSONLFiles(sourcePath)
	if err != nil {
		return fmt.Errorf("failed to count files: %w", err)
	}

	if total == 0 {
		fmt.Println("No session files found")
		return nil
	}

	// Create importer with progress
	imp := importer.New(database)
	progress := importer.NewProgressReporter(os.Stdout, total)

	// Import
	skipped, err := imp.ImportDirectory(sourcePath, progress, syncForce, syncSkipSubagents)
	if err != nil {
		return fmt.Errorf("import failed: %w", err)
	}

	progress.Finish()

	if skipped > 0 && !syncForce {
		skipRate := float64(skipped) / float64(total) * 100
		fmt.Printf("\nSkipped %d/%d files (%.1f%% unchanged)\n", skipped, total, skipRate)
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
			count++
		}
		return nil
	})
	return count, err
}
