package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/yourusername/ccrider/internal/core/db"
	"github.com/yourusername/ccrider/internal/core/importer"
)

var syncCmd = &cobra.Command{
	Use:   "sync [path]",
	Short: "Import/sync Claude Code sessions",
	Long: `Import sessions from ~/.claude/projects/ or a specified directory.

Performs incremental sync - only imports new or changed sessions.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runSync,
}

func init() {
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
	if err := imp.ImportDirectory(sourcePath, progress); err != nil {
		return fmt.Errorf("import failed: %w", err)
	}

	progress.Finish()

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
