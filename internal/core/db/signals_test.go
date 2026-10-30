package db

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newSignalDB(t *testing.T) *DB {
	t.Helper()
	database, err := New(filepath.Join(t.TempDir(), "signals.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

func TestSendSignalIdempotency(t *testing.T) {
	database := newSignalDB(t)
	ctx := context.Background()

	first, dup, err := database.SendSignal(ctx, SendSignalOptions{Channel: "c", Body: "done", IdempotencyKey: "k"})
	if err != nil || dup {
		t.Fatalf("first send: dup=%v err=%v", dup, err)
	}
	again, dup, err := database.SendSignal(ctx, SendSignalOptions{Channel: "c", Body: "done", IdempotencyKey: "k"})
	if err != nil || !dup || again.ID != first.ID {
		t.Fatalf("repeat send: id=%d dup=%v err=%v, want id %d duplicate", again.ID, dup, err, first.ID)
	}
	if _, _, err := database.SendSignal(ctx, SendSignalOptions{Channel: "c", Body: "different", IdempotencyKey: "k"}); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("conflicting send err = %v, want ErrIdempotencyConflict", err)
	}
	// The same key on another channel is independent.
	if _, dup, err := database.SendSignal(ctx, SendSignalOptions{Channel: "other", Body: "x", IdempotencyKey: "k"}); err != nil || dup {
		t.Fatalf("other channel: dup=%v err=%v", dup, err)
	}
	// No key means no deduplication.
	a, _, _ := database.SendSignal(ctx, SendSignalOptions{Channel: "c", Body: "same"})
	b, _, _ := database.SendSignal(ctx, SendSignalOptions{Channel: "c", Body: "same"})
	if a.ID == b.ID {
		t.Fatal("keyless sends were deduplicated")
	}
}

func TestSendSignalValidation(t *testing.T) {
	database := newSignalDB(t)
	ctx := context.Background()
	cases := []SendSignalOptions{
		{Channel: "", Body: "x"},
		{Channel: "bad\nchannel", Body: "x"},
		{Channel: strings.Repeat("c", MaxSignalChannelRunes+1), Body: "x"},
		{Channel: "c", Body: strings.Repeat("b", MaxSignalBodyBytes+1)},
		{Channel: "c", Body: "\xff"},
	}
	for i, opts := range cases {
		if _, _, err := database.SendSignal(ctx, opts); !errors.Is(err, ErrInvalidSignal) {
			t.Errorf("case %d: err = %v, want ErrInvalidSignal", i, err)
		}
	}
	if _, _, err := database.SendSignal(ctx, SendSignalOptions{Channel: "c", Body: strings.Repeat("b", MaxSignalBodyBytes)}); err != nil {
		t.Errorf("body at the limit rejected: %v", err)
	}
}

func TestSignalPruningWatermark(t *testing.T) {
	database := newSignalDB(t)
	ctx := context.Background()
	old := time.Now().Add(-SignalRetention - time.Hour)

	a, _, _ := database.SendSignal(ctx, SendSignalOptions{Channel: "a", Body: "1", Now: old})
	a2, _, _ := database.SendSignal(ctx, SendSignalOptions{Channel: "a", Body: "2", Now: old})
	b, _, _ := database.SendSignal(ctx, SendSignalOptions{Channel: "b", Body: "1", Now: old})
	// A fresh send prunes everything above, per channel.
	fresh, _, err := database.SendSignal(ctx, SendSignalOptions{Channel: "a", Body: "3"})
	if err != nil {
		t.Fatal(err)
	}

	page, err := database.SignalsAfter(ctx, "a", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if page.PrunedThroughID != a2.ID {
		t.Fatalf("channel a watermark = %d, want %d", page.PrunedThroughID, a2.ID)
	}
	if len(page.Signals) != 1 || page.Signals[0].ID != fresh.ID {
		t.Fatalf("channel a signals = %+v, want only %d", page.Signals, fresh.ID)
	}
	pageB, _ := database.SignalsAfter(ctx, "b", 0, 10)
	if pageB.PrunedThroughID != b.ID || len(pageB.Signals) != 0 {
		t.Fatalf("channel b = %+v, want watermark %d and no signals", pageB, b.ID)
	}
	pageC, _ := database.SignalsAfter(ctx, "never-used", 0, 10)
	if pageC.PrunedThroughID != 0 {
		t.Fatalf("unused channel watermark = %d, want 0", pageC.PrunedThroughID)
	}
	_ = a

	// The high-water mark keeps counting after rows are pruned, so a watch
	// armed now is not mistaken for one that fell behind.
	high, err := database.SignalHighWater(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if high != fresh.ID {
		t.Fatalf("high water = %d, want %d", high, fresh.ID)
	}
}

func TestSignalHighWaterSurvivesEmptyTable(t *testing.T) {
	database := newSignalDB(t)
	ctx := context.Background()
	if high, err := database.SignalHighWater(ctx); err != nil || high != 0 {
		t.Fatalf("empty high water = %d, %v", high, err)
	}
	old := time.Now().Add(-SignalRetention - time.Hour)
	s, _, _ := database.SendSignal(ctx, SendSignalOptions{Channel: "a", Body: "1", Now: old})
	// Prune it by sending on another channel, leaving channel a empty.
	_, _, _ = database.SendSignal(ctx, SendSignalOptions{Channel: "b", Body: "1"})
	high, err := database.SignalHighWater(ctx)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := database.SignalsAfter(ctx, "a", high, 10)
	if page.PrunedThroughID > high {
		t.Fatalf("watch armed at %d looks expired against watermark %d (pruned id %d)", high, page.PrunedThroughID, s.ID)
	}
}

// A prune committed from another connection while a read is in progress must
// not make an unread signal disappear without the watermark showing it: the
// read sees either the signal or the raised watermark.
func TestSignalsAfterIsOneSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshot.db")
	reader, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()
	writer, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Close() }()
	ctx := context.Background()

	old := time.Now().Add(-SignalRetention - time.Hour)
	unread, _, err := writer.SendSignal(ctx, SendSignalOptions{Channel: "c", Body: "unread", Now: old})
	if err != nil {
		t.Fatal(err)
	}

	signalsAfterHook = func() {
		signalsAfterHook = nil
		// This send prunes the unread signal and raises the watermark.
		if _, _, err := writer.SendSignal(ctx, SendSignalOptions{Channel: "c", Body: "new"}); err != nil {
			t.Error(err)
		}
	}
	defer func() { signalsAfterHook = nil }()

	page, err := reader.SignalsAfter(ctx, "c", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	sawUnread := len(page.Signals) > 0 && page.Signals[0].ID == unread.ID
	expired := page.PrunedThroughID >= unread.ID
	if !sawUnread && !expired {
		t.Fatalf("unread signal vanished without the watermark showing it: %+v", page)
	}
	if sawUnread && expired {
		t.Fatalf("read mixed two snapshots: %+v", page)
	}
}
