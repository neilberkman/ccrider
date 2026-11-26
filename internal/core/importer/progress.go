package importer

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/mattn/go-runewidth"
)

const (
	// maxBarWidth is the bar's width on a terminal wide enough for it.
	maxBarWidth = 50
	// minBarWidth is the narrowest bar drawn; a terminal with no room for it
	// shows only the status.
	minBarWidth = 10
	// fallbackTermWidth is used when the terminal's width cannot be read.
	fallbackTermWidth = 80
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
	// termWidth reports the terminal's current width in columns. It is read
	// on every redraw so a resized window is followed.
	termWidth func() int
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
		termWidth:   terminalWidth(w),
	}
}

// terminalWidth returns a function reporting w's width in columns, or
// fallbackTermWidth when w is not a terminal or its size cannot be read.
func terminalWidth(w io.Writer) func() int {
	f, ok := w.(*os.File)
	if !ok || !term.IsTerminal(f.Fd()) {
		return func() int { return fallbackTermWidth }
	}
	return func() int {
		if width, _, err := term.GetSize(f.Fd()); err == nil && width > 0 {
			return width
		}
		return fallbackTermWidth
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
	p.render(sessionSummary)
}

func (p *ProgressReporter) render(sessionSummary string) {
	// Calculate progress percentage
	pct := float64(p.current) / float64(p.total) * 100

	if !p.interactive {
		if decile := int(pct) / 10; decile > p.lastDecile {
			p.lastDecile = decile
			_, _ = fmt.Fprintf(p.writer, "%3d%% (%d/%d)\n", decile*10, p.current, p.total)
		}
		return
	}

	// Calculate ETA
	elapsed := time.Since(p.startTime)
	rate := float64(p.current) / elapsed.Seconds()
	remaining := float64(p.total-p.current) / rate
	eta := time.Duration(remaining) * time.Second

	status := fmt.Sprintf(" %3.0f%% (%d/%d) ETA: %s", pct, p.current, p.total, eta.Round(time.Second))
	label := strings.Join(strings.Fields(sessionSummary), " ")
	line := progressLine(p.termWidth(), float64(p.current)/float64(p.total), status, label)

	// \r returns to the start of the row and \x1b[K clears what a longer
	// previous line left behind. The line never reaches the last column: a
	// line that wraps leaves its first row behind on every redraw.
	_, _ = fmt.Fprintf(p.writer, "\r%s\x1b[K", line)

	p.lastMsg = label
}

// progressLine lays out "[bar] status | label" in fewer than width columns.
// The bar narrows to make room for the status, and the label takes whatever
// is left; on a terminal too narrow for both, the label is dropped and then
// the bar.
func progressLine(width int, fraction float64, status, label string) string {
	avail := width - 1
	statusWidth := runewidth.StringWidth(status)

	barWidth := min(maxBarWidth, avail-statusWidth-2)
	if barWidth < minBarWidth {
		return runewidth.Truncate(strings.TrimSpace(status), max(avail, 0), "")
	}
	filled := min(barWidth, max(0, int(float64(barWidth)*fraction)))
	line := "[" + strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled) + "]" + status

	const sep = " | "
	if room := avail - runewidth.StringWidth(line) - len(sep); label != "" && room >= 4 {
		line += sep + runewidth.Truncate(label, room, "…")
	}
	return line
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
