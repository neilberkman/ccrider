package tui

import (
	"fmt"
	"io"
	"strings"
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
	if i.session.Summary != "" {
		if i.session.IsAISummary {
			return "✨ " + i.session.Summary
		}
		return i.session.Summary
	}
	return i.session.ID[:12] + "..."
}

func (i sessionListItem) Description() string {
	return fmt.Sprintf("%s | %d messages | Updated: %s",
		i.session.Project, i.session.MessageCount, formatTime(i.session.UpdatedAt))
}

// Custom delegate to handle current directory highlighting
type sessionDelegate struct {
	list.DefaultDelegate
}

func (d sessionDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	s, ok := item.(sessionListItem)
	if !ok {
		d.DefaultDelegate.Render(w, m, index, item)
		return
	}

	// Get title and description
	title := s.Title()
	desc := s.Description()

	// Apply current directory styling if needed
	if s.session.MatchesCurrentDir {
		if index == m.Index() {
			// Selected item - use selected style
			title = selectedItemStyle.Render(title)
			desc = selectedItemStyle.Faint(true).Render(desc)
		} else {
			// Not selected - use current directory style
			title = currentDirItemStyle.Render(title)
			desc = itemStyle.Render(desc)
		}
	} else {
		if index == m.Index() {
			// Selected item
			title = selectedItemStyle.Render(title)
			desc = selectedItemStyle.Faint(true).Render(desc)
		} else {
			// Normal item
			title = itemStyle.Render(title)
			desc = itemStyle.Render(desc)
		}
	}

	fmt.Fprintf(w, "%s\n%s", title, desc)
}

func createSessionList(sessions []sessionItem, width, height int) list.Model {
	items := make([]list.Item, len(sessions))
	for i, s := range sessions {
		items[i] = sessionListItem{session: s}
	}

	delegate := sessionDelegate{DefaultDelegate: list.NewDefaultDelegate()}

	// Give list full height minus 1 line for help text
	listHeight := height - 1
	if listHeight < 10 {
		listHeight = 10
	}

	l := list.New(items, delegate, width, listHeight)
	l.Title = "" // No title
	l.SetShowStatusBar(false) // No status bar
	l.SetShowHelp(false) // No built-in help
	l.SetShowTitle(false) // No title rendering
	l.SetFilteringEnabled(false) // Disable built-in filter (we have dedicated search with /)

	// Disable paginator entirely to remove bottom spacing
	l.SetShowPagination(false)

	// Remove ALL internal margins and padding to eliminate wasted space
	l.Styles.PaginationStyle = l.Styles.PaginationStyle.Margin(0).Padding(0)
	l.Styles.HelpStyle = l.Styles.HelpStyle.Margin(0).Padding(0)
	l.Styles.StatusBar = l.Styles.StatusBar.Margin(0).Padding(0)
	l.Styles.Title = l.Styles.Title.Margin(0).Padding(0)

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

	case "s":
		// Trigger sync - save cursor position first
		m.syncing = true
		m.savedCursorIndex = m.list.Index()
		return m, syncSessions(m.db, m.projectFilterEnabled, m.currentDirectory)

	case "e":
		// Quick export to current directory
		if selected, ok := m.list.SelectedItem().(sessionListItem); ok {
			return m, exportSession(m.db, selected.session.ID)
		}
		return m, nil

	case "E":
		// Export with custom filename (save as)
		// TODO: Add text input prompt for custom filename
		if selected, ok := m.list.SelectedItem().(sessionListItem); ok {
			// For now, just do quick export
			return m, exportSession(m.db, selected.session.ID)
		}
		return m, nil
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m Model) viewList() string {
	// Build help text / sync status
	var helpText string
	if m.syncing && m.syncTotal > 0 {
		// Show full progress bar like CLI
		progressBar := renderProgressBar(m.syncCurrent, m.syncTotal, m.width)
		sessionInfo := ""
		if m.syncCurrentFile != "" {
			sessionInfo = " | " + m.syncCurrentFile

			// Truncate session info to fit within terminal width
			// Account for progress bar length (already uses most of m.width)
			// Progress bar format: "[====>    ] 5/10" is roughly 20 chars + bar width
			// Leave room for the separator and ensure no overflow
			maxSessionLen := m.width - len(progressBar) - 3 // -3 for safety margin
			if maxSessionLen < 10 {
				maxSessionLen = 10 // Minimum useful length
			}
			if len(sessionInfo) > maxSessionLen {
				sessionInfo = sessionInfo[:maxSessionLen-3] + "..."
			}
		}
		helpText = progressBar + sessionInfo
	} else if m.syncing {
		helpText = "⏳ Syncing..."
	} else {
		helpText = "↑/k up • ↓/j down • / filter • e export • q quit • ? more"
	}

	if len(m.sessions) == 0 {
		return "No sessions found. Press 's' to sync.\n\n" + helpText
	}

	// Render list and help on same line with no gap
	listView := m.list.View()
	// Strip trailing newlines from list view to eliminate gap
	listView = strings.TrimRight(listView, "\n")
	return listView + "\n" + helpText
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
