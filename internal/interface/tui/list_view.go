package tui

import (
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/dustin/go-humanize"
)

type sessionListItem struct {
	session sessionItem
}

func (i sessionListItem) FilterValue() string {
	return i.session.Summary + " " + i.session.Project
}

func (i sessionListItem) Title() string {
	// Priority: Claude summary > first message (truncated) > session ID
	title := ""
	if i.session.Summary != "" {
		title = i.session.Summary
	} else {
		title = i.session.ID[:12] + "..."
	}

	// Add subtle marker if this session matches current directory
	if i.session.MatchesCurrentDir {
		title = "• " + title
	}

	return title
}

func (i sessionListItem) Description() string {
	return fmt.Sprintf("%s | %d messages | Updated: %s",
		i.session.Project, i.session.MessageCount, formatTime(i.session.UpdatedAt))
}

func createSessionList(sessions []sessionItem, width, height int) list.Model {
	items := make([]list.Item, len(sessions))
	for i, s := range sessions {
		items[i] = sessionListItem{session: s}
	}

	delegate := list.NewDefaultDelegate()
	delegate.Styles.SelectedTitle = selectedItemStyle
	delegate.Styles.SelectedDesc = selectedItemStyle.Faint(true)

	l := list.New(items, delegate, width, height-4)
	l.Title = "Claude Code Sessions"
	l.Styles.Title = titleStyle
	l.SetShowStatusBar(true)
	l.SetFilteringEnabled(true)

	return l
}

func (m Model) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		if selected, ok := m.list.SelectedItem().(sessionListItem); ok {
			return m, loadSessionDetail(m.db, selected.session.ID)
		}
		return m, nil

	case "o":
		// Open selected session in new terminal
		if selected, ok := m.list.SelectedItem().(sessionListItem); ok {
			m.err = nil
			// Load session info (including lastCwd) then launch
			return m, loadSessionForLaunch(m.db, selected.session.ID)
		}
		return m, nil

	case "/":
		m.mode = searchView
		return m, nil

	case "p":
		// Toggle project filter
		m.projectFilterEnabled = !m.projectFilterEnabled
		// Reload sessions with new filter
		return m, loadSessions(m.db, m.projectFilterEnabled, m.currentDirectory)
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m Model) viewList() string {
	header := titleStyle.Render("Claude Code Sessions")
	footer := "\n\nEnter: view | o: open in new tab | /: search | p: toggle project filter | ?: help | q: quit"

	// Show filter status if enabled
	if m.projectFilterEnabled {
		footer = fmt.Sprintf("\n\n[Filter: %s]\n", m.currentDirectory) + footer
	}

	if len(m.sessions) == 0 {
		return header + "\n\nNo sessions found. Run 'ccrider sync' to import sessions." + footer
	}

	return m.list.View() + footer
}

func formatTime(t string) string {
	// Parse SQLite datetime format
	parsed, err := time.Parse("2006-01-02T15:04:05.999Z07:00", t)
	if err != nil {
		// Try without timezone
		parsed, err = time.Parse("2006-01-02 15:04:05", t)
		if err != nil {
			return t
		}
	}
	return humanize.Time(parsed)
}
