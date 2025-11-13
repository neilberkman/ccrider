package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// tuiProgressReporter sends progress updates via a channel that the TUI can subscribe to
type tuiProgressReporter struct {
	total     int
	current   int
	startTime time.Time
	program   *tea.Program
}

func newTUIProgressReporter(total int, p *tea.Program) *tuiProgressReporter {
	return &tuiProgressReporter{
		total:     total,
		current:   0,
		startTime: time.Now(),
		program:   p,
	}
}

func (r *tuiProgressReporter) Update(sessionSummary string, firstMsg string) {
	r.current++
	if r.program != nil {
		r.program.Send(syncProgressMsg{
			current:     r.current,
			total:       r.total,
			sessionName: sessionSummary,
		})
	}
}

func (r *tuiProgressReporter) Finish() {
	// Progress done
}

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
