package tui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/neilberkman/ccrider/internal/core/db"
)

// runCmd runs cmd, and each command inside it when it is a batch, and returns
// the messages they produce.
func runCmd(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		return []tea.Msg{msg}
	}
	var msgs []tea.Msg
	for _, c := range batch {
		msgs = append(msgs, runCmd(c)...)
	}
	return msgs
}

// Typing starts one search after the typing pauses, not one per keystroke:
// the database runs one query at a time, so searches for every prefix would
// queue ahead of the one the user wants.
func TestSearchWaitsForTypingToPause(t *testing.T) {
	database, err := db.New(filepath.Join(t.TempDir(), "debounce.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	model := NewWithContext(context.Background(), database)
	defer model.Close()
	model.mode = searchView
	model.searchInput.Focus()

	var pauses []searchPauseMsg
	for _, r := range "rep" {
		updated, cmd := model.updateSearch(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		model = updated.(Model)
		for _, msg := range runCmd(cmd) {
			switch msg := msg.(type) {
			case searchResultsMsg:
				t.Fatalf("typing %q started a search right away, want it to wait for a pause", r)
			case searchPauseMsg:
				pauses = append(pauses, msg)
			}
		}
	}
	if len(pauses) != 3 {
		t.Fatalf("got %d pause timers for 3 keystrokes, want 3", len(pauses))
	}

	if _, cmd := model.Update(pauses[0]); cmd != nil {
		t.Errorf("a pause timer from an earlier keystroke returned a command, want none")
	}
	_, cmd := model.Update(pauses[2])
	msgs := runCmd(cmd)
	if len(msgs) != 1 {
		t.Fatalf("latest pause timer produced %d messages, want one search result", len(msgs))
	}
	results, ok := msgs[0].(searchResultsMsg)
	if !ok {
		t.Fatalf("latest pause timer produced %#v, want searchResultsMsg", msgs[0])
	}
	if results.seq != model.searchSeq {
		t.Errorf("search seq = %d, want the current %d", results.seq, model.searchSeq)
	}
}

// The search a pause starts follows the headless toggle, like the list.
func TestSearchAfterPauseFollowsHeadlessToggle(t *testing.T) {
	database, err := db.New(filepath.Join(t.TempDir(), "pause-headless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	for _, s := range []struct{ id, entrypoint string }{
		{"interactive", "cli"},
		{"headless", "sdk-cli"},
	} {
		res, err := database.Exec(`
			INSERT INTO sessions (session_id, project_path, summary, provider, entrypoint, created_at, updated_at)
			VALUES (?, '/p', 'Deploy notes', 'claude', ?, datetime('now'), datetime('now'))
		`, s.id, s.entrypoint)
		if err != nil {
			t.Fatal(err)
		}
		rowID, err := res.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(`
			INSERT INTO messages (uuid, session_id, type, sender, text_content, timestamp, sequence)
			VALUES (?, ?, 'user', 'user', 'check the deploy', datetime('now'), 1)
		`, s.id+"-msg", rowID); err != nil {
			t.Fatal(err)
		}
	}
	model := NewWithContext(context.Background(), database)
	defer model.Close()
	model.mode = searchView
	model.searchInput.SetValue("deploy")
	model.searchSeq = 7

	searchIDs := func() map[string]bool {
		t.Helper()
		_, cmd := model.Update(searchPauseMsg{seq: model.searchSeq})
		msgs := runCmd(cmd)
		if len(msgs) != 1 {
			t.Fatalf("pause produced %d messages, want one search result", len(msgs))
		}
		results, ok := msgs[0].(searchResultsMsg)
		if !ok {
			t.Fatalf("pause produced %#v, want searchResultsMsg", msgs[0])
		}
		ids := make(map[string]bool)
		for _, r := range results.results {
			ids[r.SessionID] = true
		}
		return ids
	}

	if ids := searchIDs(); !ids["interactive"] || ids["headless"] {
		t.Errorf("headless hidden: search found %v, want only interactive", ids)
	}
	model.showHeadless = true
	if ids := searchIDs(); !ids["interactive"] || !ids["headless"] {
		t.Errorf("headless shown: search found %v, want both sessions", ids)
	}
}

// A keystroke cancels the search already running, so the slow search for a
// short prefix can't hold the database while the user keeps typing. The
// cancelled search reports nothing: no stale results and no error.
func TestKeystrokeCancelsRunningSearch(t *testing.T) {
	database, err := db.New(filepath.Join(t.TempDir(), "cancel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	model := NewWithContext(context.Background(), database)
	defer model.Close()
	model.mode = searchView
	model.searchInput.Focus()

	for _, r := range "re" {
		updated, _ := model.updateSearch(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		model = updated.(Model)
	}
	updated, running := model.Update(searchPauseMsg{seq: model.searchSeq})
	model = updated.(Model)
	updated, _ = model.updateSearch(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	model = updated.(Model)

	if msgs := runCmd(running); len(msgs) != 1 || msgs[0] != nil {
		t.Errorf("search overtaken by a keystroke produced %#v, want nothing", msgs)
	}
}

// The search view tells apart a query too short to run, a search still
// running, and a search that found nothing.
func TestSearchViewStates(t *testing.T) {
	database, err := db.New(filepath.Join(t.TempDir(), "states.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()

	results := func(query string) []searchResult {
		t.Helper()
		msg, ok := performSearch(context.Background(), database, query, true, 1)().(searchResultsMsg)
		if !ok {
			t.Fatalf("performSearch(%q) did not return searchResultsMsg", query)
		}
		return msg.results
	}
	if got := results("w"); got != nil {
		t.Errorf("one-character query: results = %v, want nil (keep typing)", got)
	}
	if got := results("nothingmatchesthis"); got == nil || len(got) != 0 {
		t.Errorf("query with no matches: results = %#v, want an empty, non-nil list", got)
	}

	model := NewWithContext(context.Background(), database)
	defer model.Close()
	model.mode = searchView
	model.width, model.height = 120, 40
	model.showHeadless = true
	for _, tc := range []struct {
		name    string
		pending bool
		results []searchResult
		want    string
	}{
		{"too short", false, nil, "Type to search"},
		{"running", true, nil, "Searching…"},
		{"running, earlier results on hand", true, []searchResult{{SessionID: "old", Summary: "Earlier query"}}, "Searching…"},
		{"found nothing", false, []searchResult{}, "No results found"},
	} {
		model.searchPending, model.searchResults = tc.pending, tc.results
		view := model.viewSearch()
		if !strings.Contains(view, tc.want) {
			t.Errorf("%s: view = %q, want %q", tc.name, view, tc.want)
		}
		if tc.pending && strings.Contains(view, "Earlier query") {
			t.Errorf("%s: view shows an earlier query's results while searching", tc.name)
		}
	}

	// Enter while a search runs must not open a result from the earlier query.
	model.searchPending = true
	model.searchResults = []searchResult{{SessionID: "old", Summary: "Earlier query"}}
	if _, cmd := model.updateSearch(tea.KeyMsg{Type: tea.KeyEnter}); cmd != nil {
		t.Error("enter while searching returned a command, want none")
	}
}

// A filter-only search lists sessions without matching text, so its rows show
// no match count.
func TestFilterOnlyResultsShowNoMatchCount(t *testing.T) {
	database, err := db.New(filepath.Join(t.TempDir(), "filter-only.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	model := NewWithContext(context.Background(), database)
	defer model.Close()
	model.mode = searchView
	model.width, model.height = 120, 40
	model.showHeadless = true
	model.searchResults = []searchResult{
		{SessionID: "listed", Summary: "Listed by date", UpdatedAt: "2026-11-27T10:00:00Z"},
		{SessionID: "matched", Summary: "Matched text", UpdatedAt: "2026-11-27T10:00:00Z", Matches: []matchInfo{{Snippet: "the parser"}}},
	}
	view := model.viewSearch()
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "Listed by date") && strings.Contains(line, "match") {
			t.Errorf("filter-only row = %q, want no match count", line)
		}
		if strings.Contains(line, "Matched text") && !strings.Contains(line, "(1 match)") {
			t.Errorf("text match row = %q, want (1 match)", line)
		}
	}
}

// Search rows name the agent, as the session list does, so runs of the same
// brief by different agents can be told apart.
func TestSearchRowsShowProvider(t *testing.T) {
	database, err := db.New(filepath.Join(t.TempDir(), "provider.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	model := NewWithContext(context.Background(), database)
	defer model.Close()
	model.mode = searchView
	model.width, model.height = 120, 40
	model.showHeadless = true
	model.searchResults = []searchResult{
		{SessionID: "a", Provider: "codex", Summary: "Read the brief", UpdatedAt: "2026-11-27T10:00:00Z"},
		{SessionID: "b", Provider: "antigravity", Summary: "Read the brief", UpdatedAt: "2026-11-27T10:00:00Z"},
	}
	view := model.viewSearch()
	for _, want := range []string{"[codex] Read the brief", "[antigravity] Read the brief"} {
		if !strings.Contains(view, want) {
			t.Errorf("view = %q, want a row %q", view, want)
		}
	}
}
