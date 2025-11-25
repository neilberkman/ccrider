package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/neilberkman/ccrider/internal/core/db"
	"github.com/neilberkman/ccrider/internal/core/llm"
	"github.com/spf13/cobra"
)

var (
	summarizeLimit   int
	summarizeForce   bool
	summarizeModel   string
	summarizeRegion  string
	summarizeVerbose bool
)

var summarizeCmd = &cobra.Command{
	Use:   "summarize",
	Short: "Generate LLM summaries for sessions",
	Long: `Generate summaries for sessions using an LLM provider.

Currently supports AWS Bedrock with Claude models. Requires AWS credentials
to be configured (via environment, profile, or IAM role).

Examples:
  # Summarize sessions without summaries (default: 10 at a time)
  ccrider summarize

  # Summarize more sessions
  ccrider summarize --limit 50

  # Re-summarize all sessions (overwrite existing)
  ccrider summarize --force --limit 100

  # Use a specific model
  ccrider summarize --model anthropic.claude-3-sonnet-20240229-v1:0`,
	RunE: runSummarize,
}

func init() {
	summarizeCmd.Flags().IntVarP(&summarizeLimit, "limit", "n", 10, "Number of sessions to summarize")
	summarizeCmd.Flags().BoolVarP(&summarizeForce, "force", "f", false, "Re-summarize sessions that already have summaries")
	summarizeCmd.Flags().StringVar(&summarizeModel, "model", "", "Bedrock model ID (default: claude-3-haiku)")
	summarizeCmd.Flags().StringVar(&summarizeRegion, "region", "", "AWS region (default: us-east-1)")
	summarizeCmd.Flags().BoolVarP(&summarizeVerbose, "verbose", "v", false, "Show verbose output")

	rootCmd.AddCommand(summarizeCmd)
}

func runSummarize(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	// Open database
	database, err := db.New(dbPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer database.Close()

	// Create Bedrock provider
	provider, err := llm.NewBedrockProvider(ctx, llm.BedrockConfig{
		Region:  summarizeRegion,
		ModelID: summarizeModel,
	})
	if err != nil {
		return fmt.Errorf("failed to create LLM provider: %w", err)
	}

	summarizer := llm.NewSummarizer(provider)

	// Query sessions that need summarization
	whereClause := "WHERE llm_summary IS NULL OR llm_summary = ''"
	if summarizeForce {
		whereClause = ""
	}

	query := fmt.Sprintf(`
		SELECT s.session_id, s.project_path, s.summary
		FROM sessions s
		%s
		ORDER BY s.updated_at DESC
		LIMIT ?
	`, whereClause)

	rows, err := database.Query(query, summarizeLimit)
	if err != nil {
		return fmt.Errorf("failed to query sessions: %w", err)
	}
	defer rows.Close()

	type sessionInfo struct {
		sessionID   string
		projectPath string
		summary     string
	}

	var sessions []sessionInfo
	for rows.Next() {
		var s sessionInfo
		if err := rows.Scan(&s.sessionID, &s.projectPath, &s.summary); err != nil {
			return fmt.Errorf("failed to scan session: %w", err)
		}
		sessions = append(sessions, s)
	}

	if len(sessions) == 0 {
		fmt.Println("No sessions to summarize")
		return nil
	}

	fmt.Printf("Summarizing %d sessions using %s...\n", len(sessions), provider.Name())

	// Process each session
	for i, s := range sessions {
		// Get messages for this session
		messages, err := getSessionMessages(database, s.sessionID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to get messages for %s: %v\n", s.sessionID, err)
			continue
		}

		if len(messages) == 0 {
			if summarizeVerbose {
				fmt.Printf("[%d/%d] %s: no messages, skipping\n", i+1, len(sessions), s.sessionID[:8])
			}
			continue
		}

		// Generate summary
		req := llm.SummaryRequest{
			SessionID:       s.sessionID,
			ProjectPath:     s.projectPath,
			Messages:        messages,
			ExistingSummary: s.summary,
		}

		summary, err := summarizer.Summarize(ctx, req)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to summarize %s: %v\n", s.sessionID, err)
			continue
		}

		// Clean up summary
		summary = strings.TrimSpace(summary)

		// Save to database
		if err := database.UpdateLLMSummary(s.sessionID, summary); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to save summary for %s: %v\n", s.sessionID, err)
			continue
		}

		if summarizeVerbose {
			fmt.Printf("[%d/%d] %s: %s\n", i+1, len(sessions), s.sessionID[:8], truncate(summary, 60))
		} else {
			fmt.Printf(".")
		}
	}

	if !summarizeVerbose {
		fmt.Println()
	}

	fmt.Println("Done!")
	return nil
}

func getSessionMessages(database *db.DB, sessionID string) ([]llm.Message, error) {
	rows, err := database.Query(`
		SELECT m.sender, m.text_content
		FROM messages m
		JOIN sessions s ON m.session_id = s.id
		WHERE s.session_id = ?
		ORDER BY m.sequence
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []llm.Message
	for rows.Next() {
		var sender, content string
		if err := rows.Scan(&sender, &content); err != nil {
			return nil, err
		}
		if content != "" {
			msgType := "user"
			if sender == "assistant" {
				msgType = "assistant"
			}
			messages = append(messages, llm.Message{
				Type:    msgType,
				Content: content,
			})
		}
	}

	return messages, nil
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
