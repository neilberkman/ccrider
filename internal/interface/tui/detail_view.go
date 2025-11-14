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
	result := renderConversation(detail, "", nil, -1, width, nil)
	vp.SetContent(result.content)
	return vp
}

type renderResult struct {
	content string
}

func renderConversation(detail sessionDetail, query string, matches []int, currentMatchIdx int, width int, matchLines []int) renderResult {
	// Render everything with yellow highlighting first
	var b strings.Builder

	// Header
	b.WriteString(titleStyle.Render("Session: "+detail.Session.Summary) + "\n")
	b.WriteString(fmt.Sprintf("Project: %s\n", detail.Session.Project))
	b.WriteString(fmt.Sprintf("Messages: %d\n", detail.Session.MessageCount))
	b.WriteString(strings.Repeat("─", width) + "\n\n")

	// Messages - render WITHOUT highlighting first
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

		// Render message header
		b.WriteString(style.Render(fmt.Sprintf("▸ %s", label)))
		b.WriteString(" ")
		b.WriteString(timestampStyle.Render(formatTime(msg.Timestamp)))
		b.WriteString("\n")

		// Word wrap content
		content := msg.Content
		wrapWidth := width - 10
		if wrapWidth < 40 {
			wrapWidth = 40
		}
		wrappedContent := wordwrap.String(content, wrapWidth)

		// Add content without highlighting yet
		b.WriteString(wrappedContent)
		b.WriteString("\n\n")
		b.WriteString(strings.Repeat("─", width) + "\n\n")
	}

	baseContent := b.String()

	// If no query, return base content
	if query == "" {
		return renderResult{content: baseContent}
	}

	// Split into lines and highlight - current line gets green, others get yellow
	lines := strings.Split(baseContent, "\n")
	var currentMatchLine int = -1
	if currentMatchIdx >= 0 && currentMatchIdx < len(matchLines) {
		currentMatchLine = matchLines[currentMatchIdx]
	}

	var result strings.Builder
	for i, line := range lines {
		isCurrent := (i == currentMatchLine)
		highlighted := highlightLineWithStyle(line, query, isCurrent)
		result.WriteString(highlighted)
		if i < len(lines)-1 {
			result.WriteString("\n")
		}
	}

	return renderResult{
		content: result.String(),
	}
}

func (m Model) updateDetail(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Handle in-session search mode
	if m.inSessionSearchMode {
		// In navigation mode (after Enter) - handle n/p for cycling
		if m.inSessionNavigationMode {
			switch msg.String() {
			case "esc":
				m.inSessionSearchMode = false
				m.inSessionNavigationMode = false
				m.inSessionSearch.SetValue("")
				m.inSessionMatches = nil
				m.matchLines = nil
				m.inSessionMatchIdx = 0
				// Clear highlighting when exiting search
				if m.currentSession != nil {
					result := renderConversation(*m.currentSession, "", nil, -1, m.width, nil)
					m.viewport.SetContent(result.content)
				}
				return m, nil

			case "n":
				// Next match
				if len(m.matchLines) > 0 {
					m.inSessionMatchIdx++
					if m.inSessionMatchIdx >= len(m.matchLines) {
						m.inSessionMatchIdx = 0
					}
					// Re-render with updated current match highlighting
					query := m.inSessionSearch.Value()
					result := renderConversation(*m.currentSession, query, m.inSessionMatches, m.inSessionMatchIdx, m.width, m.matchLines)
					m.viewport.SetContent(result.content)
					scrollToMatchSmart(&m)
				}
				return m, nil

			case "p":
				// Previous match
				if len(m.matchLines) > 0 {
					m.inSessionMatchIdx--
					if m.inSessionMatchIdx < 0 {
						m.inSessionMatchIdx = len(m.matchLines) - 1
					}
					// Re-render with updated current match highlighting
					query := m.inSessionSearch.Value()
					result := renderConversation(*m.currentSession, query, m.inSessionMatches, m.inSessionMatchIdx, m.width, m.matchLines)
					m.viewport.SetContent(result.content)
					scrollToMatchSmart(&m)
				}
				return m, nil

			case "j", "down", "k", "up":
				// Manual scrolling
				if msg.String() == "j" || msg.String() == "down" {
					m.viewport.LineDown(1)
				} else {
					m.viewport.LineUp(1)
				}
				return m, nil

			default:
				// Unhandled keys in navigation mode - do nothing
				return m, nil
			}
		} else {
			// NOT in navigation mode - typing in search box
			switch msg.String() {
			case "esc":
				m.inSessionSearchMode = false
				m.inSessionSearch.SetValue("")
				m.inSessionMatches = nil
				m.matchLines = nil
				m.inSessionMatchIdx = 0
				// Clear highlighting when exiting search
				if m.currentSession != nil {
					result := renderConversation(*m.currentSession, "", nil, -1, m.width, nil)
					m.viewport.SetContent(result.content)
				}
				return m, nil

			case "enter":
				// Enter navigation mode - enables n/p to cycle through matches
				if len(m.matchLines) > 0 {
					m.inSessionNavigationMode = true
					// Re-render to show current match highlighting
					query := m.inSessionSearch.Value()
					result := renderConversation(*m.currentSession, query, m.inSessionMatches, m.inSessionMatchIdx, m.width, m.matchLines)
					m.viewport.SetContent(result.content)
				}
				return m, nil

			case "j", "down", "k", "up":
				// Manual scrolling
				if msg.String() == "j" || msg.String() == "down" {
					m.viewport.LineDown(1)
				} else {
					m.viewport.LineUp(1)
				}
				return m, nil

			default:
				// Everything else goes to text input
				var cmd tea.Cmd
				m.inSessionSearch, cmd = m.inSessionSearch.Update(msg)

				// Re-render viewport with live highlighting and jump to first match on every keystroke
				query := m.inSessionSearch.Value()
				if query != "" && m.currentSession != nil {
					// Find which messages contain matches (for highlighting)
					m.inSessionMatches = findMatches(m.currentSession.Messages, query)
					// Find exact line numbers in rendered content (for scrolling)
					m.matchLines = findMatchesInRenderedContent(*m.currentSession, query, m.width)

					// Jump to first match automatically
					if len(m.matchLines) > 0 {
						m.inSessionMatchIdx = 0
					} else {
						m.inSessionMatchIdx = -1
					}

					// Render with highlighting
					result := renderConversation(*m.currentSession, query, m.inSessionMatches, m.inSessionMatchIdx, m.width, m.matchLines)
					m.viewport.SetContent(result.content)

					// Scroll to first match live (always scroll when typing)
					if len(m.matchLines) > 0 {
						scrollToMatchAlways(&m)
					}
				} else {
					// Clear highlighting if search is empty
					m.inSessionMatches = nil
					m.matchLines = nil
					m.inSessionMatchIdx = 0
					result := renderConversation(*m.currentSession, "", nil, -1, m.width, nil)
					m.viewport.SetContent(result.content)
				}

				return m, cmd
			}
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

// highlightQueryInContent highlights query matches in content, line by line
// Uses currentMatchLine to apply special highlighting to the active match
func highlightQueryInContent(content, query string, startLine, currentMatchLine int) string {
	if query == "" {
		return content
	}

	lines := strings.Split(content, "\n")
	var result strings.Builder

	// DEBUG: Log every line that contains the query to see line number comparisons
	f, _ := os.OpenFile("/tmp/ccrider-debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)

	for i, line := range lines {
		lineNumber := startLine + i
		// Check if this line is the currently selected match
		isCurrent := (lineNumber == currentMatchLine)

		// DEBUG: Log every line with a match to see comparisons
		if f != nil && strings.Contains(strings.ToLower(line), strings.ToLower(query)) {
			truncated := line
			if len(truncated) > 50 {
				truncated = truncated[:50]
			}
			fmt.Fprintf(f, "[LINE %d] isCurrent=%v (comparing %d == %d): %s\n",
				lineNumber, isCurrent, lineNumber, currentMatchLine, truncated)
		}

		// Highlight all matches in this line
		highlightedLine := highlightLineWithStyle(line, query, isCurrent)
		result.WriteString(highlightedLine)

		if i < len(lines)-1 {
			result.WriteString("\n")
		}
	}

	if f != nil {
		f.Close()
	}

	return result.String()
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// highlightLineWithStyle highlights all occurrences of query in a single line
func highlightLineWithStyle(text, query string, isCurrent bool) string {
	if query == "" {
		return text
	}

	// Highlight ALL occurrences case-insensitively
	lower := strings.ToLower(text)
	lowerQuery := strings.ToLower(query)

	var result strings.Builder
	lastIdx := 0
	matchCount := 0

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

		// Choose style: if this line is current, highlight FIRST match as current, rest as regular
		var style lipgloss.Style
		if isCurrent && matchCount == 0 {
			style = searchCurrentMatchStyle
		} else {
			style = searchMatchStyle
		}

		// Append highlighted match
		match := text[idx : idx+len(query)]
		result.WriteString(style.Render(match))

		// Move past this match
		lastIdx = idx + len(query)
		matchCount++
	}

	return result.String()
}

// highlightQueryWithStyle highlights ALL occurrences of query in text
// All matches get yellow, except if this is the current match message we highlight ALL in green
// TODO: This should be fixed to only highlight the SPECIFIC occurrence, not the whole message
func highlightQueryWithStyle(text, query string, isCurrent bool) string {
	if query == "" {
		return text
	}

	// For now: if this is the current message, highlight all occurrences in green
	// Otherwise highlight all in yellow
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

		// Append highlighted match (all in same style for this message)
		match := text[idx : idx+len(query)]
		result.WriteString(style.Render(match))

		// Move past this match
		lastIdx = idx + len(query)
	}

	return result.String()
}

// findMatchesInRenderedContent uses Shannon's approach:
// 1. Render the full conversation to a string
// 2. Split by newlines to get exact line array
// 3. Search through rendered lines for query
// 4. Return exact line indices (no offset calculations)
func findMatchesInRenderedContent(detail sessionDetail, query string, width int) []int {
	if query == "" {
		return nil
	}

	// Render full content WITHOUT search highlighting first
	result := renderConversation(detail, "", nil, -1, width, nil)

	// Split into lines
	lines := strings.Split(result.content, "\n")

	// Find lines containing query (case-insensitive)
	var matchLines []int
	queryLower := strings.ToLower(query)

	// Header is lines 0-4 (title, project, messages, separator, blank line)
	// Skip header lines - only include matches in message content (line 5+)
	const headerLines = 5

	// DEBUG: Log what we're finding
	f, _ := os.OpenFile("/tmp/ccrider-debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if f != nil {
		fmt.Fprintf(f, "[FIND_MATCHES] query=%q total_lines=%d\n", query, len(lines))
	}

	for i, line := range lines {
		if i >= headerLines && strings.Contains(strings.ToLower(line), queryLower) {
			matchLines = append(matchLines, i)
			if f != nil {
				truncated := line
				if len(truncated) > 60 {
					truncated = truncated[:60]
				}
				fmt.Fprintf(f, "[FIND_MATCHES] Line %d matches: %s\n", i, truncated)
			}
		}
	}

	if f != nil {
		fmt.Fprintf(f, "[FIND_MATCHES] Found %d matches: %v\n", len(matchLines), matchLines)
		f.Close()
	}

	return matchLines
}

// findMatches finds which messages contain the query (for highlighting)
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

// contains checks if slice contains value
func contains(slice []int, val int) bool {
	for _, v := range slice {
		if v == val {
			return true
		}
	}
	return false
}

// scrollToMatchSmart scrolls viewport intelligently for n/p navigation:
// - If match already visible, don't scroll (preserve context)
// - If need to jump to new page, position match 3 lines down for context
func scrollToMatchSmart(m *Model) {
	if len(m.matchLines) == 0 || m.inSessionMatchIdx < 0 || m.inSessionMatchIdx >= len(m.matchLines) {
		return
	}

	matchLine := m.matchLines[m.inSessionMatchIdx]
	currentOffset := m.viewport.YOffset
	viewportHeight := m.viewport.Height

	// Check if match is already visible in the current viewport
	// Visible range is [currentOffset, currentOffset + viewportHeight)
	if matchLine >= currentOffset && matchLine < currentOffset+viewportHeight {
		// Match is already visible - don't scroll, preserve context
		return
	}

	// Match is not visible, need to scroll
	// Position it a few lines down from top (3 lines of context above)
	targetOffset := matchLine - 3
	if targetOffset < 0 {
		targetOffset = 0
	}

	m.viewport.SetYOffset(targetOffset)
}

// scrollToMatchAlways always scrolls to match - used for live search while typing
// Always positions match 3 lines down for context
func scrollToMatchAlways(m *Model) {
	if len(m.matchLines) == 0 || m.inSessionMatchIdx < 0 || m.inSessionMatchIdx >= len(m.matchLines) {
		return
	}

	matchLine := m.matchLines[m.inSessionMatchIdx]

	// Always scroll - position 3 lines down for context
	targetOffset := matchLine - 3
	if targetOffset < 0 {
		targetOffset = 0
	}

	m.viewport.SetYOffset(targetOffset)
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
		if len(m.matchLines) > 0 {
			searchBox += fmt.Sprintf(" [%d/%d matches]", m.inSessionMatchIdx+1, len(m.matchLines))
		} else if m.inSessionSearch.Value() != "" {
			searchBox += " [no matches]"
		}
		if m.inSessionNavigationMode {
			searchBox += "\nn/p: next/prev | ↑↓: scroll | esc: exit"
		} else {
			searchBox += "\nEnter: navigate mode | ↑↓: scroll | esc: exit"
		}
		content += searchBox
	} else {
		footer := fmt.Sprintf("\n%3.f%%", m.viewport.ScrollPercent()*100)
		footer += "\n\nr: resume | f: fork | o: open in new terminal | c: copy | /: search | j/k: scroll | esc: back | q: quit"
		content += footer
	}

	return content
}
