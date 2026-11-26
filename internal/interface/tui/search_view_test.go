package tui

import (
	"context"
	"path/filepath"
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
