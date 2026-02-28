package tui

import (
	"strings"
	"testing"
)

func TestSessionListItem_Title_Claude(t *testing.T) {
	item := sessionListItem{session: sessionItem{
		ID:       "abc123def456",
		Summary:  "Fix auth bug",
		Provider: "claude",
	}}

	title := item.Title()
	if title != "Fix auth bug" {
		t.Errorf("Title() = %q, want %q", title, "Fix auth bug")
	}
	if strings.Contains(title, "[claude]") {
		t.Error("Claude sessions should NOT have a [claude] prefix")
	}
}

func TestSessionListItem_Title_Codex(t *testing.T) {
	item := sessionListItem{session: sessionItem{
		ID:       "abc123def456",
		Summary:  "Refactor database",
		Provider: "codex",
	}}

	title := item.Title()
	if !strings.HasPrefix(title, "[codex] ") {
		t.Errorf("Title() = %q, want prefix [codex]", title)
	}
	if !strings.Contains(title, "Refactor database") {
		t.Errorf("Title() = %q, should contain summary", title)
	}
}

func TestSessionListItem_Title_EmptySummary(t *testing.T) {
	item := sessionListItem{session: sessionItem{
		ID:       "abc123def456789abc",
		Summary:  "",
		Provider: "codex",
	}}

	title := item.Title()
	if !strings.HasPrefix(title, "[codex] ") {
		t.Errorf("Title() = %q, want prefix [codex]", title)
	}
	if !strings.Contains(title, "abc123def456") {
		t.Errorf("Title() = %q, should fall back to truncated session ID", title)
	}
	if !strings.HasSuffix(title, "...") {
		t.Errorf("Title() = %q, should end with ... for long IDs", title)
	}
}

func TestSessionListItem_Title_ShortID(t *testing.T) {
	item := sessionListItem{session: sessionItem{
		ID:       "short",
		Summary:  "",
		Provider: "",
	}}

	title := item.Title()
	if title != "short" {
		t.Errorf("Title() = %q, want %q (short ID should not panic)", title, "short")
	}
}

func TestSessionListItem_Title_EmptyProvider(t *testing.T) {
	item := sessionListItem{session: sessionItem{
		ID:       "abc123def456",
		Summary:  "Some task",
		Provider: "",
	}}

	title := item.Title()
	if title != "Some task" {
		t.Errorf("Title() = %q, want %q (no prefix for empty provider)", title, "Some task")
	}
}

func TestSessionListItem_FilterValue(t *testing.T) {
	item := sessionListItem{session: sessionItem{
		Summary: "Fix bug",
		LastCwd: "/home/user/project",
	}}

	fv := item.FilterValue()
	if !strings.Contains(fv, "Fix bug") || !strings.Contains(fv, "/home/user/project") {
		t.Errorf("FilterValue() = %q, should contain summary and lastCwd", fv)
	}
}

func TestSessionListItem_Description(t *testing.T) {
	item := sessionListItem{session: sessionItem{
		LastCwd:      "/home/user/project",
		MessageCount: 42,
		UpdatedAt:    "2026-02-28 10:30:00",
	}}

	desc := item.Description()
	if !strings.Contains(desc, "42 messages") {
		t.Errorf("Description() = %q, should contain message count", desc)
	}
}
