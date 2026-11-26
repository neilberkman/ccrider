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
