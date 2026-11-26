package importer

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	mathrand "math/rand/v2"
	"os"
	"sync"
	"time"

	"github.com/neilberkman/ccrider/internal/core/db"
)

// SyncerOptions tunes a Syncer. Zero values take the defaults.
type SyncerOptions struct {
	// Interval is the time between background passes. A pass is skipped
	// when one by any process started within the interval. Default 1 minute.
	Interval time.Duration
	// LeaseTTL is how long a holder that stopped renewing (a crashed
	// process) blocks other processes. It is renewed every third of itself
	// while a pass runs. Default 1 minute.
	LeaseTTL time.Duration
	// PassTimeout caps one pass. Default 10 minutes.
	PassTimeout time.Duration
	// Poll is how often a waiter rechecks while another process syncs.
	// Default 250 ms.
	Poll time.Duration
	// Logf reports failed passes and lease errors. Default discards them.
	Logf func(format string, args ...any)
}

// Syncer keeps the database in step with the session sources for a
// long-running process such as the MCP server. Passes run in the background,
// at most one at a time in a process, and processes sharing the database
// coordinate through a lease in it: one of them syncs at a time, and none
// repeats a pass another started moments ago. Callers that are about to
// read wait for a recent enough pass with Refresh instead of syncing
// themselves.
type Syncer struct {
	db      *db.DB
	sources func() []Source
	opts    SyncerOptions
	holder  string

	mu      sync.Mutex
	ctx     context.Context // lifetime of passes; Background until Start
	running chan struct{}   // closed when the current pass ends; nil when idle
	wg      sync.WaitGroup  // the background loop and every pass
}

// NewSyncer returns a Syncer that imports the sources returned by sources,
// called once per pass.
func NewSyncer(database *db.DB, sources func() []Source, opts SyncerOptions) *Syncer {
	if opts.Interval <= 0 {
		opts.Interval = time.Minute
	}
	if opts.LeaseTTL <= 0 {
		opts.LeaseTTL = time.Minute
	}
	if opts.PassTimeout <= 0 {
		opts.PassTimeout = 10 * time.Minute
	}
	if opts.Poll <= 0 {
		opts.Poll = 250 * time.Millisecond
	}
	if opts.Logf == nil {
		opts.Logf = func(string, ...any) {}
	}
	return &Syncer{
		db:      database,
		sources: sources,
		opts:    opts,
		holder:  newLeaseHolder(),
		ctx:     context.Background(),
	}
}

func newLeaseHolder() string {
	host, _ := os.Hostname()
	var suffix [6]byte
	_, _ = rand.Read(suffix[:])
	return fmt.Sprintf("%s/%d/%s", host, os.Getpid(), hex.EncodeToString(suffix[:]))
}

// Start runs a pass now, unless one by any process started within the
// interval, and then one every interval (plus up to a tenth of it, so
// processes started together drift apart) until ctx ends. The returned stop
// function ends the loop, cancels a pass under way and waits for it, so the
// database can be closed afterwards.
func (s *Syncer) Start(ctx context.Context) (stop func()) {
	ctx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	s.ctx = ctx
	s.wg.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.wg.Done()
		for {
			select {
			case <-s.kick(time.Now().Add(-s.opts.Interval)):
			case <-ctx.Done():
				return
			}
			delay := s.opts.Interval + time.Duration(mathrand.Int64N(int64(s.opts.Interval/10)+1))
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return
			}
		}
	}()
	return func() {
		// Cancel under the lock, so no pass is added once Wait starts.
		s.mu.Lock()
		cancel()
		s.mu.Unlock()
		s.wg.Wait()
	}
}

// Refresh waits until a pass that started at or after since has completed,
// in this process or another, starting one when none is under way here. It
// gives up after wait or when ctx ends, and reports whether the database is
// that fresh. A pass outlives an abandoned wait, so the next caller finds it
// finished or joins it.
func (s *Syncer) Refresh(ctx context.Context, since time.Time, wait time.Duration) bool {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	for {
		if s.freshSince(since) {
			return true
		}
		select {
		case <-s.kick(since):
			if s.stopped() {
				return s.freshSince(since)
			}
		case <-ctx.Done():
			return false
		case <-timer.C:
			return s.freshSince(since)
		}
	}
}

// stopped reports whether Start's stop function has run, after which no
// pass starts.
func (s *Syncer) stopped() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ctx.Err() != nil
}

// freshSince reports whether the last complete pass started at or after
// since, compared in the milliseconds the database records.
func (s *Syncer) freshSince(since time.Time) bool {
	started, err := s.db.LastSyncPassStarted()
	return err == nil && !started.IsZero() && started.UnixMilli() >= since.UnixMilli()
}

// kick starts a pass unless one is running in this process, and returns a
// channel closed when that pass ends. The pass syncs only if no complete
// pass started at or after staleBefore.
func (s *Syncer) kick(staleBefore time.Time) <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running != nil {
		return s.running
	}
	done := make(chan struct{})
	if s.ctx.Err() != nil {
		close(done)
		return done
	}
	s.running = done
	s.wg.Add(1)
	go func(ctx context.Context) {
		defer s.wg.Done()
		s.pass(ctx, staleBefore)
		s.mu.Lock()
		s.running = nil
		s.mu.Unlock()
		close(done)
	}(s.ctx)
	return done
}

func (s *Syncer) pass(ctx context.Context, staleBefore time.Time) {
	started := time.Now()
	acquired, err := s.db.AcquireSyncLease(s.holder, started, s.opts.LeaseTTL, staleBefore)
	if err != nil {
		s.opts.Logf("sync lease: %v", err)
	}
	if !acquired {
		// Another process is syncing, or a recent pass makes this one
		// unnecessary. Waiting a poll interval paces callers that loop.
		select {
		case <-time.After(s.opts.Poll):
		case <-ctx.Done():
		}
		return
	}

	passCtx, cancel := context.WithTimeout(ctx, s.opts.PassTimeout)
	defer cancel()
	renewing := make(chan struct{})
	renewed := make(chan struct{})
	go func() {
		defer close(renewed)
		s.keepLease(passCtx, cancel, renewing)
	}()

	syncErr := New(s.db).SyncAll(passCtx, s.sources(), false)
	completed := passCtx.Err() == nil
	close(renewing)
	<-renewed
	if syncErr != nil {
		s.opts.Logf("background session sync: %v", syncErr)
	}
	if err := s.db.ReleaseSyncLease(s.holder, completed, started, time.Now()); err != nil {
		s.opts.Logf("release sync lease: %v", err)
	}
}

// keepLease renews the lease until stop closes. If another holder has taken
// it (this process stalled past the TTL), the pass is cancelled so that two
// processes do not sync at once.
func (s *Syncer) keepLease(ctx context.Context, cancel context.CancelFunc, stop <-chan struct{}) {
	ticker := time.NewTicker(s.opts.LeaseTTL / 3)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			held, err := s.db.RenewSyncLease(s.holder, time.Now(), s.opts.LeaseTTL)
			if err != nil {
				s.opts.Logf("renew sync lease: %v", err)
				continue
			}
			if !held {
				s.opts.Logf("sync lease taken by another process; stopping this pass")
				cancel()
				return
			}
		}
	}
}
