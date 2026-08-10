package tui

import (
	"context"
	"path/filepath"
	"testing"
	"time"

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

func TestChannelProgressReporterDoesNotBlockWhenFull(t *testing.T) {
	progressCh := make(chan syncProgressMsg, 1)
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

func TestSyncFinishedReloadsBeforeReturning(t *testing.T) {
	database, err := db.New(filepath.Join(t.TempDir(), "sync-finished.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()

	model := NewWithContext(context.Background(), database)
	defer model.Close()
	updated, cmd := model.Update(syncFinishedMsg{})
	if cmd != nil {
		t.Fatal("syncFinishedMsg returned asynchronous command, want owned synchronous reload")
	}
	if updated.(Model).syncing {
		t.Fatal("syncing remains true after synchronous reload")
	}
}
