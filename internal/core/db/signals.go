package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
	"unicode"
	"unicode/utf8"
)

// SignalRetention is how long signals are kept. Pruning runs on insert.
const SignalRetention = 7 * 24 * time.Hour

// MaxSignalBodyBytes caps a signal body's JSON-encoded size. Limiting the
// encoded size rather than the raw size keeps every signal event inside a
// fixed response budget, whatever characters it contains.
const MaxSignalBodyBytes = 16 << 10

// MaxSignalSenderBytes caps the claimed sender session id.
const MaxSignalSenderBytes = 200

// MaxSignalChannelRunes caps a channel name.
const MaxSignalChannelRunes = 200

var (
	// ErrIdempotencyConflict means the idempotency key was already used on the
	// channel with a different body.
	ErrIdempotencyConflict = errors.New("idempotency key already used with a different body")
	// ErrInvalidSignal means the channel, body or another field failed
	// validation.
	ErrInvalidSignal = errors.New("invalid signal")
	// ErrInvalidSignalBody means the body failed validation. It wraps
	// ErrInvalidSignal.
	ErrInvalidSignalBody = fmt.Errorf("%w: body", ErrInvalidSignal)
)

// JSONStringLen returns the length of s encoded as a JSON string by
// encoding/json with HTML escaping off, excluding the surrounding quotes.
func JSONStringLen(s string) int {
	n := 0
	for _, r := range s {
		switch {
		case r == '"' || r == '\\' || r == '\n' || r == '\r' || r == '\t' || r == '\b' || r == '\f':
			n += 2
		case r < 0x20 || r == '\u2028' || r == '\u2029':
			n += 6
		default:
			n += utf8.RuneLen(r)
		}
	}
	return n
}

func validateSignalText(field, value string, maxBytes int) error {
	if !utf8.ValidString(value) || len(value) > maxBytes {
		return fmt.Errorf("%w: %s must be valid UTF-8 and at most %d bytes", ErrInvalidSignal, field, maxBytes)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("%w: %s must not contain control characters", ErrInvalidSignal, field)
		}
	}
	return nil
}

// WatchSignal is one message posted to a signal channel. FromSessionID is
// whatever the sender claimed; it is not verified.
type WatchSignal struct {
	ID             int64
	Channel        string
	IdempotencyKey string
	FromSessionID  string
	Body           string
	CreatedAt      time.Time
}

type SendSignalOptions struct {
	Channel        string
	Body           string
	IdempotencyKey string
	FromSessionID  string
	// Now overrides the clock for tests; zero means time.Now.
	Now time.Time
}

// ValidateSignalChannel checks a channel name: nonempty, valid UTF-8, no
// control characters, at most MaxSignalChannelRunes.
func ValidateSignalChannel(channel string) error {
	if channel == "" {
		return fmt.Errorf("%w: channel is required", ErrInvalidSignal)
	}
	if !utf8.ValidString(channel) || utf8.RuneCountInString(channel) > MaxSignalChannelRunes {
		return fmt.Errorf("%w: channel must be valid UTF-8 and at most %d characters", ErrInvalidSignal, MaxSignalChannelRunes)
	}
	for _, r := range channel {
		if unicode.IsControl(r) {
			return fmt.Errorf("%w: channel must not contain control characters", ErrInvalidSignal)
		}
	}
	return nil
}

// SendSignal posts a signal. Expired signals are pruned in the same
// transaction, raising each affected channel's pruning watermark. A reused
// idempotency key with the same body returns the existing signal and
// duplicate=true; with a different body it returns ErrIdempotencyConflict.
func (db *DB) SendSignal(ctx context.Context, opts SendSignalOptions) (WatchSignal, bool, error) {
	if err := ValidateSignalChannel(opts.Channel); err != nil {
		return WatchSignal{}, false, err
	}
	if !utf8.ValidString(opts.Body) || JSONStringLen(opts.Body) > MaxSignalBodyBytes {
		return WatchSignal{}, false, fmt.Errorf("%w must be valid UTF-8 and at most %d bytes once JSON-encoded", ErrInvalidSignalBody, MaxSignalBodyBytes)
	}
	if err := validateSignalText("idempotency key", opts.IdempotencyKey, 200); err != nil {
		return WatchSignal{}, false, err
	}
	if err := validateSignalText("sender session id", opts.FromSessionID, MaxSignalSenderBytes); err != nil {
		return WatchSignal{}, false, err
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	sum := sha256.Sum256([]byte(opts.Body))
	bodyHash := hex.EncodeToString(sum[:])

	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return WatchSignal{}, false, err
	}
	defer func() { _ = tx.Rollback() }()

	if err := pruneSignals(ctx, tx, now.Add(-SignalRetention)); err != nil {
		return WatchSignal{}, false, fmt.Errorf("prune signals: %w", err)
	}

	if opts.IdempotencyKey != "" {
		existing, existingHash, err := signalByKey(ctx, tx, opts.Channel, opts.IdempotencyKey)
		switch {
		case err == nil:
			if existingHash != bodyHash {
				return WatchSignal{}, false, ErrIdempotencyConflict
			}
			if err := tx.Commit(); err != nil {
				return WatchSignal{}, false, err
			}
			return existing, true, nil
		case !errors.Is(err, sql.ErrNoRows):
			return WatchSignal{}, false, err
		}
	}

	res, err := tx.ExecContext(ctx, `
		INSERT INTO watch_signals (channel, idempotency_key, from_session_id, body, body_sha256, created_at)
		VALUES (?, NULLIF(?, ''), NULLIF(?, ''), ?, ?, ?)
	`, opts.Channel, opts.IdempotencyKey, opts.FromSessionID, opts.Body, bodyHash, now.UnixMilli())
	if err != nil {
		return WatchSignal{}, false, fmt.Errorf("save signal: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return WatchSignal{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return WatchSignal{}, false, err
	}
	return WatchSignal{
		ID: id, Channel: opts.Channel, IdempotencyKey: opts.IdempotencyKey,
		FromSessionID: opts.FromSessionID, Body: opts.Body, CreatedAt: time.UnixMilli(now.UnixMilli()),
	}, false, nil
}

func pruneSignals(ctx context.Context, tx *sql.Tx, cutoff time.Time) error {
	// Record the watermark before deleting, so the two never disagree.
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO watch_signal_channels (channel, pruned_through_id)
		SELECT channel, MAX(id) FROM watch_signals WHERE created_at < ? GROUP BY channel
		ON CONFLICT(channel) DO UPDATE
		SET pruned_through_id = MAX(pruned_through_id, excluded.pruned_through_id)
	`, cutoff.UnixMilli()); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM watch_signals WHERE created_at < ?`, cutoff.UnixMilli())
	return err
}

func signalByKey(ctx context.Context, tx *sql.Tx, channel, key string) (WatchSignal, string, error) {
	var s WatchSignal
	var hash string
	var created int64
	err := tx.QueryRowContext(ctx, `
		SELECT id, channel, COALESCE(idempotency_key, ''), COALESCE(from_session_id, ''), body, body_sha256, created_at
		FROM watch_signals WHERE channel = ? AND idempotency_key = ?
	`, channel, key).Scan(&s.ID, &s.Channel, &s.IdempotencyKey, &s.FromSessionID, &s.Body, &hash, &created)
	s.CreatedAt = time.UnixMilli(created)
	return s, hash, err
}

// SignalPage is one read of a channel: signals after a position, in id order,
// and the channel's pruning watermark from the same snapshot.
type SignalPage struct {
	Signals []WatchSignal
	// PrunedThroughID is the highest signal id pruned from the channel. A
	// reader positioned below it missed at least one signal.
	PrunedThroughID int64
}

// signalsAfterHook runs between the watermark read and the signal read in
// SignalsAfter. Tests use it to prune from another connection mid-read.
var signalsAfterHook func()

// SignalsAfter returns up to limit signals on channel with id > afterID, and
// the channel's pruning watermark, read in one transaction.
func (db *DB) SignalsAfter(ctx context.Context, channel string, afterID int64, limit int) (SignalPage, error) {
	if limit <= 0 {
		return SignalPage{}, fmt.Errorf("limit must be positive")
	}
	tx, err := db.conn.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return SignalPage{}, err
	}
	defer func() { _ = tx.Rollback() }()

	page := SignalPage{Signals: []WatchSignal{}}
	err = tx.QueryRowContext(ctx, `
		SELECT COALESCE((SELECT pruned_through_id FROM watch_signal_channels WHERE channel = ?), 0)
	`, channel).Scan(&page.PrunedThroughID)
	if err != nil {
		return SignalPage{}, err
	}
	if signalsAfterHook != nil {
		signalsAfterHook()
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT id, channel, COALESCE(idempotency_key, ''), COALESCE(from_session_id, ''), body, created_at
		FROM watch_signals WHERE channel = ? AND id > ? ORDER BY id LIMIT ?
	`, channel, afterID, limit)
	if err != nil {
		return SignalPage{}, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var s WatchSignal
		var created int64
		if err := rows.Scan(&s.ID, &s.Channel, &s.IdempotencyKey, &s.FromSessionID, &s.Body, &created); err != nil {
			return SignalPage{}, err
		}
		s.CreatedAt = time.UnixMilli(created)
		page.Signals = append(page.Signals, s)
	}
	return page, rows.Err()
}

// SignalHighWater returns the highest signal id ever assigned. A watch armed
// at this position receives every signal posted afterwards. It reads the
// AUTOINCREMENT sequence rather than MAX(id), which drops when rows are pruned.
func (db *DB) SignalHighWater(ctx context.Context) (int64, error) {
	var seq int64
	err := db.conn.QueryRowContext(ctx, `
		SELECT COALESCE((SELECT seq FROM sqlite_sequence WHERE name = 'watch_signals'), 0)
	`).Scan(&seq)
	return seq, err
}
