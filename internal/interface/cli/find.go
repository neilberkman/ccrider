package cli

import (
	"fmt"
	"strings"

	"github.com/neilberkman/ccrider/internal/core/db"
	"github.com/spf13/cobra"
)

var findCmd = &cobra.Command{
	Use:   "find",
	Short: "Find sessions by issue ID or file path",
	Long: `Find sessions that mention specific issue IDs or file paths.

Examples:
  ccrider find --issue ena-6530        Find sessions mentioning ena-6530
  ccrider find --file schema.go        Find sessions mentioning schema.go
  ccrider find --stats                 Show metadata statistics`,
	RunE: runFind,
}

var (
	findIssue string
	findFile  string
	findStats bool
)

func init() {
	rootCmd.AddCommand(findCmd)

	findCmd.Flags().StringVar(&findIssue, "issue", "", "Find sessions by issue ID")
	findCmd.Flags().StringVar(&findFile, "file", "", "Find sessions by file path")
	findCmd.Flags().BoolVar(&findStats, "stats", false, "Show metadata statistics")
}

func runFind(cmd *cobra.Command, args []string) error {
	// Open database
	database, err := db.New(dbPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer database.Close()

	// Handle --stats flag
	if findStats {
		return showMetadataStats(database)
	}

	// Handle --issue flag
	if findIssue != "" {
		return findByIssue(database, findIssue)
	}

	// Handle --file flag
	if findFile != "" {
		return findByFile(database, findFile)
	}

	// No flags specified
	fmt.Println("Please specify --issue, --file, or --stats")
	fmt.Println()
	cmd.Help()
	return nil
}

func showMetadataStats(database *db.DB) error {
	issues, files, sessions, err := database.GetMetadataStats()
	if err != nil {
		return fmt.Errorf("failed to get stats: %w", err)
	}

	fmt.Println("Metadata Statistics:")
	fmt.Println("====================")
	fmt.Printf("Unique issue IDs:    %d\n", issues)
	fmt.Printf("Unique file paths:   %d\n", files)
	fmt.Printf("Sessions indexed:    %d\n", sessions)
	fmt.Println()

	if sessions == 0 {
		fmt.Println("No metadata extracted yet. Run 'ccrider sync' to import and extract metadata.")
	}

	return nil
}

func findByIssue(database *db.DB, issueID string) error {
	sessions, err := database.FindSessionsByIssueID(issueID)
	if err != nil {
		return fmt.Errorf("failed to find sessions: %w", err)
	}

	if len(sessions) == 0 {
		fmt.Printf("No sessions found mentioning issue ID: %s\n", issueID)
		return nil
	}

	fmt.Printf("Found %d session(s) mentioning %s:\n\n", len(sessions), issueID)

	for i, session := range sessions {
		fmt.Printf("%d. %s\n", i+1, session.SessionID)
		fmt.Printf("   Project: %s\n", session.ProjectPath)
		fmt.Printf("   Updated: %s\n", session.UpdatedAt.Format("2006-01-02 15:04"))

		// Show summary if available
		if session.Summary != "" {
			summary := session.Summary
			if len(summary) > 200 {
				summary = summary[:197] + "..."
			}
			fmt.Printf("   Summary: %s\n", strings.ReplaceAll(summary, "\n", " "))
		}

		fmt.Println()
	}

	return nil
}

func findByFile(database *db.DB, filePath string) error {
	sessions, err := database.FindSessionsByFilePath(filePath)
	if err != nil {
		return fmt.Errorf("failed to find sessions: %w", err)
	}

	if len(sessions) == 0 {
		fmt.Printf("No sessions found mentioning file: %s\n", filePath)
		return nil
	}

	fmt.Printf("Found %d session(s) mentioning %s:\n\n", len(sessions), filePath)

	for i, session := range sessions {
		fmt.Printf("%d. %s\n", i+1, session.SessionID)
		fmt.Printf("   Project: %s\n", session.ProjectPath)
		fmt.Printf("   Updated: %s\n", session.UpdatedAt.Format("2006-01-02 15:04"))

		// Show summary if available
		if session.Summary != "" {
			summary := session.Summary
			if len(summary) > 200 {
				summary = summary[:197] + "..."
			}
			fmt.Printf("   Summary: %s\n", strings.ReplaceAll(summary, "\n", " "))
		}

		fmt.Println()
	}

	return nil
}
