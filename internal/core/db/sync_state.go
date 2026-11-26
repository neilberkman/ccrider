package db

import (
	"database/sql"
	"errors"
	"time"
)

// SourceFingerprint returns the fingerprint recorded for a source after its
// last complete import, or "" when none is recorded. A source whose current
// fingerprint matches has not changed since, so sync can skip reading it.
func (db *DB) SourceFingerprint(provider, path string) (string, error) {
	var fingerprint string
	err := db.conn.QueryRow(
		`SELECT fingerprint FROM source_fingerprints WHERE provider = ? AND path = ?`,
		provider, path,
	).Scan(&fingerprint)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return fingerprint, err
}

// SetSourceFingerprint records the fingerprint of the inputs a source was
// just imported from completely.
func (db *DB) SetSourceFingerprint(provider, path, fingerprint string) error {
	_, err := db.conn.Exec(`
		INSERT INTO source_fingerprints (provider, path, fingerprint, recorded_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(provider, path) DO UPDATE SET
			fingerprint = excluded.fingerprint,
			recorded_at = excluded.recorded_at
	`, provider, path, fingerprint, time.Now().Unix())
	return err
}

// Background sync coordinates through one lease row so that, however many
// processes share the database, one syncs at a time and none repeats a pass
// another started moments ago. Times are Unix milliseconds.

// AcquireSyncLease takes the sync lease for holder until now+ttl. It succeeds
// only when no other holder has an unexpired lease and the last complete pass
// started before staleBefore; otherwise it returns false.
func (db *DB) AcquireSyncLease(holder string, now time.Time, ttl time.Duration, staleBefore time.Time) (bool, error) {
	// Check with a read first. Readers don't wait in WAL mode, while the
	// UPDATE needs the write lock that a syncing process keeps taking.
	var current string
	var expiresAt, lastStarted int64
	if err := db.conn.QueryRow(
		`SELECT holder, expires_at, last_pass_started_at FROM sync_lease WHERE id = 1`,
	).Scan(&current, &expiresAt, &lastStarted); err != nil {
		return false, err
	}
	heldByOther := current != "" && current != holder && expiresAt >= now.UnixMilli()
	if heldByOther || lastStarted >= staleBefore.UnixMilli() {
		return false, nil
	}

	// The UPDATE repeats the conditions, so two processes that both passed
	// the read cannot both take the lease.
	res, err := db.conn.Exec(`
		UPDATE sync_lease SET holder = ?, expires_at = ?
		WHERE id = 1
		  AND (holder = '' OR holder = ? OR expires_at < ?)
		  AND last_pass_started_at < ?
	`, holder, now.Add(ttl).UnixMilli(), holder, now.UnixMilli(), staleBefore.UnixMilli())
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// RenewSyncLease extends holder's lease to now+ttl. It returns false when
// holder no longer has the lease.
func (db *DB) RenewSyncLease(holder string, now time.Time, ttl time.Duration) (bool, error) {
	res, err := db.conn.Exec(
		`UPDATE sync_lease SET expires_at = ? WHERE id = 1 AND holder = ?`,
		now.Add(ttl).UnixMilli(), holder,
	)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// ReleaseSyncLease gives up holder's lease. When completed is true, the pass
// ran through every source and its start and finish times are recorded.
func (db *DB) ReleaseSyncLease(holder string, completed bool, started, finished time.Time) error {
	_, err := db.conn.Exec(`
		UPDATE sync_lease SET
			holder = '',
			expires_at = 0,
			last_pass_started_at = CASE WHEN ? THEN MAX(last_pass_started_at, ?) ELSE last_pass_started_at END,
			last_pass_finished_at = CASE WHEN ? THEN MAX(last_pass_finished_at, ?) ELSE last_pass_finished_at END
		WHERE id = 1 AND holder = ?
	`, completed, started.UnixMilli(), completed, finished.UnixMilli(), holder)
	return err
}

// RecordSyncPass records a complete sync of every source that ran without
// the lease, such as one started from the CLI or the TUI, so background sync
// does not repeat it.
func (db *DB) RecordSyncPass(started, finished time.Time) error {
	_, err := db.conn.Exec(`
		UPDATE sync_lease SET
			last_pass_started_at = MAX(last_pass_started_at, ?),
			last_pass_finished_at = MAX(last_pass_finished_at, ?)
		WHERE id = 1
	`, started.UnixMilli(), finished.UnixMilli())
	return err
}

// LastSyncPassStarted returns when the most recent complete sync pass, by any
// process, started; the zero time when none has completed.
func (db *DB) LastSyncPassStarted() (time.Time, error) {
	var ms int64
	if err := db.conn.QueryRow(`SELECT last_pass_started_at FROM sync_lease WHERE id = 1`).Scan(&ms); err != nil {
		return time.Time{}, err
	}
	if ms == 0 {
		return time.Time{}, nil
	}
	return time.UnixMilli(ms), nil
}
