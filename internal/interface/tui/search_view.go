package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) updateSearch(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = listView
		return m, nil
	}

	return m, nil
}

func (m Model) viewSearch() string {
	return "Search view (coming soon)\n\nPress esc to go back"
}
