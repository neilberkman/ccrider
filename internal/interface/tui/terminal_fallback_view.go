package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) updateTerminalFallback(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "r":
		// Resume in current terminal
		return m, launchClaudeSession(
			m.fallbackSessionID,
			m.fallbackProjectPath,
			m.fallbackLastCwd,
			m.fallbackUpdatedAt,
			m.fallbackSummary,
			false,
		)

	case "c":
		// Copy command to clipboard - with fallback message
		return m, copyResumeCommandWithContext(
			m.fallbackSessionID,
			m.fallbackProjectPath,
			m.fallbackLastCwd,
			true, // fromFallbackView = true
		)

	case "w":
		// Write command to file
		return m, writeCommandToFile(
			m.fallbackSessionID,
			m.fallbackProjectPath,
			m.fallbackLastCwd,
		)

	case "q", "esc":
		// Go back to wherever we came from (list or detail view)
		if m.currentSession != nil {
			m.mode = detailView
		} else {
			m.mode = listView
		}
		return m, nil
	}

	return m, nil
}

func (m Model) viewTerminalFallback() string {
	// Build the command here so we can show it
	workDir := m.fallbackProjectPath
	if m.fallbackLastCwd != "" && m.fallbackLastCwd != m.fallbackProjectPath {
		workDir = m.fallbackLastCwd
	}

	var cmd string
	if workDir != "" {
		cmd = fmt.Sprintf("cd %s && claude --resume %s", workDir, m.fallbackSessionID)
	} else {
		cmd = fmt.Sprintf("claude --resume %s", m.fallbackSessionID)
	}

	return fmt.Sprintf(`
%s

Cannot spawn a new terminal window in this environment (SSH/remote session).

Command to resume:

  %s

Options:

  r - Resume in THIS terminal
  w - Write command to /tmp/ccrider-cmd.sh
  c - Try copying to clipboard
  q - Cancel

`, titleStyle.Render("Terminal Not Available"), cmd)
}
