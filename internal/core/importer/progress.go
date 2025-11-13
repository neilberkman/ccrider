package importer

import (
	"fmt"
	"io"
	"strings"
	"time"
)

// ProgressCallback defines the interface for progress reporting
type ProgressCallback interface {
	Update(sessionSummary string, firstMsg string)
	Finish()
}

// ProgressReporter handles progress feedback during import
type ProgressReporter struct {
	writer    io.Writer
	total     int
	current   int
	startTime time.Time
	lastMsg   string
}

// NewProgressReporter creates a new progress reporter
func NewProgressReporter(w io.Writer, total int) *ProgressReporter {
	return &ProgressReporter{
		writer:    w,
		total:     total,
		current:   0,
		startTime: time.Now(),
	}
}

// Update updates the progress bar with current session info
func (p *ProgressReporter) Update(sessionSummary string, firstMsg string) {
	p.current++

	// Calculate progress percentage
	pct := float64(p.current) / float64(p.total) * 100

	// Draw progress bar (50 chars wide)
	barWidth := 50
	filled := int(float64(barWidth) * float64(p.current) / float64(p.total))
	bar := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)

	// Truncate display text to fit terminal
	displayText := sessionSummary
	if len(displayText) > 60 {
		displayText = displayText[:57] + "..."
	}

	// Calculate ETA
	elapsed := time.Since(p.startTime)
	rate := float64(p.current) / elapsed.Seconds()
	remaining := float64(p.total-p.current) / rate
	eta := time.Duration(remaining) * time.Second

	// Print progress
	_, _ = fmt.Fprintf(p.writer, "\r[%s] %3.0f%% (%d/%d) ETA: %s | %s",
		bar, pct, p.current, p.total, eta.Round(time.Second), displayText)

	p.lastMsg = displayText
}

// Finish completes the progress display
func (p *ProgressReporter) Finish() {
	elapsed := time.Since(p.startTime)
	_, _ = fmt.Fprintf(p.writer, "\nCompleted: Imported %d sessions in %s\n", p.total, elapsed.Round(time.Millisecond))
}
