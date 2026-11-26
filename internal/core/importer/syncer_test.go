package importer

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/neilberkman/ccrider/internal/core/db"
	"github.com/neilberkman/ccrider/pkg/ccsessions"
)

// countingSource is an enumerated source that counts its enumerations and
// takes delay to enumerate.
func countingSource(provider string, delay time.Duration, count *atomic.Int32) Source {
	return Source{
		Path:     "/data/" + provider,
		Provider: provider,
		EnumerateFn: func() ([]*ccsessions.ParsedSession, error) {
			count.Add(1)
			time.Sleep(delay)
			return []*ccsessions.ParsedSession{{
				SessionID: provider + "-session",
				ImportID:  provider + "-session",
				Summary:   "syncer test",
				FileMtime: time.Unix(1_700_000_000, 0),
				Messages: []ccsessions.ParsedMessage{{
					UUID: provider + "-msg", Type: "user", Sender: "human",
					TextContent: "hello", Timestamp: time.Unix(1_700_000_000, 0), Sequence: 1,
				}},
			}}, nil
		},
	}
}

func openSyncerDB(t *testing.T, path string) *db.DB {
	t.Helper()
	database, err := db.New(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

func testSyncer(database *db.DB, opts SyncerOptions, sources ...Source) *Syncer {
	if opts.Poll == 0 {
		opts.Poll = 10 * time.Millisecond
	}
	return NewSyncer(database, func() []Source { return sources }, opts)
}

func TestSyncerRunsOnePassForConcurrentCallers(t *testing.T) {
	database := openSyncerDB(t, filepath.Join(t.TempDir(), "s.db"))
	var count atomic.Int32
	syncer := testSyncer(database, SyncerOptions{}, countingSource("p", 200*time.Millisecond, &count))

	since := time.Now()
	var wg sync.WaitGroup
	var fresh atomic.Int32
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if syncer.Refresh(context.Background(), since, 5*time.Second) {
				fresh.Add(1)
			}
		}()
	}
	wg.Wait()
	if fresh.Load() != 5 || count.Load() != 1 {
		t.Fatalf("fresh callers = %d, enumerations = %d; want 5 callers served by 1 pass", fresh.Load(), count.Load())
	}

	// Data synced moments ago is fresh enough without another pass.
	if !syncer.Refresh(context.Background(), time.Now().Add(-10*time.Second), time.Second) || count.Load() != 1 {
		t.Fatalf("fresh data: enumerations = %d, want no new pass", count.Load())
	}
}

func TestSyncersSharingADatabaseRunOnePass(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared.db")
	var count atomic.Int32
	source := countingSource("p", 200*time.Millisecond, &count)
	// Separate connections stand in for separate processes.
	a := testSyncer(openSyncerDB(t, path), SyncerOptions{}, source)
	b := testSyncer(openSyncerDB(t, path), SyncerOptions{}, source)

	since := time.Now()
	results := make(chan bool, 2)
	for _, s := range []*Syncer{a, b} {
		go func(s *Syncer) { results <- s.Refresh(context.Background(), since, 5*time.Second) }(s)
	}
	first, second := <-results, <-results
	if !first || !second {
		t.Fatal("a syncer gave up waiting for the shared pass")
	}
	if count.Load() != 1 {
		t.Fatalf("enumerations = %d, want 1 pass across both syncers", count.Load())
	}
}

func TestSyncerTakesAnExpiredLease(t *testing.T) {
	database := openSyncerDB(t, filepath.Join(t.TempDir(), "s.db"))
	if _, err := database.Exec(`UPDATE sync_lease SET holder = 'crashed', expires_at = ? WHERE id = 1`, time.Now().Add(-time.Second).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	var count atomic.Int32
	syncer := testSyncer(database, SyncerOptions{}, countingSource("p", 0, &count))
	if !syncer.Refresh(context.Background(), time.Now(), 5*time.Second) || count.Load() != 1 {
		t.Fatalf("enumerations = %d, want a pass despite the crashed holder's expired lease", count.Load())
	}
}

func TestSyncerGivesUpWhileAnotherProcessHoldsTheLease(t *testing.T) {
	database := openSyncerDB(t, filepath.Join(t.TempDir(), "s.db"))
	if _, err := database.Exec(`UPDATE sync_lease SET holder = 'other', expires_at = ? WHERE id = 1`, time.Now().Add(time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	var count atomic.Int32
	syncer := testSyncer(database, SyncerOptions{}, countingSource("p", 0, &count))

	start := time.Now()
	if syncer.Refresh(context.Background(), start, 300*time.Millisecond) {
		t.Fatal("Refresh() = true while another process holds the lease and no pass completed")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Refresh() took %v, want it to give up after its 300ms wait", elapsed)
	}
	if count.Load() != 0 {
		t.Fatalf("enumerations = %d, want none while another process holds the lease", count.Load())
	}
}

func TestSyncerRenewsItsLeaseDuringALongPass(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared.db")
	var count atomic.Int32
	source := countingSource("p", 400*time.Millisecond, &count)
	opts := SyncerOptions{LeaseTTL: 90 * time.Millisecond}
	a := testSyncer(openSyncerDB(t, path), opts, source)
	b := testSyncer(openSyncerDB(t, path), opts, source)

	since := time.Now()
	done := make(chan bool)
	go func() { done <- a.Refresh(context.Background(), since, 5*time.Second) }()
	// Past the lease's TTL, but a is still renewing it.
	time.Sleep(200 * time.Millisecond)
	if !b.Refresh(context.Background(), since, 5*time.Second) || !<-done {
		t.Fatal("a syncer gave up waiting")
	}
	if count.Load() != 1 {
		t.Fatalf("enumerations = %d, want 1: the second syncer must wait for the renewed lease", count.Load())
	}
}

func TestSyncerStopsAPassWhoseLeaseWasTaken(t *testing.T) {
	database := openSyncerDB(t, filepath.Join(t.TempDir(), "s.db"))
	var count atomic.Int32
	syncer := testSyncer(database, SyncerOptions{LeaseTTL: 60 * time.Millisecond}, countingSource("p", 300*time.Millisecond, &count))

	done := make(chan bool)
	go func() { done <- syncer.Refresh(context.Background(), time.Now(), 2*time.Second) }()
	time.Sleep(50 * time.Millisecond)
	if _, err := database.Exec(`UPDATE sync_lease SET holder = 'intruder', expires_at = ? WHERE id = 1`, time.Now().Add(time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if <-done {
		t.Fatal("Refresh() = true for a pass that lost its lease")
	}
	var holder string
	if err := database.QueryRow(`SELECT holder FROM sync_lease WHERE id = 1`).Scan(&holder); err != nil {
		t.Fatal(err)
	}
	if started, _ := database.LastSyncPassStarted(); !started.IsZero() || holder != "intruder" {
		t.Fatalf("last pass started %v, lease holder %q; want no recorded pass and the intruder's lease left alone", started, holder)
	}
}

func TestSyncerPassContinuesPastAFailingSource(t *testing.T) {
	database := openSyncerDB(t, filepath.Join(t.TempDir(), "s.db"))
	var count atomic.Int32
	failing := Source{
		Path:        "/data/broken",
		Provider:    "broken",
		EnumerateFn: func() ([]*ccsessions.ParsedSession, error) { return nil, errors.New("unreadable") },
	}
	var logged atomic.Int32
	syncer := testSyncer(database, SyncerOptions{Logf: func(string, ...any) { logged.Add(1) }}, failing, countingSource("p", 0, &count))

	if !syncer.Refresh(context.Background(), time.Now(), 5*time.Second) {
		t.Fatal("Refresh() = false: a failing source must not stop the pass from completing")
	}
	if count.Load() != 1 || logged.Load() == 0 {
		t.Fatalf("enumerations after the failing source = %d, logged = %d; want 1 and a logged failure", count.Load(), logged.Load())
	}
}

func TestSyncerStartRunsAPassAndStopWaits(t *testing.T) {
	database := openSyncerDB(t, filepath.Join(t.TempDir(), "s.db"))
	var count atomic.Int32
	syncer := testSyncer(database, SyncerOptions{Interval: time.Hour}, countingSource("p", 50*time.Millisecond, &count))

	stop := syncer.Start(context.Background())
	deadline := time.Now().Add(5 * time.Second)
	for {
		if started, _ := database.LastSyncPassStarted(); !started.IsZero() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Start() did not run a pass")
		}
		time.Sleep(10 * time.Millisecond)
	}
	stop()

	// After stop no pass starts, and a waiter returns instead of spinning.
	start := time.Now()
	if syncer.Refresh(context.Background(), time.Now(), 2*time.Second) {
		t.Fatal("Refresh() after stop = true, want false: no new pass can run")
	}
	if elapsed := time.Since(start); elapsed > time.Second || count.Load() != 1 {
		t.Fatalf("after stop: Refresh took %v, enumerations = %d; want an immediate return and no new pass", elapsed, count.Load())
	}
}
