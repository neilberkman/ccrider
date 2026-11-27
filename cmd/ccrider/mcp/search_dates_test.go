package mcp

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/neilberkman/ccrider/internal/core/db"
	"github.com/neilberkman/ccrider/internal/core/liveness"
)

// search_sessions documents before_date and after_date as ISO 8601 dates such
// as 2025-01-01, so a search with one must return the sessions on that side.
func TestSearchSessionsHonorsISODates(t *testing.T) {
	// Keep the automatic transcript sync inside an empty test environment.
	t.Setenv("HOME", t.TempDir())
	database, err := db.New(filepath.Join(t.TempDir(), "dates.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	for _, s := range []struct{ id, ts string }{
		{"session-2024", "2024-06-01 10:00:00 +0000 UTC"},
		{"session-2026", "2026-06-01 10:00:00 +0000 UTC"},
	} {
		res, err := database.Exec(`
			INSERT INTO sessions (session_id, project_path, summary, created_at, updated_at)
			VALUES (?, '/project', 'Parser work', ?, ?)
		`, s.id, s.ts, s.ts)
		if err != nil {
			t.Fatal(err)
		}
		rowID, err := res.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(`
			INSERT INTO messages (uuid, session_id, type, text_content, timestamp, sequence)
			VALUES (?, ?, 'user', 'fix the parser', ?, 1)
		`, s.id+"-msg", rowID, s.ts); err != nil {
			t.Fatal(err)
		}
	}
	handler := makeSearchSessionsHandler(database, &liveness.Cache{}, newServerSyncer(database))

	for _, tc := range []struct {
		arg, want, notWant string
	}{
		{"before_date", "session-2024", "session-2026"},
		{"after_date", "session-2026", "session-2024"},
	} {
		result, err := handler(context.Background(), mcp.CallToolRequest{Params: mcp.CallToolParams{
			Name:      "search_sessions",
			Arguments: map[string]any{"query": "parser", tc.arg: "2025-01-01"},
		}})
		if err != nil {
			t.Fatal(err)
		}
		if result.IsError || len(result.Content) != 1 {
			t.Fatalf("%s: result = %+v", tc.arg, result)
		}
		text := result.Content[0].(mcp.TextContent).Text
		if !strings.Contains(text, tc.want) || strings.Contains(text, tc.notWant) {
			t.Errorf("%s=2025-01-01: response = %s, want %s and not %s", tc.arg, text, tc.want, tc.notWant)
		}
	}
}

// A date search_sessions can't read is an error for the caller, returned at
// once: retrying an anchor search can't make the date readable.
func TestSearchSessionsRejectsUnreadableDates(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	database, err := db.New(filepath.Join(t.TempDir(), "bad-dates.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	handler := makeSearchSessionsHandler(database, &liveness.Cache{}, newServerSyncer(database))

	for _, args := range []map[string]any{
		{"query": "parser", "before_date": "next tuesday"},
		{"query": "parser", "after_date": "next tuesday", "anchor_phrase": "unique words"},
	} {
		start := time.Now()
		result, err := handler(context.Background(), mcp.CallToolRequest{Params: mcp.CallToolParams{
			Name: "search_sessions", Arguments: args,
		}})
		if err != nil {
			t.Fatal(err)
		}
		if !result.IsError {
			t.Errorf("%v: result = %+v, want a tool error", args, result)
		} else if text := result.Content[0].(mcp.TextContent).Text; !strings.Contains(text, "next tuesday") {
			t.Errorf("%v: error = %q, want it to name the unreadable date", args, text)
		}
		if elapsed := time.Since(start); elapsed > time.Second {
			t.Errorf("%v: took %v, want the error without retries", args, elapsed)
		}
	}
}
