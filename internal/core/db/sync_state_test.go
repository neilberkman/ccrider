package db

import (
	"path/filepath"
	"testing"
	"time"
)

func TestSourceFingerprintRoundTrip(t *testing.T) {
	database, err := New(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()

	if fp, err := database.SourceFingerprint("antigravity", "/a"); err != nil || fp != "" {
		t.Fatalf("SourceFingerprint() before any record = %q, %v; want empty", fp, err)
	}
	for _, want := range []string{"v1", "v2"} {
		if err := database.SetSourceFingerprint("antigravity", "/a", want); err != nil {
			t.Fatal(err)
		}
		if fp, err := database.SourceFingerprint("antigravity", "/a"); err != nil || fp != want {
			t.Fatalf("SourceFingerprint() = %q, %v; want %q", fp, err, want)
		}
	}
	// Each data directory of a provider keeps its own fingerprint.
	if fp, _ := database.SourceFingerprint("antigravity", "/b"); fp != "" {
		t.Fatalf("SourceFingerprint(other path) = %q, want empty", fp)
	}
}

func TestSyncLease(t *testing.T) {
	database, err := New(filepath.Join(t.TempDir(), "lease.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	now := time.UnixMilli(1_800_000_000_000)
	ttl := time.Minute
	acquire := func(holder string, at, staleBefore time.Time) bool {
		t.Helper()
		ok, err := database.AcquireSyncLease(holder, at, ttl, staleBefore)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}

	if started, err := database.LastSyncPassStarted(); err != nil || !started.IsZero() {
		t.Fatalf("LastSyncPassStarted() on a new database = %v, %v; want zero", started, err)
	}
	if !acquire("a", now, now) {
		t.Fatal("a could not take a free lease")
	}
	if acquire("b", now.Add(time.Second), now.Add(time.Second)) {
		t.Fatal("b took a's unexpired lease")
	}
	if ok, err := database.RenewSyncLease("a", now.Add(50*time.Second), ttl); err != nil || !ok {
		t.Fatalf("a's renewal = %v, %v; want true", ok, err)
	}
	// Past the original expiry, but within the renewed one.
	if acquire("b", now.Add(70*time.Second), now.Add(70*time.Second)) {
		t.Fatal("b took a's renewed lease")
	}
	if ok, _ := database.RenewSyncLease("b", now, ttl); ok {
		t.Fatal("b renewed a lease it does not hold")
	}

	// A release by someone else changes nothing.
	if err := database.ReleaseSyncLease("b", true, now, now); err != nil {
		t.Fatal(err)
	}
	if acquire("b", now.Add(80*time.Second), now.Add(80*time.Second)) {
		t.Fatal("b's release freed a's lease")
	}

	started := now.Add(time.Second)
	if err := database.ReleaseSyncLease("a", true, started, now.Add(30*time.Second)); err != nil {
		t.Fatal(err)
	}
	if got, _ := database.LastSyncPassStarted(); !got.Equal(started) {
		t.Fatalf("LastSyncPassStarted() = %v, want %v", got, started)
	}
	// Free, but a pass started after staleBefore makes another unnecessary.
	if acquire("b", now.Add(40*time.Second), started) {
		t.Fatal("b took the lease although a pass started at staleBefore")
	}
	if !acquire("b", now.Add(40*time.Second), started.Add(time.Millisecond)) {
		t.Fatal("b could not take a free lease after a stale pass")
	}
	// An incomplete pass records nothing.
	if err := database.ReleaseSyncLease("b", false, now.Add(40*time.Second), now.Add(41*time.Second)); err != nil {
		t.Fatal(err)
	}
	if got, _ := database.LastSyncPassStarted(); !got.Equal(started) {
		t.Fatalf("LastSyncPassStarted() after an incomplete pass = %v, want %v", got, started)
	}
	// A crashed holder's lease expires.
	if !acquire("c", now.Add(50*time.Second), now.Add(time.Hour)) {
		t.Fatal("c could not take a free lease")
	}
	if !acquire("d", now.Add(50*time.Second+ttl+time.Millisecond), now.Add(time.Hour)) {
		t.Fatal("d could not take c's expired lease")
	}

	// A CLI or TUI sync records its pass without the lease, and never moves
	// the record backwards.
	later := now.Add(10 * time.Minute)
	if err := database.RecordSyncPass(later, later.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := database.RecordSyncPass(now, now); err != nil {
		t.Fatal(err)
	}
	if got, _ := database.LastSyncPassStarted(); !got.Equal(later) {
		t.Fatalf("LastSyncPassStarted() = %v, want %v", got, later)
	}
}
