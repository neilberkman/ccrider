package tui

import "github.com/charmbracelet/lipgloss"

// Global styles used across views
var (
	// List view styles
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("205"))

	itemStyle = lipgloss.NewStyle().
			PaddingLeft(2)

	selectedItemStyle = lipgloss.NewStyle().
				PaddingLeft(1).
				Foreground(lipgloss.Color("170")).
				Bold(true)

	currentDirItemStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("120")) // Light green - contrasts better with purple selection

	// Detail view styles
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
			Foreground(lipgloss.Color("246")) // Lighter gray that works better in dark terminals

	// Search view styles
	searchHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("205"))

	searchMatchStyle = lipgloss.NewStyle().
				Background(lipgloss.Color("240")).
				Foreground(lipgloss.Color("yellow")).
				Bold(true)

	searchCurrentMatchStyle = lipgloss.NewStyle().
				Background(lipgloss.Color("226")).
				Foreground(lipgloss.Color("0")).
				Bold(true)

	searchMetaStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("246")) // Lighter gray for dark terminals

	searchSelectedStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("170")).
				Bold(true)

	// Help view styles
	helpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240"))
)
