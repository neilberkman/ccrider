package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/neilberkman/ccrider/internal/core/db"
	"github.com/neilberkman/ccrider/internal/core/llm"
	"github.com/neilberkman/ccrider/internal/core/search"
	"github.com/spf13/cobra"
)

var startFromCmd = &cobra.Command{
	Use:   "start-from [query]",
	Short: "Start a new session with context from previous work",
	Long: `Find relevant past sessions and start a new Claude Code session with that context.

Examples:
  ccrider start-from "ena-6530 performance improvements"
  ccrider start-from "postgres migration fixes"
  ccrider start-from "authentication refactor"`,
	Args: cobra.MinimumNArgs(1),
	RunE: runStartFrom,
}

var (
	startFromModel string
	startFromAuto  bool
)

func init() {
	rootCmd.AddCommand(startFromCmd)

	startFromCmd.Flags().StringVar(&startFromModel, "model", "llama-8b", "Model to use for AI search")
	startFromCmd.Flags().BoolVar(&startFromAuto, "auto", false, "Auto-generate prompt without confirmation")
}

func runStartFrom(cmd *cobra.Command, args []string) error {
	query := strings.Join(args, " ")

	// Open database
	database, err := db.New(dbPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer database.Close()

	fmt.Printf("Finding context for: \"%s\"\n\n", query)

	// Initialize LLM
	modelManager, err := llm.NewModelManager()
	if err != nil {
		return fmt.Errorf("failed to create model manager: %w", err)
	}

	modelPath, err := modelManager.EnsureModel(startFromModel)
	if err != nil {
		return fmt.Errorf("failed to ensure model: %w", err)
	}

	inference, err := llm.NewLLM(modelPath, startFromModel)
	if err != nil {
		return fmt.Errorf("failed to initialize LLM: %w", err)
	}
	defer inference.Close()

	// Search for relevant sessions
	searcher := search.NewSmartSearcher(database, inference)
	results, err := searcher.Search(context.Background(), query)
	if err != nil {
		return fmt.Errorf("search failed: %w", err)
	}

	if len(results) == 0 {
		fmt.Println("No relevant context found. Starting fresh session...")
		return launchClaude(query, "")
	}

	// Show top matches
	displayCount := len(results)
	if displayCount > 3 {
		displayCount = 3
	}

	fmt.Printf("Found %d relevant session(s):\n\n", len(results))
	for i := 0; i < displayCount; i++ {
		r := results[i]
		fmt.Printf("%d. %s\n", i+1, r.Session.SessionID)
		fmt.Printf("   Project: %s\n", r.Session.ProjectPath)
		if r.Session.Summary != "" {
			summary := r.Session.Summary
			if len(summary) > 150 {
				summary = summary[:147] + "..."
			}
			fmt.Printf("   Summary: %s\n", summary)
		}
		fmt.Println()
	}

	// Generate context prompt
	contextPrompt, err := generateContextPrompt(inference, results[:displayCount], query)
	if err != nil {
		return fmt.Errorf("failed to generate prompt: %w", err)
	}

	fmt.Println("────────────────────────────────────────────────────")
	fmt.Println("Suggested prompt:")
	fmt.Println("────────────────────────────────────────────────────")
	fmt.Println(contextPrompt)
	fmt.Println("────────────────────────────────────────────────────")
	fmt.Println()

	if !startFromAuto {
		fmt.Print("Start session with this prompt? (Y/n/edit): ")
		var response string
		fmt.Scanln(&response)
		response = strings.ToLower(strings.TrimSpace(response))

		if response == "n" {
			fmt.Println("Cancelled.")
			return nil
		}

		if response == "e" || response == "edit" {
			// Open in editor
			edited, err := editPrompt(contextPrompt)
			if err != nil {
				return fmt.Errorf("failed to edit prompt: %w", err)
			}
			contextPrompt = edited
		}
	}

	// Use project path as working directory
	workDir := results[0].Session.ProjectPath
	if workDir == "" {
		workDir, _ = os.Getwd()
	}

	return launchClaudeWithPrompt(contextPrompt, workDir)
}

func generateContextPrompt(llm *llm.LLM, results []search.SmartSearchResult, query string) (string, error) {
	var contextText strings.Builder

	contextText.WriteString("Previous work:\n\n")
	for i, r := range results {
		contextText.WriteString(fmt.Sprintf("%d. Session from %s\n", i+1, r.Session.UpdatedAt.Format("2006-01-02")))
		if r.Session.Summary != "" {
			summary := r.Session.Summary
			if len(summary) > 200 {
				summary = summary[:197] + "..."
			}
			contextText.WriteString(fmt.Sprintf("   %s\n\n", summary))
		}
	}

	// Simplified prompt that works better with small models
	prompt := fmt.Sprintf(`Write a short prompt to start working on: %s

Based on this previous work:
%s

Write 2-3 sentences describing what to work on next.`, query, contextText.String())

	ctx := context.Background()
	generated, err := llm.Generate(ctx, prompt, 512)
	if err != nil {
		return "", err
	}

	// Clean up the response
	generated = strings.TrimSpace(generated)

	// If empty or too short, generate a simple fallback
	if len(generated) < 20 {
		return fmt.Sprintf("Continue work on %s based on previous sessions", query), nil
	}

	return generated, nil
}

func editPrompt(prompt string) (string, error) {
	// Create temp file
	tmpfile, err := os.CreateTemp("", "ccrider-prompt-*.txt")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmpfile.Name())

	// Write prompt
	if _, err := tmpfile.WriteString(prompt); err != nil {
		return "", err
	}
	tmpfile.Close()

	// Open in editor
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "nano" // Default fallback
	}

	cmd := exec.Command(editor, tmpfile.Name())
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return "", err
	}

	// Read edited content
	edited, err := os.ReadFile(tmpfile.Name())
	if err != nil {
		return "", err
	}

	return string(edited), nil
}

func launchClaudeWithPrompt(prompt, workingDir string) error {
	// Build command with prompt
	cmd := exec.Command("claude", "code", "--prompt", prompt)

	// Set working directory if available
	if workingDir != "" {
		cmd.Dir = workingDir
	}

	// Connect to stdio
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	fmt.Printf("\nLaunching Claude Code...\n\n")

	// Run
	return cmd.Run()
}
