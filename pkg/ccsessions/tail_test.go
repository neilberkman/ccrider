package ccsessions

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func collect(t *testing.T, data string, start, frontier int64) ([]CompleteLine, int64) {
	t.Helper()
	var got []CompleteLine
	next, err := ForEachCompleteLine(context.Background(), strings.NewReader(data[start:]), start, frontier, func(l CompleteLine) error {
		l.Bytes = append([]byte(nil), l.Bytes...)
		got = append(got, l)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return got, next
}

func TestForEachCompleteLineLeavesUnterminatedTail(t *testing.T) {
	data := "a\r\n\nbb\ncc"
	got, next := collect(t, data, 0, int64(len(data)))
	if len(got) != 2 || string(got[0].Bytes) != "a" || string(got[1].Bytes) != "bb" {
		t.Fatalf("lines = %+v", got)
	}
	if got[0].Start != 0 || got[0].End != 3 || got[1].Start != 4 || got[1].End != 7 {
		t.Fatalf("offsets = %+v", got)
	}
	if next != 7 {
		t.Fatalf("next = %d, want 7 (before the unterminated tail)", next)
	}
	// Once the tail is terminated, resuming from next delivers it.
	data += "\n"
	got, next = collect(t, data, 7, int64(len(data)))
	if len(got) != 1 || string(got[0].Bytes) != "cc" || next != int64(len(data)) {
		t.Fatalf("resumed = %+v next=%d", got, next)
	}
}

func TestForEachCompleteLineStopsAtFrontier(t *testing.T) {
	data := "one\ntwo\nthree\n"
	// A frontier inside "two" means "two" isn't complete yet.
	got, next := collect(t, data, 0, 6)
	if len(got) != 1 || next != 4 {
		t.Fatalf("lines=%+v next=%d", got, next)
	}
	if got, next := collect(t, data, 4, 4); len(got) != 0 || next != 4 {
		t.Fatalf("empty range: %+v %d", got, next)
	}
}

func TestForEachCompleteLineLongLine(t *testing.T) {
	long := strings.Repeat("x", 3*tailCheckBytes+17)
	data := long + "\nend\n"
	got, next := collect(t, data, 0, int64(len(data)))
	if len(got) != 2 || len(got[0].Bytes) != len(long) || next != int64(len(data)) {
		t.Fatalf("long line: %d lines, first len %d, next %d", len(got), len(got[0].Bytes), next)
	}
}

func TestForEachCompleteLineCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	data := "a\nb\nc\n"
	n := 0
	next, err := ForEachCompleteLine(ctx, strings.NewReader(data), 0, int64(len(data)), func(CompleteLine) error {
		n++
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) || n != 1 || next != 2 {
		t.Fatalf("n=%d next=%d err=%v", n, next, err)
	}
}
