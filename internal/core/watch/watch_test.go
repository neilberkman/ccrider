package watch

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/neilberkman/ccrider/internal/core/db"
	"github.com/neilberkman/ccrider/pkg/sessionevents"
)

func newWatcher(t *testing.T) *Watcher {
	t.Helper()
	database, err := db.New(filepath.Join(t.TempDir(), "watch.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return &Watcher{DB: database, PollInterval: 10 * time.Millisecond}
}

func send(t *testing.T, w *Watcher, channel, body string) db.WatchSignal {
	t.Helper()
	s, _, err := w.SendSignal(context.Background(), db.SendSignalOptions{Channel: channel, Body: body})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func wait(t *testing.T, w *Watcher, cur string, limits Limits) Result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	res, err := w.Wait(ctx, cur, Spec{}, limits)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func codeOf(err error) string {
	var we *Error
	if errors.As(err, &we) {
		return we.Code
	}
	return ""
}

func TestArmSignalOnlySeesLaterSignals(t *testing.T) {
	w := newWatcher(t)
	send(t, w, "ch", "before")
	armed, err := w.Arm(context.Background(), Spec{Until: []string{CondSignal}, Channel: "ch"})
	if err != nil {
		t.Fatal(err)
	}
	if armed.Status != StatusArmed || armed.Channel != "ch" {
		t.Fatalf("arm = %+v", armed)
	}
	if res := wait(t, w, armed.Cursor, Limits{}); res.Status != StatusWaiting || res.Cursor != armed.Cursor {
		t.Fatalf("before any new signal: %+v", res)
	}
	after := send(t, w, "ch", "after")
	send(t, w, "other", "ignored")
	res := wait(t, w, armed.Cursor, Limits{})
	if res.Status != StatusEvent || len(res.Events) != 1 || res.Events[0].Signal.ID != after.ID {
		t.Fatalf("after signal: %+v", res)
	}
	ev := res.Events[0]
	if ev.EventID != "signal:"+strconv.FormatInt(after.ID, 10) || ev.Kind != CondSignal || ev.Signal.Body != "after" {
		t.Fatalf("event = %+v", ev)
	}
	// Continuing from the returned cursor waits again.
	if next := wait(t, w, res.Cursor, Limits{}); next.Status != StatusWaiting {
		t.Fatalf("after consuming: %+v", next)
	}
}

func TestWaitWakesWhenSignalArrives(t *testing.T) {
	w := newWatcher(t)
	armed, err := w.Arm(context.Background(), Spec{Until: []string{CondSignal}, MintChannel: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(armed.Channel, "watch-") {
		t.Fatalf("minted channel = %q", armed.Channel)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(50 * time.Millisecond)
		send(t, w, armed.Channel, "ready")
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	res, err := w.Wait(ctx, armed.Cursor, Spec{}, Limits{})
	wg.Wait()
	if err != nil || res.Status != StatusEvent || res.Events[0].Signal.Body != "ready" {
		t.Fatalf("wait = %+v, %v", res, err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("wait took %v; it should return soon after the signal", time.Since(start))
	}
}

func TestBatchLimitsAndReplay(t *testing.T) {
	w := newWatcher(t)
	armed, _ := w.Arm(context.Background(), Spec{Until: []string{CondSignal}, Channel: "ch"})
	var ids []int64
	for i := 0; i < 5; i++ {
		ids = append(ids, send(t, w, "ch", strings.Repeat("x", 100)).ID)
	}

	first := wait(t, w, armed.Cursor, Limits{MaxEvents: 2})
	if len(first.Events) != 2 || first.Events[0].Signal.ID != ids[0] || first.Events[1].Signal.ID != ids[1] {
		t.Fatalf("first batch = %+v", first.Events)
	}
	// Replaying the same cursor returns the same events with the same ids.
	replay := wait(t, w, armed.Cursor, Limits{MaxEvents: 2})
	if replay.Events[0].EventID != first.Events[0].EventID || replay.Cursor != first.Cursor {
		t.Fatalf("replay differs: %+v vs %+v", replay, first)
	}
	// A byte budget smaller than one event still returns one, and never skips.
	var got []int64
	cur := first.Cursor
	for len(got) < 3 {
		res := wait(t, w, cur, Limits{MaxBytes: 1})
		if len(res.Events) != 1 {
			t.Fatalf("tiny byte budget returned %d events", len(res.Events))
		}
		got = append(got, res.Events[0].Signal.ID)
		cur = res.Cursor
	}
	for i, id := range got {
		if id != ids[i+2] {
			t.Fatalf("byte-limited sequence = %v, want %v", got, ids[2:])
		}
	}
}

func TestCursorExpiredAfterPruning(t *testing.T) {
	w := newWatcher(t)
	ctx := context.Background()
	armed, _ := w.Arm(ctx, Spec{Until: []string{CondSignal}, Channel: "ch"})
	old := time.Now().Add(-db.SignalRetention - time.Hour)
	if _, _, err := w.SendSignal(ctx, db.SendSignalOptions{Channel: "ch", Body: "unread", Now: old}); err != nil {
		t.Fatal(err)
	}
	// This send prunes the unread signal.
	send(t, w, "ch", "new")
	res := wait(t, w, armed.Cursor, Limits{})
	if res.Status != StatusCursorExpired || len(res.Events) != 0 {
		t.Fatalf("result = %+v, want cursor_expired", res)
	}
	// The fresh cursor starts after everything, without expiring again.
	if next := wait(t, w, res.Cursor, Limits{}); next.Status != StatusWaiting {
		t.Fatalf("after expiry: %+v", next)
	}
}

func TestCursorValidation(t *testing.T) {
	w := newWatcher(t)
	ctx := context.Background()
	armed, _ := w.Arm(ctx, Spec{Until: []string{CondSignal}, Channel: "ch"})

	if _, err := w.Wait(ctx, "not a cursor!", Spec{}, Limits{}); codeOf(err) != CodeCursorInvalid {
		t.Fatalf("garbage cursor err = %v", err)
	}
	stale := cursor{V: cursorVersion, DV: decoderVersion + 1, Until: []string{CondSignal}, Channel: "ch"}
	if _, err := w.Wait(ctx, stale.encode(), Spec{}, Limits{}); codeOf(err) != CodeCursorInvalid {
		t.Fatalf("other decoder version err = %v", err)
	}
	if _, err := w.Wait(ctx, armed.Cursor, Spec{Channel: "other"}, Limits{}); codeOf(err) != CodeCursorMismatch {
		t.Fatalf("mismatched channel err = %v", err)
	}
	if _, err := w.Wait(ctx, armed.Cursor, Spec{Until: []string{CondTurnEnd}}, Limits{}); codeOf(err) != CodeCursorMismatch {
		t.Fatalf("mismatched conditions err = %v", err)
	}
	short, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	defer cancel()
	if res, err := w.Wait(short, armed.Cursor, Spec{Channel: "ch", Until: []string{CondSignal, CondSignal}}, Limits{}); err != nil || res.Status != StatusWaiting {
		t.Fatalf("matching conditions: %+v, %v", res, err)
	}
}

func TestArmValidation(t *testing.T) {
	w := newWatcher(t)
	ctx := context.Background()
	cases := []struct {
		spec Spec
		code string
	}{
		{Spec{}, CodeInvalidArgument},
		{Spec{Until: []string{"bogus"}}, CodeInvalidArgument},
		{Spec{Until: []string{CondSignal, CondMessage}, Channel: "c"}, CodeInvalidArgument},
		{Spec{Until: []string{CondSignal}}, CodeInvalidArgument},
		{Spec{Until: []string{CondSignal}, Channel: "c", MintChannel: true}, CodeInvalidArgument},
		{Spec{Until: []string{CondSignal}, Channel: "c", SessionID: "s"}, CodeInvalidArgument},
		{Spec{Until: []string{CondSignal}, Channel: "bad\x00"}, CodeInvalidArgument},
		{Spec{Until: []string{CondTurnEnd}, SessionID: "s"}, CodeNotFound},
	}
	for i, tc := range cases {
		if _, err := w.Arm(ctx, tc.spec); codeOf(err) != tc.code {
			t.Errorf("case %d: err = %v, want %s", i, err, tc.code)
		}
	}
}

func TestWaitReturnsCancellation(t *testing.T) {
	w := newWatcher(t)
	armed, _ := w.Arm(context.Background(), Spec{Until: []string{CondSignal}, Channel: "ch"})
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(30 * time.Millisecond); cancel() }()
	if _, err := w.Wait(ctx, armed.Cursor, Spec{}, Limits{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled wait err = %v, want context.Canceled", err)
	}
}

func TestCursorInvariants(t *testing.T) {
	w := newWatcher(t)
	ctx := context.Background()
	bad := []cursor{
		{V: cursorVersion, DV: decoderVersion, Until: []string{CondSignal}},
		{V: cursorVersion, DV: decoderVersion, Until: []string{CondSignal}, Channel: "c", Signal: -1},
		{V: cursorVersion, DV: decoderVersion, Until: []string{CondSignal}, Channel: "c", SessionID: "s"},
		{V: cursorVersion, DV: decoderVersion, Until: []string{CondSignal}, Channel: "c", Offset: 10},
		{V: cursorVersion, DV: decoderVersion, Until: []string{CondSignal}, Channel: "bad\nchannel"},
		{V: cursorVersion, DV: decoderVersion, Until: []string{CondTurnEnd}, Channel: "c"},
	}
	for i, c := range bad {
		if _, err := w.Wait(ctx, c.encode(), Spec{}, Limits{}); codeOf(err) != CodeCursorInvalid {
			t.Errorf("case %d: err = %v, want cursor_invalid", i, err)
		}
	}
}

func TestSignalCursorRejectsTranscriptFields(t *testing.T) {
	w := newWatcher(t)
	st := sessionevents.State{Turn: sessionevents.TurnIdle}
	for i, c := range []cursor{
		{V: cursorVersion, DV: decoderVersion, Until: []string{CondSignal}, Channel: "c", TailLen: 5},
		{V: cursorVersion, DV: decoderVersion, Until: []string{CondSignal}, Channel: "c", Gen: 1},
		{V: cursorVersion, DV: decoderVersion, Until: []string{CondSignal}, Channel: "c", State: &st},
	} {
		if _, err := w.Wait(context.Background(), c.encode(), Spec{}, Limits{}); codeOf(err) != CodeCursorInvalid {
			t.Errorf("case %d: err = %v", i, err)
		}
	}
}
