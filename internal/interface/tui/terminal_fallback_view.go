package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/neilberkman/ccrider/internal/core/session"
)

func (m Model) updateTerminalFallback(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "r":
		// Resume in current terminal
		return m, launchClaudeSession(
			m.fallbackProvider,
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
			m.fallbackProvider,
			m.fallbackSessionID,
			m.fallbackProjectPath,
			m.fallbackLastCwd,
			true, // fromFallbackView = true
		)

	case "w":
		// Write command to file
		return m, writeCommandToFile(
			m.fallbackProvider,
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
	// Build the command here so we can show it. The working directory must be
	// the project path (NOT lastCwd) — providers only find sessions from the
	// directory they were created in (see session.ResolveWorkingDir).
	workDir := session.ResolveWorkingDir(m.fallbackProjectPath, m.fallbackLastCwd)
	cmd := session.ResumeCommandIn(workDir, m.fallbackProvider, m.fallbackSessionID, "", false, nil)

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
