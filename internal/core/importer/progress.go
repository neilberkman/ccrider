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
	// Skip advances progress for a unit that needed no import (unchanged, or
	// unreadable). On a routine sync nearly every file is unchanged, so a bar
	// fed only by Update sits at 0% for the whole run and reads as hung.
	Skip()
	Finish()
}

// ProgressReporter handles progress feedback during import
type ProgressReporter struct {
	writer      io.Writer
	total       int
	current     int
	imported    int
	startTime   time.Time
	lastMsg     string
	interactive bool
	lastDecile  int
}

// NewProgressReporter creates a new progress reporter. When interactive is
// false (e.g. output is piped or redirected), the animated progress bar is
// replaced with one plain line per 10% of progress.
func NewProgressReporter(w io.Writer, total int, interactive bool) *ProgressReporter {
	return &ProgressReporter{
		writer:      w,
		total:       total,
		current:     0,
		startTime:   time.Now(),
		interactive: interactive,
	}
}

// Update updates the progress bar with current session info
func (p *ProgressReporter) Update(sessionSummary string, firstMsg string) {
	p.imported++
	p.advance(sessionSummary)
}

// Skip advances the bar for a unit that required no import, keeping the last
// imported session's label on screen.
func (p *ProgressReporter) Skip() {
	p.advance(p.lastMsg)
}

func (p *ProgressReporter) advance(sessionSummary string) {
	p.current++

	// Calculate progress percentage
	pct := float64(p.current) / float64(p.total) * 100

	if !p.interactive {
		if decile := int(pct) / 10; decile > p.lastDecile {
			p.lastDecile = decile
			_, _ = fmt.Fprintf(p.writer, "%3d%% (%d/%d)\n", decile*10, p.current, p.total)
		}
		return
	}

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
	if p.interactive {
		_, _ = fmt.Fprintln(p.writer)
	}
	// Report actual imports, not the unit count: a routine sync skips nearly
	// everything, and claiming every file was imported hides that.
	_, _ = fmt.Fprintf(p.writer, "Completed: Imported %d sessions in %s\n", p.imported, elapsed.Round(time.Millisecond))
}
