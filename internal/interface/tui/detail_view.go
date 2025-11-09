package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	userStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("cyan")).
			Bold(true)

	assistantStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("green")).
			Bold(true)

	systemStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("yellow")).
			Bold(true)

	timestampStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240")).
			Faint(true)
)

func createViewport(detail sessionDetail, width, height int) viewport.Model {
	vp := viewport.New(width, height-8)
	vp.SetContent(renderConversation(detail))
	return vp
}

func renderConversation(detail sessionDetail) string {
	var b strings.Builder

	// Header
	b.WriteString(titleStyle.Render("Session: "+detail.Session.Summary) + "\n")
	b.WriteString(fmt.Sprintf("Project: %s\n", detail.Session.Project))
	b.WriteString(fmt.Sprintf("Messages: %d\n", detail.Session.MessageCount))
	b.WriteString(strings.Repeat("─", 80) + "\n\n")

	// Messages
	for _, msg := range detail.Messages {
		var style lipgloss.Style
		var label string

		switch msg.Type {
		case "user":
			style = userStyle
			label = "USER"
		case "assistant":
			style = assistantStyle
			label = "ASSISTANT"
		case "system":
			style = systemStyle
			label = "SYSTEM"
		default:
			style = lipgloss.NewStyle()
			label = strings.ToUpper(msg.Type)
		}

		b.WriteString(style.Render(fmt.Sprintf("▸ %s", label)))
		b.WriteString(" ")
		b.WriteString(timestampStyle.Render(formatTime(msg.Timestamp)))
		b.WriteString("\n")

		// Content (truncate if too long for preview)
		content := msg.Content
		if len(content) > 500 {
			content = content[:500] + "\n... (truncated)"
		}
		b.WriteString(content)
		b.WriteString("\n\n")
		b.WriteString(strings.Repeat("─", 80) + "\n\n")
	}

	return b.String()
}

func (m Model) updateDetail(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Handle in-session search mode
	if m.inSessionSearchMode {
		switch msg.String() {
		case "esc":
			m.inSessionSearchMode = false
			m.inSessionSearch.SetValue("")
			m.inSessionMatches = nil
			m.inSessionMatchIdx = 0
			return m, nil

		case "enter":
			// Perform search
			query := m.inSessionSearch.Value()
			if query != "" && m.currentSession != nil {
				m.inSessionMatches = findMatches(m.currentSession.Messages, query)
				m.inSessionMatchIdx = 0
			}
			return m, nil

		case "ctrl+n", "n":
			// Next match
			if len(m.inSessionMatches) > 0 {
				m.inSessionMatchIdx++
				if m.inSessionMatchIdx >= len(m.inSessionMatches) {
					m.inSessionMatchIdx = 0
				}
			}
			return m, nil

		case "ctrl+p", "p":
			// Previous match
			if len(m.inSessionMatches) > 0 {
				m.inSessionMatchIdx--
				if m.inSessionMatchIdx < 0 {
					m.inSessionMatchIdx = len(m.inSessionMatches) - 1
				}
			}
			return m, nil

		default:
			var cmd tea.Cmd
			m.inSessionSearch, cmd = m.inSessionSearch.Update(msg)
			return m, cmd
		}
	}

	// Normal detail view navigation
	switch msg.String() {
	case "esc", "q":
		m.mode = listView
		return m, nil

	case "ctrl+f", "/":
		m.inSessionSearchMode = true
		m.inSessionSearch.Focus()
		return m, nil

	case "j", "down":
		m.viewport.LineDown(1)
		return m, nil

	case "k", "up":
		m.viewport.LineUp(1)
		return m, nil

	case "d":
		m.viewport.HalfViewDown()
		return m, nil

	case "u":
		m.viewport.HalfViewUp()
		return m, nil

	case "g":
		m.viewport.GotoTop()
		return m, nil

	case "G":
		m.viewport.GotoBottom()
		return m, nil
	}

	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

func findMatches(messages []messageItem, query string) []int {
	var matches []int
	lowerQuery := strings.ToLower(query)

	for i, msg := range messages {
		if strings.Contains(strings.ToLower(msg.Content), lowerQuery) {
			matches = append(matches, i)
		}
	}

	return matches
}

func (m Model) viewDetail() string {
	if m.currentSession == nil {
		return "No session loaded"
	}

	content := m.viewport.View()

	// Add search box if in search mode
	if m.inSessionSearchMode {
		searchBox := "\n" + m.inSessionSearch.View()
		if len(m.inSessionMatches) > 0 {
			searchBox += fmt.Sprintf(" [%d/%d matches]", m.inSessionMatchIdx+1, len(m.inSessionMatches))
		} else if m.inSessionSearch.Value() != "" {
			searchBox += " [no matches]"
		}
		searchBox += "\nn/p: next/prev match | Enter: search | esc: exit search"
		content += searchBox
	} else {
		footer := fmt.Sprintf("\n%3.f%%", m.viewport.ScrollPercent()*100)
		footer += "\n\n/: search in session | j/k: scroll | d/u: half page | g/G: top/bottom | esc: back | q: quit"
		content += footer
	}

	return content
}
