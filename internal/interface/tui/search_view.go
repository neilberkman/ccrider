package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	searchHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("205"))

	searchMatchStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("170")).
				Bold(true)

	searchMetaStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")).
			Faint(true)

	searchSelectedStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("170")).
				Bold(true)
)

func (m Model) updateSearch(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg.String() {
	case "esc":
		m.mode = listView
		m.searchInput.SetValue("")
		m.searchResults = nil
		m.searchSelectedIdx = 0
		return m, nil

	case "enter":
		// If we have results and something is selected, open that session
		if len(m.searchResults) > 0 && m.searchSelectedIdx < len(m.searchResults) {
			sessionID := m.searchResults[m.searchSelectedIdx].SessionID
			return m, loadSessionDetail(m.db, sessionID)
		}
		// Otherwise, perform search
		query := m.searchInput.Value()
		m.searchSelectedIdx = 0
		return m, performSearch(m.db, query)

	case "down", "j":
		if len(m.searchResults) > 0 {
			m.searchSelectedIdx++
			if m.searchSelectedIdx >= len(m.searchResults) {
				m.searchSelectedIdx = len(m.searchResults) - 1
			}
		}
		return m, nil

	case "up", "k":
		if len(m.searchResults) > 0 {
			m.searchSelectedIdx--
			if m.searchSelectedIdx < 0 {
				m.searchSelectedIdx = 0
			}
		}
		return m, nil
	}

	// Update text input
	m.searchInput, cmd = m.searchInput.Update(msg)

	return m, cmd
}

func (m Model) viewSearch() string {
	var b strings.Builder

	// Header
	b.WriteString(searchHeaderStyle.Render("Search Sessions"))
	b.WriteString("\n\n")

	// Search input
	b.WriteString(m.searchInput.View())
	b.WriteString("\n\n")

	// Results
	if m.searchResults == nil {
		b.WriteString(searchMetaStyle.Render("Type to search and press Enter"))
	} else if len(m.searchResults) == 0 {
		b.WriteString(searchMetaStyle.Render("No results found"))
	} else {
		b.WriteString(fmt.Sprintf(searchMetaStyle.Render("Found %d matches:"), len(m.searchResults)))
		b.WriteString("\n\n")

		// Show results (limit to screen height)
		maxResults := m.height - 12
		if maxResults < 5 {
			maxResults = 5
		}
		if maxResults > len(m.searchResults) {
			maxResults = len(m.searchResults)
		}

		for i := 0; i < maxResults; i++ {
			result := m.searchResults[i]
			isSelected := i == m.searchSelectedIdx

			// Session summary or first line of match
			summary := result.Summary
			if summary == "" {
				summary = firstLine(result.MatchSnippet, 80)
			}

			// Add selection indicator
			prefix := "  "
			if isSelected {
				prefix = "> "
				summary = searchSelectedStyle.Render(summary)
			} else {
				summary = searchMatchStyle.Render(summary)
			}

			b.WriteString(fmt.Sprintf("%s%s\n", prefix, summary))
			b.WriteString(fmt.Sprintf("  %s | %s message\n",
				searchMetaStyle.Render(result.Project),
				result.MessageType))

			// Show snippet with query highlighted
			query := m.searchInput.Value()
			snippet := highlightQuery(result.MatchSnippet, query)
			b.WriteString(fmt.Sprintf("  %s\n\n", snippet))
		}

		if len(m.searchResults) > maxResults {
			b.WriteString(searchMetaStyle.Render(fmt.Sprintf("... and %d more results\n", len(m.searchResults)-maxResults)))
		}
	}

	if len(m.searchResults) > 0 {
		b.WriteString("\n\nj/k: navigate | Enter: open session | esc: back | q: quit")
	} else {
		b.WriteString("\n\nPress Enter to search | esc to go back | q to quit")
	}

	return b.String()
}

func highlightQuery(text, query string) string {
	if query == "" {
		return text
	}

	// Simple case-insensitive highlighting
	lower := strings.ToLower(text)
	lowerQuery := strings.ToLower(query)

	idx := strings.Index(lower, lowerQuery)
	if idx == -1 {
		return text
	}

	// Highlight the match
	before := text[:idx]
	match := text[idx : idx+len(query)]
	after := text[idx+len(query):]

	return before + searchMatchStyle.Render(match) + after
}
