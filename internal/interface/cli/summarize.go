package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/neilberkman/ccrider/internal/core/config"
	"github.com/neilberkman/ccrider/internal/core/db"
	"github.com/neilberkman/ccrider/internal/core/llm"
	"github.com/spf13/cobra"
)

// Env vars for credentials:
// ANTHROPIC_API_KEY, ANTHROPIC_BASE_URL - Anthropic API or a compatible server
// OPENAI_API_KEY, OPENAI_BASE_URL - OpenAI API or a compatible server
// AWS credentials - standard AWS env vars or CCRIDER_AWS_* variants

var (
	summarizeLimit    int
	summarizeForce    bool
	summarizeModel    string
	summarizeRegion   string
	summarizeVerbose  bool
	summarizeExtract  bool
	summarizeProvider string
	summarizeBaseURL  string
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

Providers:
- anthropic: Anthropic API (ANTHROPIC_API_KEY), or any Anthropic-compatible
  server via --base-url / ANTHROPIC_BASE_URL. Default model: Claude Haiku 4.5
  (` + llm.DefaultAnthropicModel + `)
- bedrock: AWS Bedrock (AWS env vars, profile, or IAM role). Default model:
  Claude Haiku 4.5 (` + llm.DefaultBedrockModel + `)
- openai: OpenAI API (OPENAI_API_KEY), or any OpenAI-compatible server via
  --base-url / OPENAI_BASE_URL: Ollama, LM Studio, llama.cpp, vLLM, LocalAI.
  No API key is needed for a local server. Requires --model
- codex: runs ` + "`codex exec`" + ` with your Codex CLI login, so a ChatGPT plan's
  included usage pays for summaries instead of API billing. Uses the Codex
  default model unless --model is given. Never auto-detected

Without --provider or llm_provider in ~/.config/ccrider/config.toml, the
provider is auto-detected from credentials: Anthropic, then AWS, then OpenAI.

config.toml keys: llm_provider, llm_model, llm_base_url. Precedence: flag,
then environment, then config.toml. llm_model and llm_base_url are ignored
when --provider picks a different provider than llm_provider.

Examples:
  # Summarize sessions (auto-detects provider)
  ccrider summarize

  # Use specific provider
  ccrider summarize --provider anthropic
  ccrider summarize --provider bedrock

  # Use your ChatGPT plan through the Codex CLI
  ccrider summarize --provider codex

  # Use a local model through Ollama (session text stays on this machine)
  ccrider summarize --provider openai --base-url http://localhost:11434/v1 --model qwen3

  # Summarize more sessions
  ccrider summarize --limit 50

  # Re-summarize all sessions (overwrite existing)
  ccrider summarize --force --limit 100

  # Extract metadata only (no LLM calls)
  ccrider summarize --extract-only`,
	RunE: runSummarize,
}

func init() {
	summarizeCmd.Flags().IntVarP(&summarizeLimit, "limit", "n", 10, "Number of sessions to summarize")
	summarizeCmd.Flags().BoolVarP(&summarizeForce, "force", "f", false, "Re-summarize sessions that already have summaries")
	summarizeCmd.Flags().StringVar(&summarizeProvider, "provider", "", "LLM provider: anthropic, bedrock, openai, or codex (auto-detected if not set)")
	summarizeCmd.Flags().StringVar(&summarizeModel, "model", "", "Model ID (provider-specific; anthropic and bedrock default to Claude Haiku 4.5)")
	summarizeCmd.Flags().StringVar(&summarizeBaseURL, "base-url", "", "Endpoint for the openai or anthropic provider, e.g. http://localhost:11434/v1 for Ollama")
	summarizeCmd.Flags().StringVar(&summarizeRegion, "region", "", "AWS region for Bedrock (default: us-east-1)")
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
		provider, err := createLLMProvider(ctx)
		if err != nil {
			return fmt.Errorf("failed to create LLM provider: %w", err)
		}
		summarizer = llm.NewHierarchicalSummarizer(provider)
		fmt.Printf("Summarizing %d sessions...\n", len(sessions))
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
	// Minimum 5 messages to be worth summarizing
	minMessages := 5
	if force {
		query = `
			SELECT s.id, s.session_id, s.project_path
			FROM sessions s
			WHERE s.message_count >= ?
			ORDER BY s.updated_at DESC
			LIMIT ?
		`
	} else {
		query = `
			SELECT s.id, s.session_id, s.project_path
			FROM sessions s
			LEFT JOIN session_summaries ss ON s.id = ss.session_id
			WHERE s.message_count >= ? AND (ss.session_id IS NULL OR s.message_count > ss.last_message_count)
			ORDER BY s.updated_at DESC
			LIMIT ?
		`
	}
	rows, err := database.Query(query, minMessages, limit)
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

// createLLMProvider resolves the provider from flags, config and environment,
// then reports which one is in use.
func createLLMProvider(ctx context.Context) (llm.Provider, error) {
	var file llm.Settings
	if cfg, err := config.Load(); err == nil && cfg != nil {
		file = llm.Settings{Provider: cfg.LLMProvider, Model: cfg.LLMModel, BaseURL: cfg.LLMBaseURL}
	}
	flags := llm.Settings{
		Provider: summarizeProvider,
		Model:    summarizeModel,
		BaseURL:  summarizeBaseURL,
		Region:   summarizeRegion,
	}

	resolved, err := llm.Resolve(flags, file, os.Getenv)
	if err != nil {
		return nil, err
	}
	printProviderChoice(resolved)
	return llm.NewProvider(ctx, resolved)
}

func printProviderChoice(r *llm.Resolved) {
	switch {
	case r.Anthropic != nil && r.Anthropic.BaseURL != "":
		fmt.Printf("Using Anthropic-compatible API at %s (%s)\n", llm.NormalizeAnthropicBaseURL(r.Anthropic.BaseURL), r.Anthropic.ModelID)
		if r.Anthropic.ModelID == llm.DefaultAnthropicModel {
			fmt.Printf("  Local servers usually need their own model name: --model or llm_model in config.toml\n")
		}
	case r.Anthropic != nil:
		fmt.Printf("Using Anthropic API (%s)\n", r.Anthropic.ModelID)
	case r.Bedrock != nil:
		fmt.Printf("Using AWS Bedrock (%s, %s)\n", r.Bedrock.ModelID, r.Bedrock.Region)
	case r.OpenAI != nil:
		baseURL := r.OpenAI.BaseURL
		if baseURL == "" {
			baseURL = llm.DefaultOpenAIBaseURL
		}
		fmt.Printf("Using OpenAI-compatible API at %s (%s)\n", baseURL, r.OpenAI.ModelID)
	case r.Codex != nil:
		model := r.Codex.ModelID
		if model == "" {
			model = "Codex default model"
		}
		fmt.Printf("Using Codex CLI login (%s)\n", model)
	}
	if r.AutoDetected {
		fmt.Printf("  Auto-detected from credentials; choose another with --provider (%s) or llm_provider in config.toml\n",
			strings.Join(llm.ProviderNames, ", "))
	}
	fmt.Println()
}
