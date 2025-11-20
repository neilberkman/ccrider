package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/neilberkman/ccrider/internal/core/db"
	"github.com/neilberkman/ccrider/internal/core/llm"
	"github.com/neilberkman/ccrider/internal/core/search"
	"github.com/spf13/cobra"
)

var infoCmd = &cobra.Command{
	Use:   "info [query]",
	Short: "Deep investigation across sessions",
	Long: `Gather detailed information about an issue or topic across all sessions.

Examples:
  ccrider info "ena-6530"
  ccrider info "postgres deadlock"
  ccrider info "authentication bugs" --export report.md`,
	Args: cobra.MinimumNArgs(1),
	RunE: runInfo,
}

var (
	infoExport string
	infoModel  string
)

func init() {
	rootCmd.AddCommand(infoCmd)

	infoCmd.Flags().StringVarP(&infoExport, "export", "e", "", "Export report to file")
	infoCmd.Flags().StringVar(&infoModel, "model", "llama-8b", "Model to use for AI search")
}

func runInfo(cmd *cobra.Command, args []string) error {
	query := strings.Join(args, " ")

	// Open database
	database, err := db.New(dbPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer database.Close()

	fmt.Printf("Investigating: \"%s\"\n\n", query)

	// Step 1: Find all relevant sessions
	// Try exact match first (fast)
	var sessions []search.SmartSearchResult

	// Check for issue ID pattern
	if isIssueIDPattern(query) {
		issueID := strings.ToLower(strings.TrimSpace(query))
		dbSessions, err := database.FindSessionsByIssueID(issueID)
		if err != nil {
			return fmt.Errorf("issue ID search failed: %w", err)
		}
		if len(dbSessions) > 0 {
			fmt.Printf("Found %d session(s) mentioning issue %s:\n\n", len(dbSessions), query)
			// Convert to SmartSearchResult
			for _, s := range dbSessions {
				sessions = append(sessions, search.SmartSearchResult{
					Session:   s,
					Relevance: 1.0,
					Method:    "exact",
					Summary:   s.Summary,
				})
			}
		}
	}

	// If no exact match, use smart search
	if len(sessions) == 0 {
		fmt.Println("Initializing AI search...")

		modelManager, err := llm.NewModelManager()
		if err != nil {
			return fmt.Errorf("failed to create model manager: %w", err)
		}

		modelPath, err := modelManager.EnsureModel(infoModel)
		if err != nil {
			return fmt.Errorf("failed to ensure model: %w", err)
		}

		inference, err := llm.NewLLM(modelPath, infoModel)
		if err != nil {
			return fmt.Errorf("failed to initialize LLM: %w", err)
		}
		defer inference.Close()

		searcher := search.NewSmartSearcher(database, inference)
		sessions, err = searcher.Search(context.Background(), query)
		if err != nil {
			return fmt.Errorf("search failed: %w", err)
		}

		if len(sessions) == 0 {
			fmt.Println("No matching sessions found.")
			return nil
		}

		fmt.Printf("Found %d session(s) [method: %s]:\n\n", len(sessions), sessions[0].Method)
	}

	// Step 2: Display session details
	report := generateReport(sessions, query)
	fmt.Println(report)

	// Step 3: Export if requested
	if infoExport != "" {
		return exportReport(infoExport, report)
	}

	return nil
}

func generateReport(sessions []search.SmartSearchResult, query string) string {
	var report strings.Builder

	report.WriteString("═══════════════════════════════════════════════════\n")
	report.WriteString(fmt.Sprintf(" INVESTIGATION REPORT: %s\n", query))
	report.WriteString("═══════════════════════════════════════════════════\n\n")

	for i, result := range sessions {
		report.WriteString(fmt.Sprintf("Session %d: %s\n", i+1, result.Session.SessionID))
		report.WriteString(fmt.Sprintf("Project: %s\n", result.Session.ProjectPath))
		report.WriteString(fmt.Sprintf("Updated: %s\n", result.Session.UpdatedAt.Format("2006-01-02 15:04")))

		if result.Session.Summary != "" {
			summary := result.Session.Summary
			if len(summary) > 200 {
				summary = summary[:197] + "..."
			}
			report.WriteString(fmt.Sprintf("Summary: %s\n", summary))
		}

		if result.Relevance < 1.0 {
			report.WriteString(fmt.Sprintf("Relevance: %.0f%%\n", result.Relevance*100))
		}

		report.WriteString("\n")
		report.WriteString("───────────────────────────────────────────────────\n\n")
	}

	report.WriteString("═══════════════════════════════════════════════════\n")
	report.WriteString(fmt.Sprintf(" TOTAL: %d session(s) found\n", len(sessions)))
	report.WriteString("═══════════════════════════════════════════════════\n")

	return report.String()
}

func exportReport(filename, report string) error {
	file, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer file.Close()

	_, err = file.WriteString(report)
	if err != nil {
		return fmt.Errorf("failed to write report: %w", err)
	}

	fmt.Printf("\nReport exported to: %s\n", filename)
	return nil
}

func isIssueIDPattern(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	// Match patterns like: ena-6530, ENA-6530
	if len(s) < 3 {
		return false
	}
	// Simple heuristic: contains letter + dash + number
	return strings.Contains(s, "-") && len(s) < 20
}
