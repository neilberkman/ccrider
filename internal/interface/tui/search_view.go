package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
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

	// Navigation: Use Ctrl+j or arrow keys (allow j/k to be typed in search)
	// Note: Ctrl+k is left for textinput to handle (kills rest of line)
	case "ctrl+j", "down":
		if len(m.searchResults) > 0 {
			m.searchSelectedIdx++
			if m.searchSelectedIdx >= len(m.searchResults) {
				m.searchSelectedIdx = len(m.searchResults) - 1
			}
			return adjustSearchViewport(m), nil
		}
		return m, nil

	case "up":
		if len(m.searchResults) > 0 {
			m.searchSelectedIdx--
			if m.searchSelectedIdx < 0 {
				m.searchSelectedIdx = 0
			}
			return adjustSearchViewport(m), nil
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

			// Session header with match count and updated time
			matchCount := fmt.Sprintf("(%d %s)", len(result.Matches),
				map[bool]string{true: "match", false: "matches"}[len(result.Matches) == 1])
			updatedTime := formatTime(result.UpdatedAt)
			b.WriteString(fmt.Sprintf("%s%s %s | %s\n", prefix, summary,
				searchMetaStyle.Render(matchCount), searchMetaStyle.Render(updatedTime)))
			b.WriteString(fmt.Sprintf("  %s\n", searchMetaStyle.Render(result.Project)))

			// Show each match with clear separation
			query := m.searchInput.Value()
			for j, match := range result.Matches {
				typeLabel := fmt.Sprintf("[%s]", match.MessageType)
				b.WriteString(fmt.Sprintf("    %s ", searchMetaStyle.Render(typeLabel)))

				snippet := highlightQuery(match.Snippet, query)
				// Trim and show first line
				snippetLine := firstLine(snippet, 100)
				b.WriteString(snippetLine)

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

	// Footer with comprehensive help
	b.WriteString("\n\n")
	if len(m.searchResults) > 0 {
		b.WriteString("Ctrl+j or ↑↓: navigate | Enter: open | Ctrl+k: kill line | esc: back | ?: help")
	} else {
		b.WriteString("Type to search (min 2 chars) | Ctrl+k: kill line | esc: back | ?: help")
	}
	b.WriteString("\n")
	b.WriteString(searchMetaStyle.Render("Filters: project:path | after:yesterday | after:3-days-ago | before:2024-11-01"))

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
