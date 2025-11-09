package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var helpStyle = lipgloss.NewStyle().
	Foreground(lipgloss.Color("240"))

func (m Model) updateHelp(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q", "?":
		m.mode = listView
		return m, nil
	}

	return m, nil
}

func (m Model) viewHelp() string {
	help := `
Claude Code Session Manager - Help
═══════════════════════════════════

SESSION LIST VIEW
─────────────────
  ↑/↓, j/k     Navigate sessions
  Enter        View session details
  /            Search messages
  ?            Show this help
  q            Quit

SESSION DETAIL VIEW
───────────────────
  r            Resume session in Claude Code
  f            Fork session (new session ID)
  c            Copy resume command to clipboard
  /            Search within session
  j/k          Scroll line by line
  d/u          Scroll half page
  g/G          Jump to top/bottom
  esc          Back to session list
  q            Quit

SEARCH VIEW
───────────
  Type         Enter search query (live)
  Enter        Open selected session
  j/k          Navigate results
  esc          Back to session list
  q            Quit

Press any key to return to session list
`

	return helpStyle.Render(help)
}
