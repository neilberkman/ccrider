package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/cbroglie/mustache"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/dustin/go-humanize"
	"github.com/yourusername/ccrider/internal/core/config"
	"github.com/yourusername/ccrider/internal/core/session"
	"github.com/yourusername/ccrider/internal/core/terminal"
)

func createViewport(detail sessionDetail, width, height int) viewport.Model {
	vp := viewport.New(width, height-8)
	vp.SetContent(renderConversation(detail))
	return vp
}

func renderConversation(detail sessionDetail) string {
	var b strings.Builder

	// Header
	b.WriteString(titleStyle.Render("Session: "+detail.Session.Summary) + "\n")
	b.WriteString(fmt.Sprintf("Project: %s\n", detail.Session.Project))
	b.WriteString(fmt.Sprintf("Messages: %d\n", detail.Session.MessageCount))
	b.WriteString(strings.Repeat("─", 80) + "\n\n")

	// Messages
	for _, msg := range detail.Messages {
		var style lipgloss.Style
		var label string

		switch msg.Type {
		case "user":
			style = userStyle
			label = "USER"
		case "assistant":
			style = assistantStyle
			label = "ASSISTANT"
		case "system":
			style = systemStyle
			label = "SYSTEM"
		default:
			style = lipgloss.NewStyle()
			label = strings.ToUpper(msg.Type)
		}

		b.WriteString(style.Render(fmt.Sprintf("▸ %s", label)))
		b.WriteString(" ")
		b.WriteString(timestampStyle.Render(formatTime(msg.Timestamp)))
		b.WriteString("\n")

		// Content (truncate if too long for preview)
		content := msg.Content
		if len(content) > 500 {
			content = content[:500] + "\n... (truncated)"
		}
		b.WriteString(content)
		b.WriteString("\n\n")
		b.WriteString(strings.Repeat("─", 80) + "\n\n")
	}

	return b.String()
}

func (m Model) updateDetail(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Handle in-session search mode
	if m.inSessionSearchMode {
		switch msg.String() {
		case "esc":
			m.inSessionSearchMode = false
			m.inSessionSearch.SetValue("")
			m.inSessionMatches = nil
			m.inSessionMatchIdx = 0
			return m, nil

		case "enter":
			// Perform search
			query := m.inSessionSearch.Value()
			if query != "" && m.currentSession != nil {
				m.inSessionMatches = findMatches(m.currentSession.Messages, query)
				m.inSessionMatchIdx = 0
			}
			return m, nil

		case "ctrl+n", "n":
			// Next match
			if len(m.inSessionMatches) > 0 {
				m.inSessionMatchIdx++
				if m.inSessionMatchIdx >= len(m.inSessionMatches) {
					m.inSessionMatchIdx = 0
				}
			}
			return m, nil

		case "ctrl+p", "p":
			// Previous match
			if len(m.inSessionMatches) > 0 {
				m.inSessionMatchIdx--
				if m.inSessionMatchIdx < 0 {
					m.inSessionMatchIdx = len(m.inSessionMatches) - 1
				}
			}
			return m, nil

		default:
			var cmd tea.Cmd
			m.inSessionSearch, cmd = m.inSessionSearch.Update(msg)
			return m, cmd
		}
	}

	// Normal detail view navigation
	switch msg.String() {
	case "esc", "q":
		m.mode = listView
		return m, nil

	case "r":
		// Resume session in Claude Code
		if m.currentSession != nil {
			return m, launchClaudeSession(
				m.currentSession.Session.ID,
				m.currentSession.Session.Project,
				m.currentSession.LastCwd,
				m.currentSession.UpdatedAt,
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
			)
		}
		return m, nil

	case "ctrl+f", "/":
		m.inSessionSearchMode = true
		m.inSessionSearch.Focus()
		return m, nil

	case "j", "down":
		m.viewport.LineDown(1)
		return m, nil

	case "k", "up":
		m.viewport.LineUp(1)
		return m, nil

	case "d":
		m.viewport.HalfViewDown()
		return m, nil

	case "u":
		m.viewport.HalfViewUp()
		return m, nil

	case "g":
		m.viewport.GotoTop()
		return m, nil

	case "G":
		m.viewport.GotoBottom()
		return m, nil
	}

	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

func findMatches(messages []messageItem, query string) []int {
	var matches []int
	lowerQuery := strings.ToLower(query)

	for i, msg := range messages {
		if strings.Contains(strings.ToLower(msg.Content), lowerQuery) {
			matches = append(matches, i)
		}
	}

	return matches
}

type sessionLaunchedMsg struct {
	success     bool
	message     string
	err         error
	sessionID   string
	projectPath string
	lastCwd     string
	updatedAt   string
	fork        bool
}

func launchClaudeSession(sessionID, projectPath, lastCwd, updatedAt string, fork bool) tea.Cmd {
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
			fork:        fork,
		}
	}
}

func copyResumeCommand(sessionID, projectPath, lastCwd string) tea.Cmd {
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

		// Try to copy to clipboard using pbcopy (macOS)
		clipCmd := exec.Command("pbcopy")
		clipCmd.Stdin = strings.NewReader(cmd)
		err := clipCmd.Run()

		if err != nil {
			// Fallback: just show the command
			return sessionLaunchedMsg{
				success: false,
				message: "Command: " + cmd,
				err:     err,
			}
		}

		return sessionLaunchedMsg{
			success: true,
			message: "Resume command copied to clipboard!",
		}
	}
}

type terminalSpawnedMsg struct {
	success bool
	message string
	err     error
}

func openInNewTerminal(sessionID, projectPath, lastCwd, updatedAt string) tea.Cmd {
	return func() tea.Msg {
		// WORKAROUND: Claude Code's --resume only finds sessions in top-level ~/.claude/projects/
		// but sessions are stored in project subdirectories. Create a symlink to make it accessible.
		if err := ensureSessionSymlink(sessionID); err != nil {
			return terminalSpawnedMsg{
				success: false,
				message: "Failed to prepare session",
				err:     fmt.Errorf("ensure session symlink: %w", err),
			}
		}

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
			"last_updated":     updatedAt,
			"last_cwd":         lastCwd,
			"time_since":       timeSince,
			"project_path":     projectPath,
			"same_directory":   sameDir,
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

		if err := spawner.Spawn(spawnCfg); err != nil {
			return terminalSpawnedMsg{
				success: false,
				err:     err,
			}
		}

		return terminalSpawnedMsg{
			success: true,
		}
	}
}

func (m Model) viewDetail() string {
	if m.currentSession == nil {
		return "No session loaded"
	}

	content := m.viewport.View()

	// Add search box if in search mode
	if m.inSessionSearchMode {
		searchBox := "\n" + m.inSessionSearch.View()
		if len(m.inSessionMatches) > 0 {
			searchBox += fmt.Sprintf(" [%d/%d matches]", m.inSessionMatchIdx+1, len(m.inSessionMatches))
		} else if m.inSessionSearch.Value() != "" {
			searchBox += " [no matches]"
		}
		searchBox += "\nn/p: next/prev match | Enter: search | esc: exit search"
		content += searchBox
	} else {
		footer := fmt.Sprintf("\n%3.f%%", m.viewport.ScrollPercent()*100)
		footer += "\n\nr: resume | f: fork | o: open in new terminal | c: copy | /: search | j/k: scroll | esc: back | q: quit"
		content += footer
	}

	return content
}

// ensureSessionSymlink creates a symlink to the session file in the top-level projects directory
// so that `claude --resume` can find it (Claude Code bug: only looks in top level, not subdirs)
func ensureSessionSymlink(sessionID string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("get home dir: %w", err)
	}

	projectsDir := filepath.Join(home, ".claude", "projects")
	targetPath := filepath.Join(projectsDir, sessionID+".jsonl")

	// If symlink/file already exists at top level, we're done
	if _, err := os.Lstat(targetPath); err == nil {
		return nil
	}

	// Find the actual session file in subdirectories
	var sourcePath string
	err = filepath.Walk(projectsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors
		}
		if !info.IsDir() && filepath.Base(path) == sessionID+".jsonl" {
			sourcePath = path
			return filepath.SkipAll // Found it, stop walking
		}
		return nil
	})
	if err != nil && err != filepath.SkipAll {
		return fmt.Errorf("walk projects dir: %w", err)
	}

	if sourcePath == "" {
		return fmt.Errorf("session file not found: %s", sessionID)
	}

	// Create symlink from top level to actual file
	if err := os.Symlink(sourcePath, targetPath); err != nil {
		return fmt.Errorf("create symlink: %w", err)
	}

	return nil
}
