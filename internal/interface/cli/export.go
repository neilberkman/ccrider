package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/neilberkman/ccrider/internal/core/config"
	"github.com/neilberkman/ccrider/internal/core/db"
	"github.com/neilberkman/ccrider/internal/core/export"
	"github.com/spf13/cobra"
)

var (
	exportOutput string
	exportRepo   bool
	exportForce  bool
)

var exportCmd = &cobra.Command{
	Use:   "export <session-id>",
	Short: "Export a session to markdown",
	Long: `Export a Claude Code session to markdown.

By default, writes markdown to stdout for piping or redirection.

Use --output to write to a specific file path.
Use --repo to write to the session's repo at <repo>/.ccrider/exports/session-<id>.md.

Examples:
  ccrider export 0ccfddc4-00e7-443a-bb82-58ede5936619
  ccrider export 0ccfddc4-00e7-443a-bb82-58ede5936619 | pbcopy
  ccrider export 0ccfddc4-00e7-443a-bb82-58ede5936619 > session.md
  ccrider export 0ccfddc4-00e7-443a-bb82-58ede5936619 --repo
  ccrider export 0ccfddc4-00e7-443a-bb82-58ede5936619 --output ~/exported-session.md
  ccrider export 0ccfddc4-00e7-443a-bb82-58ede5936619 --output session.md --force`,
	Args: cobra.ExactArgs(1),
	RunE: runExport,
}

func init() {
	rootCmd.AddCommand(exportCmd)
	exportCmd.Flags().StringVarP(&exportOutput, "output", "o", "", "Output file path")
	exportCmd.Flags().BoolVar(&exportRepo, "repo", false, "Write to session's repo at <repo>/.ccrider/exports/")
	exportCmd.Flags().BoolVarP(&exportForce, "force", "f", false, "Overwrite existing file")
}

func runExport(cmd *cobra.Command, args []string) error {
	sessionID := args[0]

	if exportOutput != "" && exportRepo {
		return fmt.Errorf("--output and --repo are mutually exclusive")
	}

	// Open database
	database, err := db.New(dbPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer func() {
		_ = database.Close()
	}()

	// Generate markdown
	content, err := export.GenerateMarkdown(database, sessionID)
	if err != nil {
		return err
	}

	// Determine output mode
	if exportOutput == "" && !exportRepo {
		// Default: write to stdout
		fmt.Print(content)
		return nil
	}

	var outputPath string

	if exportRepo {
		// Look up session's project path
		detail, err := database.GetSessionDetail(sessionID)
		if err != nil {
			return fmt.Errorf("session not found: %w", err)
		}
		if detail.ProjectPath == "" {
			return fmt.Errorf("session has no associated repo; use --output to specify a path")
		}
		outputPath = export.RepoExportPath(sessionID, detail.ProjectPath)
	} else {
		// Explicit --output path
		outputPath = exportOutput
		if !filepath.IsAbs(outputPath) {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("failed to get current directory: %w", err)
			}
			outputPath = filepath.Join(cwd, outputPath)
		}
	}

	if err := export.WriteExport(content, outputPath, exportForce); err != nil {
		return err
	}

	// Remember the export directory in config
	cfg, _ := config.Load()
	if cfg != nil {
		dir := filepath.Dir(outputPath)
		cfg.LastExportDir = dir
		if exportRepo {
			// Look up project path for per-repo memory
			detail, _ := database.GetSessionDetail(sessionID)
			if detail != nil && detail.ProjectPath != "" {
				if cfg.RepoExportDirs == nil {
					cfg.RepoExportDirs = make(map[string]string)
				}
				cfg.RepoExportDirs[detail.ProjectPath] = dir
			}
		}
		_ = config.Save(cfg)
	}

	fmt.Fprintf(os.Stderr, "Exported session to %s\n", outputPath)
	return nil
}
