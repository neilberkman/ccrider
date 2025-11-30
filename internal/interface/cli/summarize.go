package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/neilberkman/ccrider/internal/core/db"
	"github.com/neilberkman/ccrider/internal/core/llm"
	"github.com/spf13/cobra"
)

// Env vars for Bedrock credentials (so you don't put them on command line)
// CCRIDER_AWS_ACCESS_KEY_ID
// CCRIDER_AWS_SECRET_ACCESS_KEY
// CCRIDER_AWS_REGION
// CCRIDER_AWS_PROFILE

var (
	summarizeLimit   int
	summarizeForce   bool
	summarizeModel   string
	summarizeRegion  string
	summarizeProfile string
	summarizeVerbose bool
	summarizeExtract bool
)

var summarizeCmd = &cobra.Command{
	Use:   "summarize",
	Short: "Generate LLM summaries for sessions",
	Long: `Generate hierarchical summaries for sessions using an LLM provider.

Features:
- Progressive chunk-based summarization for long sessions
- Two-tier summaries: one-line (for lists) and full (detailed)
- Metadata extraction: issue IDs (ENA-1234) and file paths
- Incremental updates when sessions grow

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
  ccrider summarize --model anthropic.claude-3-sonnet-20240229-v1:0

  # Extract metadata only (no LLM calls)
  ccrider summarize --extract-only`,
	RunE: runSummarize,
}

func init() {
	summarizeCmd.Flags().IntVarP(&summarizeLimit, "limit", "n", 10, "Number of sessions to summarize")
	summarizeCmd.Flags().BoolVarP(&summarizeForce, "force", "f", false, "Re-summarize sessions that already have summaries")
	summarizeCmd.Flags().StringVar(&summarizeModel, "model", "", "Bedrock model ID (default: claude-3-haiku)")
	summarizeCmd.Flags().StringVar(&summarizeRegion, "region", "", "AWS region (default: us-east-1)")
	summarizeCmd.Flags().BoolVarP(&summarizeVerbose, "verbose", "v", false, "Show verbose output")
	summarizeCmd.Flags().BoolVar(&summarizeExtract, "extract-only", false, "Only extract metadata (issue IDs, files), no LLM calls")

	rootCmd.AddCommand(summarizeCmd)
}

func runSummarize(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	// Open database
	database, err := db.New(dbPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer func() { _ = database.Close() }()

	// Get sessions that need processing
	sessions, err := getSummarizableSessions(database, summarizeLimit, summarizeForce)
	if err != nil {
		return fmt.Errorf("failed to query sessions: %w", err)
	}

	if len(sessions) == 0 {
		fmt.Println("No sessions to summarize")
		return nil
	}

	// Initialize components
	extractor := llm.NewMetadataExtractor()

	var summarizer *llm.HierarchicalSummarizer
	if !summarizeExtract {
		// Check env vars for AWS credentials
		region := summarizeRegion
		if region == "" {
			region = os.Getenv("CCRIDER_AWS_REGION")
		}
		accessKey := os.Getenv("AWS_ACCESS_KEY_ID")
		if v := os.Getenv("CCRIDER_AWS_ACCESS_KEY_ID"); v != "" {
			accessKey = v
		}
		secretKey := os.Getenv("AWS_SECRET_ACCESS_KEY")
		if v := os.Getenv("CCRIDER_AWS_SECRET_ACCESS_KEY"); v != "" {
			secretKey = v
		}
		profile := summarizeProfile
		if profile == "" {
			profile = os.Getenv("CCRIDER_AWS_PROFILE")
		}

		provider, err := llm.NewBedrockProvider(ctx, llm.BedrockConfig{
			Region:          region,
			ModelID:         summarizeModel,
			Profile:         profile,
			AccessKeyID:     accessKey,
			SecretAccessKey: secretKey,
		})
		if err != nil {
			return fmt.Errorf("failed to create LLM provider: %w", err)
		}
		summarizer = llm.NewHierarchicalSummarizer(provider)
		fmt.Printf("Summarizing %d sessions using %s...\n", len(sessions), provider.Name())
	} else {
		fmt.Printf("Extracting metadata from %d sessions...\n", len(sessions))
	}

	// Process each session
	var successCount, skipCount, errorCount int
	for i, s := range sessions {
		// Get messages for this session
		messages, err := getSessionMessages(database, s.sessionID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to get messages for %s: %v\n", s.sessionID, err)
			errorCount++
			continue
		}

		if len(messages) == 0 {
			if summarizeVerbose {
				fmt.Printf("[%d/%d] %s: no messages, skipping\n", i+1, len(sessions), truncateID(s.sessionID))
			}
			skipCount++
			continue
		}

		// Extract metadata (always do this)
		issues := extractor.ExtractIssues(messages)
		files := extractor.ExtractFiles(messages)

		// Save extracted metadata
		if len(issues) > 0 {
			for j := range issues {
				issues[j].SessionID = s.id
			}
			if err := database.SaveSessionIssues(s.id, issues); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to save issues for %s: %v\n", s.sessionID, err)
			}
		}
		if len(files) > 0 {
			for j := range files {
				files[j].SessionID = s.id
			}
			if err := database.SaveSessionFiles(s.id, files); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to save files for %s: %v\n", s.sessionID, err)
			}
		}

		// Generate summary (unless extract-only mode)
		if !summarizeExtract && summarizer != nil {
			req := llm.SummaryRequest{
				SessionID:   s.sessionID,
				ProjectPath: s.projectPath,
				Messages:    messages,
			}

			summary, err := summarizer.SummarizeSession(ctx, req)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to summarize %s: %v\n", s.sessionID, err)
				errorCount++
				continue
			}

			// Save summary
			summary.SessionID = s.id
			if err := database.SaveSessionSummary(*summary); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to save summary for %s: %v\n", s.sessionID, err)
				errorCount++
				continue
			}

			if summarizeVerbose {
				fmt.Printf("[%d/%d] %s: %s (issues:%d files:%d chunks:%d)\n",
					i+1, len(sessions), truncateID(s.sessionID),
					truncate(summary.OneLine, 50),
					len(issues), len(files), len(summary.ChunkSummaries))
			} else {
				fmt.Printf(".")
			}
		} else {
			if summarizeVerbose {
				fmt.Printf("[%d/%d] %s: extracted (issues:%d files:%d)\n",
					i+1, len(sessions), truncateID(s.sessionID),
					len(issues), len(files))
			} else {
				fmt.Printf(".")
			}
		}

		successCount++
	}

	if !summarizeVerbose {
		fmt.Println()
	}

	fmt.Printf("Done! Processed: %d, Skipped: %d, Errors: %d\n", successCount, skipCount, errorCount)
	return nil
}

type summarizeSessionInfo struct {
	id          int64
	sessionID   string
	projectPath string
}

func getSummarizableSessions(database *db.DB, limit int, force bool) ([]summarizeSessionInfo, error) {
	var query string
	if force {
		query = `
			SELECT s.id, s.session_id, s.project_path
			FROM sessions s
			WHERE s.message_count > 0
			ORDER BY s.updated_at DESC
			LIMIT ?
		`
	} else {
		query = `
			SELECT s.id, s.session_id, s.project_path
			FROM sessions s
			LEFT JOIN session_summaries ss ON s.id = ss.session_id
			WHERE s.message_count > 0 AND (ss.session_id IS NULL OR s.message_count > ss.last_message_count)
			ORDER BY s.updated_at DESC
			LIMIT ?
		`
	}

	rows, err := database.Query(query, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var sessions []summarizeSessionInfo
	for rows.Next() {
		var s summarizeSessionInfo
		if err := rows.Scan(&s.id, &s.sessionID, &s.projectPath); err != nil {
			return nil, err
		}
		sessions = append(sessions, s)
	}
	return sessions, nil
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
	defer func() { _ = rows.Close() }()

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

func truncateID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
