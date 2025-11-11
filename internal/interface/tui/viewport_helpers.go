package tui

// adjustSearchViewport ensures the selected search result is visible within the viewport.
// It calculates the visible window based on screen height and scrolls up/down as needed.
func adjustSearchViewport(m Model) Model {
	const linesPerResult = 7 // Each search result takes ~7 lines to render
	const reservedLines = 8  // Header + footer lines

	availableHeight := m.height - reservedLines
	maxVisibleResults := availableHeight / linesPerResult
	if maxVisibleResults < 2 {
		maxVisibleResults = 2
	}

	// Scroll down if selected item is below visible window
	if m.searchSelectedIdx >= m.searchViewOffset+maxVisibleResults {
		m.searchViewOffset = m.searchSelectedIdx - maxVisibleResults + 1
	}

	// Scroll up if selected item is above visible window
	if m.searchSelectedIdx < m.searchViewOffset {
		m.searchViewOffset = m.searchSelectedIdx
	}

	return m
}

// handleSearchMouseWheel handles mouse wheel events for search view scrolling.
// Returns updated model with adjusted selection and viewport offset.
func handleSearchMouseWheel(m Model, wheelDown bool) Model {
	if len(m.searchResults) == 0 {
		return m
	}

	const linesPerResult = 7
	const reservedLines = 8

	if wheelDown {
		m.searchSelectedIdx++
		if m.searchSelectedIdx >= len(m.searchResults) {
			m.searchSelectedIdx = len(m.searchResults) - 1
		}
	} else {
		m.searchSelectedIdx--
		if m.searchSelectedIdx < 0 {
			m.searchSelectedIdx = 0
		}
	}

	// Adjust viewport to keep selection visible
	availableHeight := m.height - reservedLines
	maxVisibleResults := availableHeight / linesPerResult
	if maxVisibleResults < 2 {
		maxVisibleResults = 2
	}

	if wheelDown && m.searchSelectedIdx >= m.searchViewOffset+maxVisibleResults {
		m.searchViewOffset = m.searchSelectedIdx - maxVisibleResults + 1
	} else if !wheelDown && m.searchSelectedIdx < m.searchViewOffset {
		m.searchViewOffset = m.searchSelectedIdx
	}

	return m
}
