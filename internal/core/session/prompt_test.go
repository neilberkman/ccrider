package session

import (
	"strings"
	"testing"
	"time"
)

func TestParseSessionTime(t *testing.T) {
	want := time.Date(2026, 6, 4, 14, 40, 56, 0, time.UTC)
	cases := []string{
		"2026-06-04 14:40:56",
		"2026-06-04T14:40:56Z",
	}
	for _, in := range cases {
		if got := ParseSessionTime(in); !got.Equal(want) {
			t.Errorf("ParseSessionTime(%q) = %v, want %v", in, got, want)
		}
	}
	if got := ParseSessionTime("garbage"); !got.IsZero() {
		t.Errorf("ParseSessionTime(garbage) = %v, want zero time", got)
	}
}

func TestRenderResumePrompt(t *testing.T) {
	template := "Last in {{last_cwd}}.{{#different_directory}} MOVE to {{last_cwd}}.{{/different_directory}}{{#same_directory}} Same dir.{{/same_directory}}"

	t.Run("different directory branch", func(t *testing.T) {
		got := RenderResumePrompt(template, "/proj", "/worktree", "2026-06-04 14:40:56")
		if !strings.Contains(got, "MOVE to /worktree") {
			t.Errorf("prompt = %q, want different_directory branch rendered", got)
		}
		if strings.Contains(got, "Same dir") {
			t.Errorf("prompt = %q, must not render same_directory branch", got)
		}
	})

	t.Run("same directory branch", func(t *testing.T) {
		got := RenderResumePrompt(template, "/proj", "/proj", "2026-06-04 14:40:56")
		if !strings.Contains(got, "Same dir") {
			t.Errorf("prompt = %q, want same_directory branch rendered", got)
		}
	})

	t.Run("bad template falls back", func(t *testing.T) {
		got := RenderResumePrompt("{{#unclosed}}", "/proj", "/worktree", "")
		want := "Resuming session. You were last in: /worktree"
		if got != want {
			t.Errorf("prompt = %q, want fallback %q", got, want)
		}
	})
}

func TestRenderResumePromptOneLine(t *testing.T) {
	got := RenderResumePromptOneLine("line1\nline2\r\nline3", "/proj", "/proj", "")
	if strings.ContainsAny(got, "\n\r") {
		t.Errorf("one-line prompt still contains newlines: %q", got)
	}
	if got != "line1 line2  line3" {
		t.Errorf("one-line prompt = %q", got)
	}
}
