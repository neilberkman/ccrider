package tui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	"github.com/cbroglie/mustache"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/dustin/go-humanize"
	"github.com/neilberkman/ccrider/internal/core/config"
	"github.com/neilberkman/ccrider/internal/core/session"
	"github.com/neilberkman/ccrider/internal/core/terminal"
)

// buildClaudeCommand creates a claude --resume command with configured flags
func buildClaudeCommand(sessionID, workDir string, withPrompt bool) string {
	cfg, _ := config.Load()

	claudeCmd := "claude"
	if cfg != nil && len(cfg.ClaudeFlags) > 0 {
		claudeCmd += " " + strings.Join(cfg.ClaudeFlags, " ")
	}
	claudeCmd += " --resume " + sessionID

	if withPrompt {
		claudeCmd += " '%s'" // Placeholder for prompt
	}

	if workDir != "" {
		return fmt.Sprintf("cd %s && %s", workDir, claudeCmd)
	}
	return claudeCmd
}

func (m Model) updateDetail(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// If search is active, pass ALL keys to pager (so typing works)
	if m.pager.search.active {
		updatedPager, cmd := m.pager.Update(msg)
		m.pager = updatedPager.(pagerModel)
		return m, cmd
	}

	// Handle custom keys (that pager doesn't use) when NOT in search mode
	switch msg.String() {
	case "r":
		// Resume session in Claude Code
		if m.currentSession != nil {
			return m, launchClaudeSession(
				m.currentSession.Session.ID,
				m.currentSession.Session.Project,
				m.currentSession.LastCwd,
				m.currentSession.UpdatedAt,
				m.currentSession.Session.Summary,
				false,
			)
		}
		return m, nil

	case "f":
		// Fork session (resume with new session ID)
		if m.currentSession != nil {
			return m, launchClaudeSession(
				m.currentSession.Session.ID,
				m.currentSession.Session.Project,
				m.currentSession.LastCwd,
				m.currentSession.UpdatedAt,
				m.currentSession.Session.Summary,
				true,
			)
		}
		return m, nil

	case "c":
		// Copy resume command to clipboard
		if m.currentSession != nil {
			return m, copyResumeCommand(
				m.currentSession.Session.ID,
				m.currentSession.Session.Project,
				m.currentSession.LastCwd,
			)
		}
		return m, nil

	case "o":
		// Open in new terminal window
		if m.currentSession != nil {
			m.err = nil // Clear any previous errors
			return m, openInNewTerminal(
				m.currentSession.Session.ID,
				m.currentSession.Session.Project,
				m.currentSession.LastCwd,
				m.currentSession.UpdatedAt,
				m.currentSession.Session.Summary,
			)
		}
		return m, nil

	case "e":
		// Quick export to current directory
		if m.currentSession != nil {
			return m, exportSession(m.db, m.currentSession.Session.ID)
		}
		return m, nil

	case "E":
		// Export with custom filename (save as)
		if m.currentSession != nil {
			return m, exportSession(m.db, m.currentSession.Session.ID)
		}
		return m, nil
	}

	// Pass everything else (including /, q, esc, n, p, navigation) to the pager
	updatedPager, cmd := m.pager.Update(msg)
	m.pager = updatedPager.(pagerModel)

	// If pager wants to quit (q or esc when not searching), go back to list instead of quitting app
	// We can't easily check the cmd type, so just let it through and handle q/esc in model.go
	return m, cmd
}

type sessionLaunchedMsg struct {
	success     bool
	message     string
	err         error
	sessionID   string
	projectPath string
	lastCwd     string
	updatedAt   string
	summary     string
	fork        bool
}

func launchClaudeSession(sessionID, projectPath, lastCwd, updatedAt, summary string, fork bool) tea.Cmd {
	return func() tea.Msg {
		// We need to exec() to replace the process, but bubbletea makes this tricky
		// Instead, we'll return a special message telling the TUI to quit,
		// then the CLI layer will exec claude
		return sessionLaunchedMsg{
			success:     true,
			message:     fmt.Sprintf("cd %s && claude --resume %s", projectPath, sessionID),
			sessionID:   sessionID,
			projectPath: projectPath,
			lastCwd:     lastCwd,
			updatedAt:   updatedAt,
			summary:     summary,
			fork:        fork,
		}
	}
}

func copyResumeCommand(sessionID, projectPath, lastCwd string) tea.Cmd {
	return copyResumeCommandWithContext(sessionID, projectPath, lastCwd, false)
}

func copyResumeCommandWithContext(sessionID, projectPath, lastCwd string, fromFallbackView bool) tea.Cmd {
	return func() tea.Msg {
		// Resolve working directory (always projectPath, see session.ResolveWorkingDir)
		workDir := session.ResolveWorkingDir(projectPath, lastCwd)

		// Create a command that cd's to the working directory and runs claude
		var cmd string
		if workDir != "" {
			cmd = fmt.Sprintf("cd %s && claude --resume %s", workDir, sessionID)
		} else {
			cmd = fmt.Sprintf("claude --resume %s", sessionID)
		}

		// Use cross-platform clipboard library
		err := clipboard.WriteAll(cmd)
		if err != nil {
			// Fallback: show the command with context-appropriate message
			var message string
			if fromFallbackView {
				message = "NoClipboard: " + cmd
			} else {
				message = "Command: " + cmd
			}
			return sessionLaunchedMsg{
				success: false,
				message: message,
				err:     err,
			}
		}

		return sessionLaunchedMsg{
			success: true,
			message: "Resume command copied to clipboard!",
		}
	}
}

func writeCommandToFile(sessionID, projectPath, lastCwd string) tea.Cmd {
	return func() tea.Msg {
		// Resolve working directory
		workDir := session.ResolveWorkingDir(projectPath, lastCwd)

		// Create command
		var cmd string
		if workDir != "" {
			cmd = fmt.Sprintf("cd %s && claude --resume %s", workDir, sessionID)
		} else {
			cmd = fmt.Sprintf("claude --resume %s", sessionID)
		}

		// Write to file
		filePath := "/tmp/ccrider-cmd.sh"
		content := fmt.Sprintf("#!/bin/bash\n%s\n", cmd)
		err := os.WriteFile(filePath, []byte(content), 0755)
		if err != nil {
			return sessionLaunchedMsg{
				success: false,
				message: fmt.Sprintf("Failed to write file: %v", err),
				err:     err,
			}
		}

		return sessionLaunchedMsg{
			success: false, // Don't quit
			message: fmt.Sprintf("Command written to %s", filePath),
		}
	}
}

type terminalSpawnedMsg struct {
	success     bool
	message     string
	err         error
	sessionID   string
	projectPath string
	lastCwd     string
	updatedAt   string
	summary     string
}

func openInNewTerminal(sessionID, projectPath, lastCwd, updatedAt, summary string) tea.Cmd {
	return func() tea.Msg {
		// Load config to get terminal command and resume prompt template
		cfg, err := config.Load()
		if err != nil {
			return terminalSpawnedMsg{
				success: false,
				message: "Failed to load config",
				err:     fmt.Errorf("config load: %w", err),
			}
		}

		// Build template data for resume prompt
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

		// Replace newlines with spaces for shell command
		resumePrompt = strings.ReplaceAll(resumePrompt, "\n", " ")
		resumePrompt = strings.ReplaceAll(resumePrompt, "\r", " ")

		// Build the full command that will run in the new terminal
		// Use shell with the prompt as an argument to claude
		shellCmd := fmt.Sprintf("claude --resume %s '%s'", sessionID, resumePrompt)

		// Resolve working directory (always projectPath, see session.ResolveWorkingDir)
		workDir := session.ResolveWorkingDir(projectPath, lastCwd)

		// Create spawner with custom command from config
		spawner := &terminal.Spawner{
			CustomCommand: cfg.TerminalCommand,
		}

		// Spawn new terminal window
		spawnCfg := terminal.SpawnConfig{
			WorkingDir: workDir,
			Command:    shellCmd,
			Message:    "Starting Claude Code (this may take a few seconds)...",
		}

		fmt.Fprintf(os.Stderr, "[DEBUG openInNewTerminal] About to spawn terminal\n")
		fmt.Fprintf(os.Stderr, "[DEBUG openInNewTerminal] WorkingDir: %s\n", workDir)
		fmt.Fprintf(os.Stderr, "[DEBUG openInNewTerminal] Command: %s\n", shellCmd)

		if err := spawner.Spawn(spawnCfg); err != nil {
			fmt.Fprintf(os.Stderr, "[DEBUG openInNewTerminal] Spawn failed: %v\n", err)
			return terminalSpawnedMsg{
				success:     false,
				err:         err,
				sessionID:   sessionID,
				projectPath: projectPath,
				lastCwd:     lastCwd,
				updatedAt:   updatedAt,
				summary:     summary,
			}
		}

		fmt.Fprintf(os.Stderr, "[DEBUG openInNewTerminal] Spawn succeeded\n")

		return terminalSpawnedMsg{
			success: true,
		}
	}
}

func (m Model) viewDetail() string {
	if m.currentSession == nil {
		return "No session loaded"
	}

	// Get the pager's view (includes search box when active)
	content := m.pager.View()

	// Add custom footer with action keys (only when NOT in search mode)
	if !m.pager.search.active {
		footer := "\ne: export | r: resume | f: fork | o: open in new terminal | c: copy"
		content += footer
	}

	return content
}
