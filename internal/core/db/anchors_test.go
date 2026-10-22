package db

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestSessionAnchorsPersistAcrossUpgradeAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "anchors.db")
	database, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	// Emulate the previous schema while preserving an existing session.
	if _, err := database.Exec(`INSERT INTO sessions (session_id, project_path) VALUES ('existing', '/repo'); DROP TABLE session_anchors`); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	database, err = New(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	plain, err := database.GenerateSessionAnchor(ctx, GenerateSessionAnchorOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(strings.Split(plain.Anchor, "-")) != 4 {
		t.Fatalf("default anchor = %q, want four words", plain.Anchor)
	}
	first, err := database.GenerateSessionAnchor(ctx, GenerateSessionAnchorOptions{Label: "Add REST endpoint", SessionID: "existing"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := database.GenerateSessionAnchor(ctx, GenerateSessionAnchorOptions{Label: "Add REST endpoint"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(first.Anchor, "add-rest-endpoint-") || first.Anchor == second.Anchor {
		t.Fatalf("expected distinct descriptive tags, got %q and %q", first.Anchor, second.Anchor)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	database, err = New(path)
	if err != nil {
		t.Fatal(err)
	}
	anchors, err := database.ListSessionAnchors(ctx, SessionAnchorFilters{})
	if err != nil {
		t.Fatal(err)
	}
	if len(anchors) != 3 || anchors[0].Anchor != second.Anchor || anchors[2].Anchor != plain.Anchor {
		t.Fatalf("anchors after reopen = %+v", anchors)
	}
	bound := anchors[1]
	if bound.Label != "Add REST endpoint" || !bound.CreatedAt.Equal(first.CreatedAt) ||
		bound.SessionID != "existing" || len(bound.Sessions) != 1 || bound.Sessions[0].SessionID != "existing" {
		t.Fatalf("bound anchor after reopen = %+v", bound)
	}
	if len(anchors[0].Sessions) != 0 || len(anchors[2].Sessions) != 0 {
		t.Fatalf("unmatched anchors should remain visible with no sessions: %+v", anchors)
	}
}

func TestSessionAnchorsDiscoverLiteralHistoricalMatchesAndFilter(t *testing.T) {
	database := newSessionTestDB(t)
	ctx := context.Background()
	anchor, err := database.GenerateSessionAnchor(ctx, GenerateSessionAnchorOptions{Label: "REST endpoint"})
	if err != nil {
		t.Fatal(err)
	}
	anchors, err := database.ListSessionAnchors(ctx, SessionAnchorFilters{})
	if err != nil || len(anchors) != 1 || len(anchors[0].Sessions) != 0 {
		t.Fatalf("pending anchor = %+v, err = %v", anchors, err)
	}
	claudeID := "8650b828-b3a9-4db4-81c0-64d0fee6dd70"
	codexID := "rollout-2026-07-24T08-08-11-019f94ab-7288-7043-b7ef-5e5b852ed3a5"
	// FTS sees these as equivalent tokens, but only one contains the literal tag.
	if _, err := database.Exec(`UPDATE messages SET text_content = ?, timestamp = '2020-01-01 00:00:00'
		WHERE session_id = (SELECT id FROM sessions WHERE session_id = ?)`, "Bookmark: "+anchor.Anchor, claudeID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE messages SET text_content = ?
		WHERE session_id = (SELECT id FROM sessions WHERE session_id = ?)`, strings.ReplaceAll(anchor.Anchor, "-", " "), codexID); err != nil {
		t.Fatal(err)
	}
	anchors, err = database.ListSessionAnchors(ctx, SessionAnchorFilters{})
	if err != nil || len(anchors) != 1 || len(anchors[0].Sessions) != 1 || anchors[0].Sessions[0].SessionID != claudeID {
		t.Fatalf("historical literal matches = %+v, err = %v", anchors, err)
	}
	// A copy in another session must be exposed, not silently picked as owner.
	if _, err := database.Exec(`UPDATE messages SET text_content = ?`, anchor.Anchor); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE sessions SET project_path = '/other/repo' WHERE session_id = ?`, codexID); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		filters SessionAnchorFilters
		want    int
		id      string
	}{
		{"all", SessionAnchorFilters{}, 2, ""},
		{"provider", SessionAnchorFilters{Provider: "codex"}, 1, codexID},
		{"project", SessionAnchorFilters{ProjectPath: "test/project"}, 1, claudeID},
		{"bare uuid", SessionAnchorFilters{SessionID: "019f94ab-7288-7043-b7ef-5e5b852ed3a5"}, 1, codexID},
		{"combined", SessionAnchorFilters{ProjectPath: "test", Provider: "codex"}, 0, ""},
		{"literal project", SessionAnchorFilters{ProjectPath: "%"}, 0, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			anchors, err := database.ListSessionAnchors(ctx, tc.filters)
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == 0 {
				if len(anchors) != 0 {
					t.Fatalf("expected no anchors, got %+v", anchors)
				}
				return
			}
			if len(anchors) != 1 || len(anchors[0].Sessions) != tc.want || (tc.id != "" && anchors[0].Sessions[0].SessionID != tc.id) {
				t.Fatalf("filtered anchors = %+v", anchors)
			}
		})
	}
	// A full message reimport must not destroy the registry entry.
	if _, err := database.Exec(`DELETE FROM messages`); err != nil {
		t.Fatal(err)
	}
	anchors, err = database.ListSessionAnchors(ctx, SessionAnchorFilters{})
	if err != nil || len(anchors) != 1 || anchors[0].Anchor != anchor.Anchor || len(anchors[0].Sessions) != 0 {
		t.Fatalf("anchor after message removal = %+v, err = %v", anchors, err)
	}
}

func TestSessionAnchorExplicitBindingAndValidation(t *testing.T) {
	database := newSessionTestDB(t)
	ctx := context.Background()
	codexID := "rollout-2026-07-24T08-08-11-019f94ab-7288-7043-b7ef-5e5b852ed3a5"
	anchor, err := database.GenerateSessionAnchor(ctx, GenerateSessionAnchorOptions{
		Label: "修复 API", SessionID: "019f94ab-7288-7043-b7ef-5e5b852ed3a5",
	})
	if err != nil {
		t.Fatal(err)
	}
	if anchor.SessionID != codexID || !strings.HasPrefix(anchor.Anchor, "修复-api-") {
		t.Fatalf("generated anchor = %+v", anchor)
	}
	// An explicit association is authoritative even if the phrase is copied.
	if _, err := database.Exec(`UPDATE messages SET text_content = ?`, anchor.Anchor); err != nil {
		t.Fatal(err)
	}
	anchors, err := database.ListSessionAnchors(ctx, SessionAnchorFilters{})
	if err != nil || len(anchors) != 1 || len(anchors[0].Sessions) != 1 || anchors[0].Sessions[0].SessionID != codexID {
		t.Fatalf("bound anchor = %+v, err = %v", anchors, err)
	}
	for _, opts := range []GenerateSessionAnchorOptions{
		{Label: strings.Repeat("a", 121)}, {Label: "!?"}, {Label: "a\x00b"},
		{Label: "a\nb"}, {SessionID: "missing-session"},
	} {
		if _, err := database.GenerateSessionAnchor(ctx, opts); err == nil {
			t.Fatalf("expected invalid options to fail: %+v", opts)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := database.GenerateSessionAnchor(canceled, GenerateSessionAnchorOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled generation error = %v", err)
	}
	anchors, err = database.ListSessionAnchors(ctx, SessionAnchorFilters{})
	if err != nil || len(anchors) != 1 {
		t.Fatalf("failed calls must not persist anchors: %+v, err = %v", anchors, err)
	}
}
