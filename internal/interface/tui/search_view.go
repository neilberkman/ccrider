package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	coresession "github.com/neilberkman/ccrider/internal/core/session"
)

func (m Model) updateSearch(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg.String() {
	case "esc":
		m.cancelSearch()
		m.searchPending = false
		m.mode = listView
		m.searchInput.SetValue("")
		m.searchResults = nil
		m.searchSelectedIdx = 0
		m.searchViewOffset = 0
		return m, nil

	case "enter":
		// Open selected session
		// While a search runs, the results on hand belong to an earlier query.
		if !m.searchPending && len(m.searchResults) > 0 && m.searchSelectedIdx < len(m.searchResults) {
			sessionID := m.searchResults[m.searchSelectedIdx].SessionID
			return m, loadSessionDetail(m.db, sessionID)
		}
		return m, nil

	// Navigation: Use Ctrl+j/n or arrow keys (allow j/k/q to be typed in search)
	// Note: Ctrl+k is left for textinput to handle (kills rest of line)
	case "ctrl+j", "ctrl+n", "down":
		if len(m.searchResults) > 0 {
			m.searchSelectedIdx++
			if m.searchSelectedIdx >= len(m.searchResults) {
				m.searchSelectedIdx = len(m.searchResults) - 1
			}
			return adjustSearchViewport(m), nil
		}
		return m, nil

	case "ctrl+p", "up":
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

	// Live search once typing pauses. The database runs one query at a time,
	// so a search per keystroke would queue every prefix ahead of the query
	// the user wants.
	m.searchSelectedIdx = 0
	m.searchViewOffset = 0 // Reset scroll on new search
	m.searchSeq++          // Increment sequence to invalidate in-flight searches
	m.cancelSearch()       // Free the database for the search this keystroke leads to
	seq := m.searchSeq
	return m, tea.Batch(cmd, tea.Tick(searchTypingPause, func(time.Time) tea.Msg {
		return searchPauseMsg{seq: seq}
	}))
}

// cancelSearch stops the running search, if any. Its command then reports
// nothing, and the database connection is free for the next query.
func (m *Model) cancelSearch() {
	if m.searchCancel != nil {
		m.searchCancel()
		m.searchCancel = nil
	}
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
	if m.searchPending {
		// Results for an earlier query would be misleading while this one runs.
		b.WriteString(searchMetaStyle.Render("Searching…"))
	} else if m.searchResults == nil {
		b.WriteString(searchMetaStyle.Render("Type to search (minimum 2 characters)"))
	} else if len(m.searchResults) == 0 {
		if m.showHeadless {
			b.WriteString(searchMetaStyle.Render("No results found"))
		} else {
			b.WriteString(searchMetaStyle.Render("No results found. Headless sessions are hidden: press esc, then 'a' to show them."))
		}
	} else {
		hiddenNote := " (headless hidden)"
		if m.showHeadless {
			hiddenNote = ""
		}
		fmt.Fprintf(&b, searchMetaStyle.Render("Found %d sessions%s:"), len(m.searchResults), hiddenNote)
		b.WriteString("\n\n")

		// Calculate max results based on screen height
		// Reserve: 5 for header (Search + divider + blank + "Found N" + blank)
		//          5 for footer (scroll indicators + blank + help + filters)
		availableHeight := m.height - 10
		if availableHeight < 10 {
			availableHeight = 10 // Fallback if height not set
		}

		// Estimate lines per result based on whether we have matches
		// With matches: ~7 lines (header + project + 3 matches + spacing)
		// Without matches (filter-only): ~3 lines (header + project + spacing)
		hasMatches := len(m.searchResults) > 0 && len(m.searchResults[0].Matches) > 0
		linesPerResult := 3
		if hasMatches {
			linesPerResult = 7
		}

		maxVisibleResults := availableHeight / linesPerResult
		if maxVisibleResults < 2 {
			maxVisibleResults = 2
		}
		// Hard cap - summaries can wrap, so be conservative
		if maxVisibleResults > 6 {
			maxVisibleResults = 6
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

			// Calculate max summary length to fit on one line
			// Format: "► summary (N matches) | time ago"
			// Reserve: 4 for prefix, ~20 for match count, ~15 for time = ~40
			maxSummaryLen := m.width - 40
			if maxSummaryLen < 30 {
				maxSummaryLen = 30
			}

			// Session header (one rune of the budget is kept for the ellipsis)
			summary := coresession.DisplaySummary(result.Summary, maxSummaryLen-1)
			if summary == "" && len(result.Matches) > 0 {
				summary = coresession.DisplayLine(result.Matches[0].Snippet, 60)
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
			// A filter-only search (a date or project, no words) lists
			// sessions without matching text, so it shows no match count.
			updatedTime := formatTime(result.UpdatedAt)
			if len(result.Matches) > 0 {
				matchCount := fmt.Sprintf("(%d %s)", len(result.Matches),
					map[bool]string{true: "match", false: "matches"}[len(result.Matches) == 1])
				fmt.Fprintf(&b, "%s%s %s | %s\n", prefix, summary,
					searchMetaStyle.Render(matchCount), searchMetaStyle.Render(updatedTime))
			} else {
				fmt.Fprintf(&b, "%s%s %s\n", prefix, summary, searchMetaStyle.Render("| "+updatedTime))
			}

			// Truncate project path too
			project := result.Project
			if len(project) > m.width-4 {
				project = "..." + project[len(project)-(m.width-7):]
			}
			fmt.Fprintf(&b, "  %s\n", searchMetaStyle.Render(project))

			// Show each match with clear separation
			query := m.searchInput.Value()
			for j, match := range result.Matches {
				// Fit the snippet on one line (100 runes max to match core
				// extraction) before highlighting, so the cut never lands inside
				// a highlight's escape codes.
				snippetLine := highlightQuery(coresession.DisplayLine(match.Snippet, 100), query)
				fmt.Fprintf(&b, "    %s", snippetLine)

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
		b.WriteString("Ctrl+j/k or ↑↓: navigate | Enter: open session | esc: back to list | ?: help")
	} else {
		b.WriteString("Type to search (min 2 chars) | esc: back to list | ?: help")
	}
	b.WriteString("\n")
	b.WriteString(searchMetaStyle.Render("Filters: project:path | after:3h | after:yesterday | after:3-days-ago | before:2024-11-01"))

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
