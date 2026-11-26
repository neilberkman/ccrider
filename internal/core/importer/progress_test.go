package importer

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mattn/go-runewidth"
)

// frames splits interactive progress output into the lines drawn by each
// redraw, without the carriage return and erase-line codes around them.
func frames(out string) []string {
	var got []string
	for _, frame := range strings.Split(out, "\r") {
		if frame == "" {
			continue
		}
		got = append(got, strings.TrimSuffix(frame, "\x1b[K"))
	}
	return got
}

func TestProgressLinesFitTheTerminal(t *testing.T) {
	long := "Read /Users/someone/projects/lanes/2026-11-25-a-long-lane-name/brief.md and follow it exactly"
	for _, width := range []int{40, 80, 136, 200} {
		var buf bytes.Buffer
		p := NewProgressReporter(&buf, 1045, true)
		p.termWidth = func() int { return width }
		for range 3 {
			p.Update(long, "")
		}
		p.Skip()

		got := frames(buf.String())
		if len(got) != 4 {
			t.Fatalf("width %d: %d frames, want 4: %q", width, len(got), buf.String())
		}
		for _, frame := range got {
			if strings.ContainsAny(frame, "\n") {
				t.Errorf("width %d: frame contains a newline: %q", width, frame)
			}
			if w := runewidth.StringWidth(frame); w >= width {
				t.Errorf("width %d: frame is %d columns, want fewer than %d: %q", width, w, width, frame)
			}
			if !strings.Contains(frame, "(") {
				t.Errorf("width %d: frame lost its count: %q", width, frame)
			}
		}
	}
}

func TestProgressLineFlattensMultilineLabels(t *testing.T) {
	var buf bytes.Buffer
	p := NewProgressReporter(&buf, 10, true)
	p.termWidth = func() int { return 200 }
	p.Update("\n\n<pasted_content>\nfirst line\n\tsecond line", "")

	frame := frames(buf.String())[0]
	if !strings.HasSuffix(frame, "| <pasted_content> first line second line") {
		t.Errorf("frame = %q, want the label on one line", frame)
	}
}

func TestProgressLineOnANarrowTerminal(t *testing.T) {
	status := " 50% (5/10) ETA: 1s"
	if got := progressLine(200, 0.5, status, "label"); !strings.HasPrefix(got, "["+strings.Repeat("█", maxBarWidth/2)) {
		t.Errorf("wide terminal: %q, want a full-width bar", got)
	}
	// Too narrow for the label: the bar narrows and the label is dropped.
	if got := progressLine(40, 0.5, status, "label"); strings.Contains(got, "label") || !strings.HasPrefix(got, "[") {
		t.Errorf("width 40: %q, want a narrower bar without the label", got)
	}
	// Too narrow for any bar: only the status remains.
	if got := progressLine(20, 0.5, status, "label"); got != "50% (5/10) ETA: 1s" {
		t.Errorf("width 20: %q, want only the status", got)
	}
}

func TestProgressNonInteractiveOutputUnchanged(t *testing.T) {
	var buf bytes.Buffer
	p := NewProgressReporter(&buf, 10, false)
	for range 10 {
		p.Skip()
	}
	if strings.ContainsAny(buf.String(), "\r\x1b") {
		t.Errorf("non-interactive output has terminal codes: %q", buf.String())
	}
	if lines := strings.Count(buf.String(), "\n"); lines != 10 {
		t.Errorf("non-interactive output has %d lines, want one per 10%%: %q", lines, buf.String())
	}
}
