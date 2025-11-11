package tui

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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

	_, _ = fmt.Fprintf(w, "%s\n%s", title, desc)
}

func createSessionList(sessions []sessionItem, width, height int) list.Model {
	items := make([]list.Item, len(sessions))
	for i, s := range sessions {
		items[i] = sessionListItem{session: s}
	}

	delegate := sessionDelegate{DefaultDelegate: list.NewDefaultDelegate()}

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

	case "s":
		// Trigger sync (syncStartedMsg will set m.syncing = true)
		return m, syncSessions(m.db, m.projectFilterEnabled, m.currentDirectory)
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m Model) viewList() string {
	header := titleStyle.Render("Claude Code Sessions")

	// Build footer with sync status and help text
	var footer string

	// Show sync status if syncing
	if m.syncing {
		if m.syncTotal > 0 {
			// Show progress bar
			pct := float64(m.syncProgress) / float64(m.syncTotal) * 100
			barWidth := 30
			filled := int(float64(barWidth) * float64(m.syncProgress) / float64(m.syncTotal))
			if filled > barWidth {
				filled = barWidth
			}
			bar := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)
			footer = fmt.Sprintf("\n\n⟳ Syncing: [%s] %3.0f%% (%d/%d)", bar, pct, m.syncProgress, m.syncTotal)
		} else {
			footer = "\n\n" + lipgloss.NewStyle().Faint(true).Render("⟳ Syncing sessions...")
		}
	} else {
		// Build help text with proper width constraint
		helpText := "Enter: view | o: open in new tab | /: search | p: toggle project filter | s: sync | ?: help | q: quit"
		wrappedHelp := lipgloss.NewStyle().
			Width(m.width - 2).
			Render(helpText)
		footer = "\n\n" + wrappedHelp

		// Show filter status if enabled
		if m.projectFilterEnabled {
			footer = fmt.Sprintf("\n\n[Filter: %s]\n", m.currentDirectory) + wrappedHelp
		}
	}

	if len(m.sessions) == 0 {
		return header + "\n\nNo sessions found. Press 's' to sync or run 'ccrider sync'." + footer
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
