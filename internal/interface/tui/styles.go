package tui

import "github.com/charmbracelet/lipgloss"

// Global styles used across views.
// AdaptiveColor picks the right value based on the terminal background.
var (
	// List view styles
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.AdaptiveColor{Light: "127", Dark: "205"})

	itemStyle = lipgloss.NewStyle().
			PaddingLeft(2)

	selectedItemStyle = lipgloss.NewStyle().
				PaddingLeft(1).
				Foreground(lipgloss.AdaptiveColor{Light: "90", Dark: "170"}).
				Bold(true)

	currentDirItemStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.AdaptiveColor{Light: "28", Dark: "120"}) // Light green - contrasts better with purple selection

	// Detail view styles
	userStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "30", Dark: "cyan"}).
			Bold(true)

	assistantStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "28", Dark: "green"}).
			Bold(true)

	systemStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "130", Dark: "yellow"}).
			Bold(true)

	timestampStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "243", Dark: "246"}) // Lighter gray that works better in dark terminals

	// Search view styles
	searchHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.AdaptiveColor{Light: "127", Dark: "205"})

	searchMatchStyle = lipgloss.NewStyle().
				Foreground(lipgloss.AdaptiveColor{Light: "130", Dark: "226"}). // Bright yellow text for all matches
				Bold(true)

	searchCurrentMatchStyle = lipgloss.NewStyle().
				Foreground(lipgloss.AdaptiveColor{Light: "22", Dark: "46"}). // Bright green text for current match
				Bold(true).
				Underline(true)

	searchMetaStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "243", Dark: "246"}) // Lighter gray for dark terminals

	searchSelectedStyle = lipgloss.NewStyle().
				Foreground(lipgloss.AdaptiveColor{Light: "90", Dark: "170"}).
				Bold(true)

	// Help view styles
	helpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "244", Dark: "240"})
)
