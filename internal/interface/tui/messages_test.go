package tui

import (
	"context"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/neilberkman/ccrider/internal/core/db"
)

func TestSyncManagerCloseCancelsAndWaits(t *testing.T) {
	manager := newSyncManager(context.Background())
	started := make(chan struct{})
	finished := make(chan struct{})

	if !manager.start(func(ctx context.Context) {
		close(started)
		<-ctx.Done()
		close(finished)
	}, nil) {
		t.Fatal("start() = false, want true")
	}
	<-started

	manager.close()

	select {
	case <-finished:
	default:
		t.Fatal("close() returned before active work finished")
	}
	if manager.start(func(context.Context) {}, nil) {
		t.Fatal("start() after close = true, want false")
	}
}

func TestSyncManagerPreventsOverlapAndAllowsRestart(t *testing.T) {
	manager := newSyncManager(context.Background())
	defer manager.close()

	release := make(chan struct{})
	done := make(chan struct{})
	if !manager.start(func(context.Context) {
		<-release
		close(done)
	}, nil) {
		t.Fatal("first start() = false, want true")
	}
	if manager.start(func(context.Context) {}, nil) {
		t.Fatal("overlapping start() = true, want false")
	}
	close(release)
	<-done

	deadline := time.Now().Add(time.Second)
	for !manager.start(func(context.Context) {}, nil) {
		if time.Now().After(deadline) {
			t.Fatal("start() remained false after previous work completed")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestSyncManagerOnDoneRunsAfterManagerBecomesRestartable(t *testing.T) {
	manager := newSyncManager(context.Background())
	defer manager.close()

	restartAccepted := make(chan bool, 1)
	if !manager.start(func(context.Context) {}, func() {
		restartAccepted <- manager.start(func(context.Context) {}, nil)
	}) {
		t.Fatal("initial start() = false, want true")
	}
	if accepted := <-restartAccepted; !accepted {
		t.Fatal("manager remained active when completion became observable")
	}
}

func TestChannelProgressReporterDoesNotBlockWhenFull(t *testing.T) {
	progressCh := make(chan syncEvent, 2)
	reporter := &channelProgressReporter{ch: progressCh}
	reporter.send()

	done := make(chan struct{})
	go func() {
		reporter.send()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("send() blocked on a full progress channel")
	}
}

func TestChannelProgressReporterScopesEachProvider(t *testing.T) {
	progressCh := make(chan syncEvent, 6)
	reporter := &channelProgressReporter{ch: progressCh}

	reporter.beginSource("claude", 2)
	reporter.Update("first", "")
	reporter.Update("second", "")
	reporter.beginSource("amp", 1)
	reporter.Skip()

	want := []struct {
		provider string
		current  int
		total    int
	}{
		{provider: "claude", current: 0, total: 2},
		{provider: "claude", current: 1, total: 2},
		{provider: "claude", current: 2, total: 2},
		{provider: "amp", current: 0, total: 1},
		{provider: "amp", current: 1, total: 1},
	}
	for index, expected := range want {
		got := (<-progressCh).progress
		if got.provider != expected.provider || got.current != expected.current || got.total != expected.total {
			t.Fatalf("progress message %d = %+v, want provider %q at %d/%d", index, got, expected.provider, expected.current, expected.total)
		}
	}
}

func TestChannelProgressReporterCompletionCarriesWarningsWhenFull(t *testing.T) {
	progressCh := make(chan syncEvent, 1)
	reporter := &channelProgressReporter{ch: progressCh}
	reporter.send()
	reporter.complete([]string{"amp sync timed out"})

	msg := syncSubscribe(progressCh)()
	finished, ok := msg.(syncFinishedMsg)
	if !ok || !reflect.DeepEqual(finished.warnings, []string{"amp sync timed out"}) {
		t.Fatalf("completion message = %#v, want visible sync warning", msg)
	}
}

func TestViewListNamesSyncProvider(t *testing.T) {
	model := Model{
		initialLoad:  false,
		syncing:      true,
		syncProvider: "amp",
		syncCurrent:  1,
		syncTotal:    2,
		width:        80,
	}

	if got := model.viewList(); !strings.Contains(got, "Syncing amp") {
		t.Fatalf("viewList() = %q, want provider-scoped sync status", got)
	}
}

func TestSessionsLoadedMessageIgnoresStaleGeneration(t *testing.T) {
	model := Model{
		sessions:               []sessionItem{{ID: "current"}},
		sessionsLoadGeneration: 2,
	}

	updated, _ := model.Update(sessionsLoadedMsg{
		sessions:   []sessionItem{{ID: "stale"}},
		generation: 1,
	})
	got := updated.(Model)
	if len(got.sessions) != 1 || got.sessions[0].ID != "current" {
		t.Fatalf("stale load replaced sessions: %+v", got.sessions)
	}
}

func TestSessionsLoadFailureIgnoresStaleGeneration(t *testing.T) {
	model := Model{sessionsLoadGeneration: 2}
	staleErr := context.Canceled

	updated, _ := model.Update(sessionsLoadFailedMsg{err: staleErr, generation: 1})
	if got := updated.(Model).err; got != nil {
		t.Fatalf("stale load error replaced current state: %v", got)
	}
}

func TestApplySessionsRestoresSelectedSessionByID(t *testing.T) {
	model := Model{
		width:            80,
		height:           24,
		savedCursorIndex: 1,
		savedSessionID:   "selected",
	}
	model.applySessions([]sessionItem{
		{ID: "new"},
		{ID: "other"},
		{ID: "selected"},
	}, true)

	selected, ok := model.list.SelectedItem().(sessionListItem)
	if !ok || selected.session.ID != "selected" {
		t.Fatalf("selected item = %#v, want selected session ID", model.list.SelectedItem())
	}
}

func TestSyncFinishedReloadsAsynchronouslyAndShowsWarnings(t *testing.T) {
	database, err := db.New(filepath.Join(t.TempDir(), "sync-finished.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()

	model := NewWithContext(context.Background(), database)
	defer model.Close()
	updated, cmd := model.Update(syncFinishedMsg{warnings: []string{"amp sync timed out"}})
	if cmd == nil {
		t.Fatal("syncFinishedMsg returned nil command, want asynchronous reload")
	}
	got := updated.(Model)
	if got.syncing {
		t.Fatal("syncing remains true after synchronous reload")
	}
	if !strings.Contains(got.View(), "amp sync timed out") {
		t.Fatalf("View() = %q, want user-visible sync warning", got.View())
	}
	loaded, ok := cmd().(sessionsLoadedMsg)
	if !ok || !loaded.restoreSelection {
		t.Fatalf("reload message = %#v, want selection-restoring session load", loaded)
	}
}

func TestHeadlessSessionsHiddenUntilToggled(t *testing.T) {
	database, err := db.New(filepath.Join(t.TempDir(), "headless.db"))
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
			VALUES (?, '/p', 'Test session', 'claude', ?, datetime('now'), datetime('now'))
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
			VALUES (?, ?, 'user', 'user', 'hello', datetime('now'), 1)
		`, s.id+"-msg", rowID); err != nil {
			t.Fatal(err)
		}
	}

	loadedIDs := func(msg tea.Msg) []string {
		t.Helper()
		loaded, ok := msg.(sessionsLoadedMsg)
		if !ok {
			t.Fatalf("load message = %#v, want sessionsLoadedMsg", msg)
		}
		var ids []string
		for _, s := range loaded.sessions {
			ids = append(ids, s.ID)
		}
		return ids
	}

	model := NewWithContext(context.Background(), database)
	defer model.Close()
	if model.showHeadless {
		t.Fatal("showHeadless = true on a new model, want headless sessions hidden by default")
	}
	if got := loadedIDs(loadSessionsNow(database, false, model.showHeadless, "", 1, false)); !reflect.DeepEqual(got, []string{"interactive"}) {
		t.Errorf("default load = %v, want [interactive]", got)
	}

	updated, cmd := model.updateList(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if !updated.(Model).showHeadless {
		t.Fatal("showHeadless = false after pressing a, want true")
	}
	if got := loadedIDs(cmd()); len(got) != 2 {
		t.Errorf("load after pressing a = %v, want both sessions", got)
	}

	updated, cmd = updated.(Model).updateList(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if updated.(Model).showHeadless {
		t.Fatal("showHeadless = true after pressing a twice, want false")
	}
	if got := loadedIDs(cmd()); !reflect.DeepEqual(got, []string{"interactive"}) {
		t.Errorf("load after pressing a twice = %v, want [interactive]", got)
	}
}

func TestListViewShowsWhetherHeadlessSessionsAreHidden(t *testing.T) {
	database, err := db.New(filepath.Join(t.TempDir(), "headless-view.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	model := NewWithContext(context.Background(), database)
	defer model.Close()
	model.initialLoad = false
	model.syncing = false

	for _, tc := range []struct {
		showHeadless bool
		footer       string
		empty        string
	}{
		{false, "a show headless", "No sessions found. Headless sessions are hidden: press 'a' to show them, or 's' to sync."},
		{true, "a hide headless", "No sessions found. Press 's' to sync."},
	} {
		model.showHeadless = tc.showHeadless
		view := model.viewList()
		if !strings.Contains(view, tc.footer) {
			t.Errorf("showHeadless=%v: view = %q, want footer containing %q", tc.showHeadless, view, tc.footer)
		}
		if !strings.HasPrefix(view, tc.empty) {
			t.Errorf("showHeadless=%v: view = %q, want empty state %q", tc.showHeadless, view, tc.empty)
		}
	}
}

// pastedFirstPrompt is a first prompt the way Claude Code stores a paste:
// blank lines, then a wrapper tag around a multi-line document.
const pastedFirstPrompt = "\n\n<pasted_content>\nRefactor the parser\nso it streams\n</pasted_content>"

func TestListTitlesPastedFirstPromptOnOneLine(t *testing.T) {
	database, err := db.New(filepath.Join(t.TempDir(), "pasted.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	res, err := database.Exec(`
		INSERT INTO sessions (session_id, project_path, summary, provider, created_at, updated_at)
		VALUES ('pasted', '/p', '', 'claude', datetime('now'), datetime('now'))
	`)
	if err != nil {
		t.Fatal(err)
	}
	rowID, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		INSERT INTO messages (uuid, session_id, type, sender, text_content, timestamp, sequence)
		VALUES ('pasted-msg', ?, 'user', 'user', ?, datetime('now'), 1)
	`, rowID, pastedFirstPrompt); err != nil {
		t.Fatal(err)
	}

	loaded, ok := loadSessionsNow(database, false, true, "", 1, false).(sessionsLoadedMsg)
	if !ok || len(loaded.sessions) != 1 {
		t.Fatalf("load = %#v, want one session", loaded)
	}
	want := "Refactor the parser so it streams"
	if got := loaded.sessions[0].Summary; got != want {
		t.Errorf("Summary = %q, want %q", got, want)
	}
}

func TestSearchViewTitlesPastedFirstPromptOnOneLine(t *testing.T) {
	database, err := db.New(filepath.Join(t.TempDir(), "pasted-search.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	model := NewWithContext(context.Background(), database)
	defer model.Close()
	model.width, model.height = 120, 40
	model.searchResults = []searchResult{{SessionID: "pasted", Summary: pastedFirstPrompt, Project: "/p"}}

	view := model.viewSearch()
	if !strings.Contains(view, "Refactor the parser so it streams") {
		t.Errorf("view = %q, want the prompt joined onto one title line", view)
	}
	if strings.Contains(view, "<pasted_content>") {
		t.Errorf("view = %q, want the leading wrapper tag removed", view)
	}
}

func TestSearchFollowsHeadlessToggle(t *testing.T) {
	database, err := db.New(filepath.Join(t.TempDir(), "headless-search.db"))
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

	searchIDs := func(showHeadless bool) []string {
		t.Helper()
		msg, ok := performSearch(database, "deploy", showHeadless, 1)().(searchResultsMsg)
		if !ok {
			t.Fatalf("performSearch(showHeadless=%v) did not return searchResultsMsg", showHeadless)
		}
		var ids []string
		for _, r := range msg.results {
			ids = append(ids, r.SessionID)
		}
		sort.Strings(ids)
		return ids
	}
	if got := searchIDs(false); !reflect.DeepEqual(got, []string{"interactive"}) {
		t.Errorf("search with headless hidden = %v, want [interactive]", got)
	}
	if got := searchIDs(true); !reflect.DeepEqual(got, []string{"headless", "interactive"}) {
		t.Errorf("search with headless shown = %v, want [headless interactive]", got)
	}

	model := NewWithContext(context.Background(), database)
	defer model.Close()
	model.width, model.height = 120, 40
	model.searchResults = []searchResult{}
	if view := model.viewSearch(); !strings.Contains(view, "No results found. Headless sessions are hidden: press esc, then 'a' to show them.") {
		t.Errorf("empty search view with headless hidden = %q, want the hidden note", view)
	}
	model.searchResults = []searchResult{{SessionID: "interactive", Summary: "Deploy notes"}}
	if view := model.viewSearch(); !strings.Contains(view, "Found 1 sessions (headless hidden):") {
		t.Errorf("search view with headless hidden = %q, want the hidden note on the count", view)
	}
	model.showHeadless = true
	if view := model.viewSearch(); !strings.Contains(view, "Found 1 sessions:") {
		t.Errorf("search view with headless shown = %q, want the plain count", view)
	}
}

func TestListFooterCountsHiddenHeadlessSessions(t *testing.T) {
	database, err := db.New(filepath.Join(t.TempDir(), "hidden-count.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	for _, s := range []struct{ id, entrypoint string }{
		{"interactive", "cli"},
		{"headless-1", "sdk-cli"},
		{"headless-2", "sdk-ts"},
	} {
		res, err := database.Exec(`
			INSERT INTO sessions (session_id, project_path, summary, provider, entrypoint, created_at, updated_at)
			VALUES (?, '/p', 'Test session', 'claude', ?, datetime('now'), datetime('now'))
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
			VALUES (?, ?, 'user', 'user', 'hello', datetime('now'), 1)
		`, s.id+"-msg", rowID); err != nil {
			t.Fatal(err)
		}
	}

	for _, tc := range []struct {
		showHeadless bool
		hidden       int
	}{{false, 2}, {true, 0}} {
		loaded, ok := loadSessionsNow(database, false, tc.showHeadless, "", 1, false).(sessionsLoadedMsg)
		if !ok {
			t.Fatalf("showHeadless=%v: load did not return sessionsLoadedMsg", tc.showHeadless)
		}
		if loaded.hiddenHeadless != tc.hidden {
			t.Errorf("showHeadless=%v: hiddenHeadless = %d, want %d", tc.showHeadless, loaded.hiddenHeadless, tc.hidden)
		}
	}

	model := NewWithContext(context.Background(), database)
	defer model.Close()
	model.initialLoad = false
	model.syncing = false
	for _, tc := range []struct {
		hidden int
		footer string
	}{
		{425, "a show headless (425 hidden) •"},
		{0, "a show headless •"},
	} {
		model.hiddenHeadless = tc.hidden
		if view := model.viewList(); !strings.Contains(view, tc.footer) {
			t.Errorf("hiddenHeadless=%d: view = %q, want footer containing %q", tc.hidden, view, tc.footer)
		}
	}
}
