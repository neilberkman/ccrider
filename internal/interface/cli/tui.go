package cli

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/cbroglie/mustache"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/dustin/go-humanize"
	"github.com/neilberkman/ccrider/internal/core/config"
	"github.com/neilberkman/ccrider/internal/core/db"
	"github.com/neilberkman/ccrider/internal/core/session"
	"github.com/neilberkman/ccrider/internal/interface/tui"
	"github.com/spf13/cobra"
)

var tuiCmd = &cobra.Command{
	Use:   "tui",
	Short: "Launch interactive TUI browser",
	Long:  "Launch an interactive terminal UI for browsing and viewing Claude Code sessions",
	RunE:  runTUI,
}

func init() {
	rootCmd.AddCommand(tuiCmd)
}

func runTUI(cmd *cobra.Command, args []string) error {
	database, err := db.New(dbPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer func() { _ = database.Close() }()

	model := tui.New(database)
	p := tea.NewProgram(
		model,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)

	finalModel, err := p.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error running TUI: %v\n", err)
		os.Exit(1)
	}

	// Check if user wants to launch a session
	if m, ok := finalModel.(tui.Model); ok {
		if m.LaunchSessionID != "" {
			// Exec claude to replace this process
			return execClaude(
				m.LaunchSessionID,
				m.LaunchProjectPath,
				m.LaunchLastCwd,
				m.LaunchUpdatedAt,
				m.LaunchSummary,
				m.LaunchFork,
			)
		}
	}

	return nil
}

func execClaude(sessionID, projectPath, lastCwd, updatedAt, summary string, fork bool) error {
	// Check if session file exists - if not, use recovery mode
	if !session.SessionFileExists(sessionID, projectPath) {
		return execClaudeRecovery(sessionID, projectPath, lastCwd, updatedAt, summary)
	}

	// Load config to get resume prompt template
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Build template data
	updatedTime, _ := time.Parse("2006-01-02 15:04:05", updatedAt)
	if updatedTime.IsZero() {
		updatedTime, _ = time.Parse(time.RFC3339, updatedAt)
	}

	timeSince := "unknown"
	if !updatedTime.IsZero() {
		timeSince = humanize.Time(updatedTime)
	}

	// Check if we're already in the right directory
	sameDir := (lastCwd == projectPath)

	templateData := map[string]interface{}{
		"last_updated":        updatedAt,
		"last_cwd":            lastCwd,
		"time_since":          timeSince,
		"project_path":        projectPath,
		"same_directory":      sameDir,
		"different_directory": !sameDir,
	}

	// Render the resume prompt
	resumePrompt, err := mustache.Render(cfg.ResumePromptTemplate, templateData)
	if err != nil {
		// Fall back to simple prompt if template fails
		resumePrompt = fmt.Sprintf("Resuming session. You were last in: %s", lastCwd)
	}

	// Write prompt to temp file and pass via command substitution
	// This avoids all shell escaping issues
	tmpfile, err := os.CreateTemp("", "ccrider-prompt-*.txt")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	defer func() { _ = os.Remove(tmpfile.Name()) }()

	if _, err := tmpfile.Write([]byte(resumePrompt)); err != nil {
		_ = tmpfile.Close()
		return fmt.Errorf("failed to write prompt: %w", err)
	}
	_ = tmpfile.Close()

	// Build claude command with prompt from file
	var cmd string
	flags := ""
	if len(cfg.ClaudeFlags) > 0 {
		flags = " " + strings.Join(cfg.ClaudeFlags, " ")
	}
	if fork {
		cmd = fmt.Sprintf("claude%s --resume %s --fork-session \"$(cat %s)\"", flags, sessionID, tmpfile.Name())
	} else {
		cmd = fmt.Sprintf("claude%s --resume %s \"$(cat %s)\"", flags, sessionID, tmpfile.Name())
	}

	// Resolve working directory (always projectPath, see session.ResolveWorkingDir)
	workDir := session.ResolveWorkingDir(projectPath, lastCwd)

	// Show what we're doing
	fmt.Fprintf(os.Stderr, "[ccrider] cd %s && %s\n", workDir, cmd)

	// Set terminal title before launching
	if !updatedTime.IsZero() && summary != "" {
		// Format: [resumed MM/DD HH:MM] summary
		titleTime := updatedTime.Format("01/02 15:04")
		title := fmt.Sprintf("[resumed %s] %s", titleTime, summary)
		// Set terminal title using escape sequence
		fmt.Fprintf(os.Stderr, "\033]0;%s\007", title)
	}

	// Start spinner (Claude Code can take a few seconds to start)
	spinner := session.NewSpinner("Starting Claude Code (this may take a few seconds)...")
	spinner.Start()
	defer spinner.Stop()

	// Give spinner a moment to show before exec
	time.Sleep(100 * time.Millisecond)

	// Change to working directory
	if workDir != "" {
		if err := os.Chdir(workDir); err != nil {
			return fmt.Errorf("failed to cd to %s: %w", workDir, err)
		}
	}

	// Find shell
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}

	// Pre-flight check: verify claude is runnable from this directory
	if err := session.ValidateClaudeRunnable(workDir); err != nil {
		spinner.Stop()
		return err
	}

	// Exec shell with claude command (replaces current process)
	// Use -c to run the command, -l to make it a login shell (loads asdf/mise)
	return syscall.Exec(shell, []string{shell, "-l", "-c", cmd}, os.Environ())
}

// execClaudeRecovery starts a new Claude session with context from an old session
// whose file is missing. It provides context to Claude via the ccrider MCP server.
func execClaudeRecovery(sessionID, projectPath, lastCwd, updatedAt, summary string) error {
	// Explain what's happening to the user
	fmt.Fprintf(os.Stderr, "\n")
	fmt.Fprintf(os.Stderr, "╭─────────────────────────────────────────────────────────────╮\n")
	fmt.Fprintf(os.Stderr, "│  SESSION RECOVERY MODE                                      │\n")
	fmt.Fprintf(os.Stderr, "╰─────────────────────────────────────────────────────────────╯\n")
	fmt.Fprintf(os.Stderr, "\n")
	fmt.Fprintf(os.Stderr, "The original session file has been deleted by Claude Code,\n")
	fmt.Fprintf(os.Stderr, "but CCRider has preserved the conversation in its database.\n")
	fmt.Fprintf(os.Stderr, "\n")
	fmt.Fprintf(os.Stderr, "Session: %s\n", sessionID)
	fmt.Fprintf(os.Stderr, "Summary: %s\n", summary)
	fmt.Fprintf(os.Stderr, "\n")
	fmt.Fprintf(os.Stderr, "CCRider can start a NEW session with context from the old one.\n")
	fmt.Fprintf(os.Stderr, "The new Claude session will be able to search the old conversation\n")
	fmt.Fprintf(os.Stderr, "using the CCRider MCP server.\n")
	fmt.Fprintf(os.Stderr, "\n")

	// Check if ccrider MCP is likely configured
	home, _ := os.UserHomeDir()
	claudeSettingsPath := home + "/.claude.json"
	hasMCP := false
	if data, err := os.ReadFile(claudeSettingsPath); err == nil {
		hasMCP = strings.Contains(string(data), "ccrider")
	}

	if !hasMCP {
		fmt.Fprintf(os.Stderr, "⚠️  WARNING: CCRider MCP server may not be configured.\n")
		fmt.Fprintf(os.Stderr, "   Without it, Claude won't be able to search the old session.\n")
		fmt.Fprintf(os.Stderr, "   To install: ccrider mcp install\n")
		fmt.Fprintf(os.Stderr, "\n")
	}

	// Prompt user to continue
	fmt.Fprintf(os.Stderr, "Continue with recovery session? [Y/n] ")
	var response string
	_, _ = fmt.Scanln(&response)
	response = strings.ToLower(strings.TrimSpace(response))
	if response != "" && response != "y" && response != "yes" {
		fmt.Fprintf(os.Stderr, "Aborted.\n")
		return nil
	}

	// Determine working directory
	workDir := ""

	// Try lastCwd first
	if lastCwd != "" {
		if _, err := os.Stat(lastCwd); err == nil {
			workDir = lastCwd
		}
	}

	// Try projectPath
	if workDir == "" && projectPath != "" {
		if _, err := os.Stat(projectPath); err == nil {
			workDir = projectPath
		}
	}

	// If neither exists, ask user
	if workDir == "" {
		fmt.Fprintf(os.Stderr, "\n")
		fmt.Fprintf(os.Stderr, "Neither the project directory nor the last working directory exist:\n")
		fmt.Fprintf(os.Stderr, "  Project: %s\n", projectPath)
		if lastCwd != "" && lastCwd != projectPath {
			fmt.Fprintf(os.Stderr, "  Last CWD: %s\n", lastCwd)
		}
		fmt.Fprintf(os.Stderr, "\n")
		fmt.Fprintf(os.Stderr, "Enter directory to start session in (or press Enter for current dir): ")
		_, _ = fmt.Scanln(&workDir)
		workDir = strings.TrimSpace(workDir)

		if workDir == "" {
			workDir, _ = os.Getwd()
		}

		// Expand ~ if present
		if strings.HasPrefix(workDir, "~/") {
			workDir = home + workDir[1:]
		}

		// Verify it exists
		if _, err := os.Stat(workDir); err != nil {
			return fmt.Errorf("directory does not exist: %s", workDir)
		}
	}

	// Open database to get recovery context
	database, err := db.New(dbPath)
	if err != nil {
		return fmt.Errorf("failed to open database for recovery: %w", err)
	}
	defer func() { _ = database.Close() }()

	// Get recovery context with first/last 5 messages
	ctx, err := database.GetRecoveryContext(sessionID, 5)
	if err != nil {
		return fmt.Errorf("failed to get recovery context: %w", err)
	}

	// Build the recovery prompt
	recoveryPrompt := buildRecoveryPrompt(ctx)

	// Load config for claude flags
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Write prompt to temp file
	tmpfile, err := os.CreateTemp("", "ccrider-recovery-*.txt")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	defer func() { _ = os.Remove(tmpfile.Name()) }()

	if _, err := tmpfile.Write([]byte(recoveryPrompt)); err != nil {
		_ = tmpfile.Close()
		return fmt.Errorf("failed to write prompt: %w", err)
	}
	_ = tmpfile.Close()

	// Build claude command (new session, not --resume)
	flags := ""
	if len(cfg.ClaudeFlags) > 0 {
		flags = " " + strings.Join(cfg.ClaudeFlags, " ")
	}
	cmd := fmt.Sprintf("claude%s \"$(cat %s)\"", flags, tmpfile.Name())

	fmt.Fprintf(os.Stderr, "\n")
	fmt.Fprintf(os.Stderr, "[ccrider] Starting recovery session in: %s\n", workDir)

	// Set terminal title
	title := fmt.Sprintf("[recovery] %s", summary)
	fmt.Fprintf(os.Stderr, "\033]0;%s\007", title)

	// Start spinner
	spinner := session.NewSpinner("Starting Claude Code recovery session...")
	spinner.Start()
	defer spinner.Stop()

	time.Sleep(100 * time.Millisecond)

	// Change to working directory
	if err := os.Chdir(workDir); err != nil {
		spinner.Stop()
		return fmt.Errorf("failed to cd to %s: %w", workDir, err)
	}

	// Find shell
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}

	// Pre-flight check
	if err := session.ValidateClaudeRunnable(workDir); err != nil {
		spinner.Stop()
		return err
	}

	// Exec claude (new session)
	return syscall.Exec(shell, []string{shell, "-l", "-c", cmd}, os.Environ())
}

// buildRecoveryPrompt creates a prompt that helps Claude understand the context
// from a previous session whose file is missing
func buildRecoveryPrompt(ctx *db.RecoveryContext) string {
	var sb strings.Builder

	sb.WriteString("# Session Recovery Mode\n\n")
	sb.WriteString("The original session file is no longer available, but CCRider has preserved context from that session.\n\n")

	sb.WriteString("## Previous Session Info\n")
	sb.WriteString(fmt.Sprintf("- **Session ID**: %s\n", ctx.SessionID))
	sb.WriteString(fmt.Sprintf("- **Summary**: %s\n", ctx.Summary))
	sb.WriteString(fmt.Sprintf("- **Project**: %s\n", ctx.ProjectPath))
	if ctx.LastCwd != "" && ctx.LastCwd != ctx.ProjectPath {
		sb.WriteString(fmt.Sprintf("- **Last Working Directory**: %s\n", ctx.LastCwd))
	}
	sb.WriteString(fmt.Sprintf("- **Message Count**: %d\n", ctx.MessageCount))
	sb.WriteString(fmt.Sprintf("- **Last Updated**: %s (%s)\n", ctx.UpdatedAt.Format("2006-01-02 15:04"), humanize.Time(ctx.UpdatedAt)))

	// First messages
	if len(ctx.FirstMsgs) > 0 {
		sb.WriteString("\n## How the Session Started\n\n")
		for _, msg := range ctx.FirstMsgs {
			prefix := "User"
			if msg.Type == "assistant" {
				prefix = "Assistant"
			}
			content := msg.Content
			if len(content) > 500 {
				content = content[:500] + "..."
			}
			sb.WriteString(fmt.Sprintf("**%s**: %s\n\n", prefix, content))
		}
	}

	// Last messages
	if len(ctx.LastMsgs) > 0 {
		sb.WriteString("\n## Where the Session Left Off\n\n")
		for _, msg := range ctx.LastMsgs {
			prefix := "User"
			if msg.Type == "assistant" {
				prefix = "Assistant"
			}
			content := msg.Content
			if len(content) > 500 {
				content = content[:500] + "..."
			}
			sb.WriteString(fmt.Sprintf("**%s**: %s\n\n", prefix, content))
		}
	}

	sb.WriteString("\n## How to Continue\n\n")
	sb.WriteString("You have access to the CCRider MCP server. To find more context from the old session:\n\n")
	sb.WriteString(fmt.Sprintf("1. Search for specific topics: `mcp__ccrider__search_sessions` with query and current_session_id `%s`\n", ctx.SessionID))
	sb.WriteString(fmt.Sprintf("2. Get more messages: `mcp__ccrider__get_session_messages` with session_id `%s` and `last_n` or `around_sequence`\n", ctx.SessionID))
	sb.WriteString("\n**Ask the user what they'd like to continue working on, then search the old session for relevant context.**\n")

	return sb.String()
}
