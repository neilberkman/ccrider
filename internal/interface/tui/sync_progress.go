package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbletea"
)

// Keep imports for future use
var _ = tea.Quit
var _ = time.Now

// renderProgressBar creates a visual progress bar like the CLI
func renderProgressBar(current, total int, width int) string {
	if total == 0 {
		return ""
	}

	pct := float64(current) / float64(total) * 100

	// Progress bar (use available width, max 50)
	barWidth := width - 30 // Leave space for percentage and counts
	if barWidth > 50 {
		barWidth = 50
	}
	if barWidth < 20 {
		barWidth = 20
	}

	filled := int(float64(barWidth) * float64(current) / float64(total))
	if filled > barWidth {
		filled = barWidth
	}
	bar := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)

	return fmt.Sprintf("[%s] %3.0f%% (%d/%d)", bar, pct, current, total)
}
