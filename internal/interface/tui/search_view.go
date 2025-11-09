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
		m.searchViewOffset = 0
		return m, nil

	case "enter":
		// Open selected session
		if len(m.searchResults) > 0 && m.searchSelectedIdx < len(m.searchResults) {
			sessionID := m.searchResults[m.searchSelectedIdx].SessionID
			return m, loadSessionDetail(m.db, sessionID)
		}
		return m, nil

	// Navigation: Use Ctrl+j/k or arrow keys (allow j/k to be typed in search)
	case "ctrl+j", "down":
		if len(m.searchResults) > 0 {
			m.searchSelectedIdx++
			if m.searchSelectedIdx >= len(m.searchResults) {
				m.searchSelectedIdx = len(m.searchResults) - 1
			}
			m = adjustSearchViewport(m)
		}
		return m, nil

	case "ctrl+k", "up":
		if len(m.searchResults) > 0 {
			m.searchSelectedIdx--
			if m.searchSelectedIdx < 0 {
				m.searchSelectedIdx = 0
			}
			m = adjustSearchViewport(m)
		}
		return m, nil
	}

	// Update text input (all other keys including j/k/q go here)
	m.searchInput, cmd = m.searchInput.Update(msg)

	// Perform live search on every keystroke
	query := m.searchInput.Value()
	m.searchSelectedIdx = 0
	m.searchViewOffset = 0 // Reset scroll on new search
	return m, tea.Batch(cmd, performSearch(m.db, query))
}

// adjustSearchViewport ensures selected item is visible
func adjustSearchViewport(m Model) Model {
	linesPerResult := 7
	availableHeight := m.height - 8
	maxVisibleResults := availableHeight / linesPerResult
	if maxVisibleResults < 2 {
		maxVisibleResults = 2
	}

	// If selected is below visible window, scroll down
	if m.searchSelectedIdx >= m.searchViewOffset+maxVisibleResults {
		m.searchViewOffset = m.searchSelectedIdx - maxVisibleResults + 1
	}
	// If selected is above visible window, scroll up
	if m.searchSelectedIdx < m.searchViewOffset {
		m.searchViewOffset = m.searchSelectedIdx
	}

	return m
}

func (m Model) viewSearch() string {
	var b strings.Builder

	// Header with search input - ALWAYS at top
	b.WriteString(searchHeaderStyle.Render("Search: "))
	b.WriteString(m.searchInput.View())
	b.WriteString("\n")
	b.WriteString(strings.Repeat("─", 80))
	b.WriteString("\n\n")

	// Results
	if m.searchResults == nil {
		b.WriteString(searchMetaStyle.Render("Type to search (minimum 2 characters)"))
	} else if len(m.searchResults) == 0 {
		b.WriteString(searchMetaStyle.Render("No results found"))
	} else {
		b.WriteString(fmt.Sprintf(searchMetaStyle.Render("Found %d sessions:"), len(m.searchResults)))
		b.WriteString("\n\n")

		// Calculate max results based on screen height
		// Each result takes ~7 lines (header + project + 3 matches + spacing)
		// Reserve: 4 for header, 4 for footer = 8 total
		linesPerResult := 7
		availableHeight := m.height - 8
		maxVisibleResults := availableHeight / linesPerResult

		if maxVisibleResults < 2 {
			maxVisibleResults = 2
		}

		// Calculate visible window
		startIdx := m.searchViewOffset
		endIdx := startIdx + maxVisibleResults
		if endIdx > len(m.searchResults) {
			endIdx = len(m.searchResults)
		}

		for i := startIdx; i < endIdx; i++ {
			result := m.searchResults[i]
			isSelected := i == m.searchSelectedIdx

			// Session header
			summary := result.Summary
			if summary == "" && len(result.Matches) > 0 {
				summary = firstLine(result.Matches[0].Snippet, 60)
			}
			if summary == "" {
				summary = "[No summary]"
			}

			// Add selection indicator
			prefix := "  "
			if isSelected {
				prefix = "► "
				summary = searchSelectedStyle.Render(summary)
			} else {
				summary = searchMatchStyle.Render(summary)
			}

			// Session header with match count
			matchCount := fmt.Sprintf("(%d %s)", len(result.Matches),
				map[bool]string{true: "match", false: "matches"}[len(result.Matches) == 1])
			b.WriteString(fmt.Sprintf("%s%s %s\n", prefix, summary,
				searchMetaStyle.Render(matchCount)))
			b.WriteString(fmt.Sprintf("  %s\n", searchMetaStyle.Render(result.Project)))

			// Show each match with clear separation
			query := m.searchInput.Value()
			for j, match := range result.Matches {
				typeLabel := fmt.Sprintf("[%s]", match.MessageType)
				b.WriteString(fmt.Sprintf("    %s ", searchMetaStyle.Render(typeLabel)))

				snippet := highlightQuery(match.Snippet, query)
				// Trim and show first line
				snippetLine := firstLine(snippet, 100)
				b.WriteString(fmt.Sprintf("%s", snippetLine))

				if j < len(result.Matches)-1 {
					b.WriteString("\n")
				}
			}
			b.WriteString("\n\n")
		}

		// Show scroll indicators
		if startIdx > 0 {
			b.WriteString(searchMetaStyle.Render(fmt.Sprintf("... %d results above\n", startIdx)))
		}
		if endIdx < len(m.searchResults) {
			b.WriteString(searchMetaStyle.Render(fmt.Sprintf("... %d results below\n", len(m.searchResults)-endIdx)))
		}
	}

	if len(m.searchResults) > 0 {
		b.WriteString("\n\nCtrl+j/k or ↑↓: navigate | Enter: open | esc: back")
	} else {
		b.WriteString("\n\nType to search (min 2 chars, all keys work) | esc: back")
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
