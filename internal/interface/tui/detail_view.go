package tui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	"github.com/cbroglie/mustache"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/dustin/go-humanize"
	"github.com/muesli/reflow/wordwrap"
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

func createViewport(detail sessionDetail, width, height int) viewport.Model {
	vp := viewport.New(width, height-8)
	result := renderConversation(detail, "", nil, -1, width)
	vp.SetContent(result.content)
	return vp
}

type renderResult struct {
	content       string
	messageStarts []int // line number where each message starts
}

func renderConversation(detail sessionDetail, query string, matches []int, currentMatchIdx int, width int) renderResult {
	var b strings.Builder
	var messageStarts []int
	currentLine := 0

	// Header
	b.WriteString(titleStyle.Render("Session: "+detail.Session.Summary) + "\n")
	currentLine++
	b.WriteString(fmt.Sprintf("Project: %s\n", detail.Session.Project))
	currentLine++
	b.WriteString(fmt.Sprintf("Messages: %d\n", detail.Session.MessageCount))
	currentLine++
	b.WriteString(strings.Repeat("─", width) + "\n\n")
	currentLine += 2

	// Messages
	for i, msg := range detail.Messages {
		messageStarts = append(messageStarts, currentLine)

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
		currentLine++

		// Content - wrap first, then highlight
		content := msg.Content

		// Word wrap content to viewport width
		wrappedContent := wordwrap.String(content, width)

		// Highlight query if this message is a match (after wrapping)
		if query != "" && contains(matches, i) {
			// Check if this is the current/focused match
			isCurrent := currentMatchIdx >= 0 && currentMatchIdx < len(matches) && matches[currentMatchIdx] == i
			wrappedContent = highlightQueryWithStyle(wrappedContent, query, isCurrent)
		}

		b.WriteString(wrappedContent)
		currentLine += strings.Count(wrappedContent, "\n") + 1
		b.WriteString("\n\n")
		currentLine += 2
		b.WriteString(strings.Repeat("─", width) + "\n\n")
		currentLine += 2
	}

	return renderResult{
		content:       b.String(),
		messageStarts: messageStarts,
	}
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
			// Clear highlighting when exiting search
			if m.currentSession != nil {
				result := renderConversation(*m.currentSession, "", nil, -1, m.width)
				m.viewport.SetContent(result.content)
				m.messageStarts = result.messageStarts
			}
			return m, nil

		case "enter":
			// Perform search and scroll to first match
			query := m.inSessionSearch.Value()
			if query != "" && m.currentSession != nil {
				m.inSessionMatches = findMatches(m.currentSession.Messages, query)
				m.inSessionMatchIdx = 0
				result := renderConversation(*m.currentSession, query, m.inSessionMatches, m.inSessionMatchIdx, m.width)
				m.viewport.SetContent(result.content)
				m.messageStarts = result.messageStarts
				scrollToMatch(&m)
			}
			return m, nil

		case "ctrl+n":
			// Next match
			if len(m.inSessionMatches) > 0 {
				m.inSessionMatchIdx++
				if m.inSessionMatchIdx >= len(m.inSessionMatches) {
					m.inSessionMatchIdx = 0
				}
				query := m.inSessionSearch.Value()
				result := renderConversation(*m.currentSession, query, m.inSessionMatches, m.inSessionMatchIdx, m.width)
				m.viewport.SetContent(result.content)
				m.messageStarts = result.messageStarts
				scrollToMatch(&m)
			}
			return m, nil

		case "ctrl+p":
			// Previous match
			if len(m.inSessionMatches) > 0 {
				m.inSessionMatchIdx--
				if m.inSessionMatchIdx < 0 {
					m.inSessionMatchIdx = len(m.inSessionMatches) - 1
				}
				query := m.inSessionSearch.Value()
				result := renderConversation(*m.currentSession, query, m.inSessionMatches, m.inSessionMatchIdx, m.width)
				m.viewport.SetContent(result.content)
				m.messageStarts = result.messageStarts
				scrollToMatch(&m)
			}
			return m, nil

		default:
			var cmd tea.Cmd
			m.inSessionSearch, cmd = m.inSessionSearch.Update(msg)

			// Re-render viewport with live highlighting on every keystroke
			query := m.inSessionSearch.Value()
			if query != "" && m.currentSession != nil {
				m.inSessionMatches = findMatches(m.currentSession.Messages, query)
				result := renderConversation(*m.currentSession, query, m.inSessionMatches, m.inSessionMatchIdx, m.width)
				m.viewport.SetContent(result.content)
				m.messageStarts = result.messageStarts
			} else {
				// Clear highlighting if search is empty
				m.inSessionMatches = nil
				result := renderConversation(*m.currentSession, "", nil, -1, m.width)
				m.viewport.SetContent(result.content)
				m.messageStarts = result.messageStarts
			}

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

func highlightQueryWithStyle(text, query string, isCurrent bool) string {
	if query == "" {
		return text
	}

	// Choose style based on whether this is the current/focused match
	style := searchMatchStyle
	if isCurrent {
		style = searchCurrentMatchStyle
	}

	// Highlight ALL occurrences case-insensitively
	lower := strings.ToLower(text)
	lowerQuery := strings.ToLower(query)

	var result strings.Builder
	lastIdx := 0

	for {
		idx := strings.Index(lower[lastIdx:], lowerQuery)
		if idx == -1 {
			// No more matches, append the rest
			result.WriteString(text[lastIdx:])
			break
		}

		// Adjust idx to be relative to original text
		idx += lastIdx

		// Append text before match
		result.WriteString(text[lastIdx:idx])

		// Append highlighted match
		match := text[idx : idx+len(query)]
		result.WriteString(style.Render(match))

		// Move past this match
		lastIdx = idx + len(query)
	}

	return result.String()
}

func findMatches(messages []messageItem, query string) []int {
	var matches []int
	lowerQuery := strings.ToLower(query)

	// DEBUG logging
	f, _ := os.OpenFile("/tmp/ccrider-debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if f != nil {
		fmt.Fprintf(f, "\nFIND MATCHES for query=%q in %d messages\n", query, len(messages))
	}

	for i, msg := range messages {
		if strings.Contains(strings.ToLower(msg.Content), lowerQuery) {
			matches = append(matches, i)
			if f != nil {
				preview := msg.Content
				if len(preview) > 100 {
					preview = preview[:100]
				}
				fmt.Fprintf(f, "  match at index %d: type=%s preview=%q\n", i, msg.Type, preview)
			}
		}
	}

	if f != nil {
		fmt.Fprintf(f, "  TOTAL MATCHES: %v\n", matches)
		f.Close()
	}

	return matches
}

// contains checks if slice contains value
func contains(slice []int, val int) bool {
	for _, v := range slice {
		if v == val {
			return true
		}
	}
	return false
}

// scrollToMatch scrolls the viewport to show the currently selected match
func scrollToMatch(m *Model) {
	if len(m.inSessionMatches) == 0 || m.currentSession == nil || len(m.messageStarts) == 0 {
		return
	}

	// Get the message index of the current match
	matchedMsgIdx := m.inSessionMatches[m.inSessionMatchIdx]
	if matchedMsgIdx < 0 || matchedMsgIdx >= len(m.messageStarts) {
		return
	}

	// Use the pre-calculated line offset from rendering
	lineOffset := m.messageStarts[matchedMsgIdx]

	// DEBUG logging
	f, _ := os.OpenFile("/tmp/ccrider-debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if f != nil {
		fmt.Fprintf(f, "SCROLL DEBUG: matchIdx=%d/%d msgIdx=%d lineOffset=%d totalMessages=%d totalStarts=%d\n",
			m.inSessionMatchIdx+1, len(m.inSessionMatches), matchedMsgIdx, lineOffset,
			len(m.currentSession.Messages), len(m.messageStarts))
		fmt.Fprintf(f, "  matches=%v\n", m.inSessionMatches)
		fmt.Fprintf(f, "  messageStarts=%v\n", m.messageStarts)
		if matchedMsgIdx < len(m.currentSession.Messages) {
			msg := m.currentSession.Messages[matchedMsgIdx]
			preview := msg.Content
			if len(preview) > 100 {
				preview = preview[:100]
			}
			fmt.Fprintf(f, "  message content preview: %q\n", preview)
		}
		f.Close()
	}

	// Set viewport to show this line
	m.viewport.SetYOffset(lineOffset)
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

	content := m.viewport.View()

	// Add search box if in search mode
	if m.inSessionSearchMode {
		searchBox := "\n" + m.inSessionSearch.View()
		if len(m.inSessionMatches) > 0 {
			searchBox += fmt.Sprintf(" [%d/%d matches]", m.inSessionMatchIdx+1, len(m.inSessionMatches))
		} else if m.inSessionSearch.Value() != "" {
			searchBox += " [no matches]"
		}
		searchBox += "\nctrl+n/ctrl+p: next/prev match | Enter: search | esc: exit search"
		content += searchBox
	} else {
		footer := fmt.Sprintf("\n%3.f%%", m.viewport.ScrollPercent()*100)
		footer += "\n\nr: resume | f: fork | o: open in new terminal | c: copy | /: search | j/k: scroll | esc: back | q: quit"
		content += footer
	}

	return content
}
