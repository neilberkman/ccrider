// Package watch lets one agent session wait for something to happen in
// another: a signal posted to a channel, or (later) a message, phrase or turn
// end in the target's transcript. Waiting is cursor-based: arming returns a
// cursor at the current end of the stream, and each wait returns events after
// the cursor plus a cursor to continue from. Delivery is at least once; every
// event has a stable id for deduplication.
//
// See docs/plans/2026-10-30-session-watch-spec.md.
package watch

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/neilberkman/ccrider/internal/core/db"
)

// Condition names.
const (
	CondMessage = "message"
	CondPhrase  = "phrase"
	CondTurnEnd = "turn_end"
	CondSignal  = "signal"
)

// Result statuses.
const (
	StatusArmed         = "armed"
	StatusEvent         = "event"
	StatusWaiting       = "waiting"
	StatusGap           = "gap"
	StatusCursorExpired = "cursor_expired"
)

// Error codes returned in *Error.
const (
	CodeNotFound             = "not_found"
	CodeUnsupportedCondition = "unsupported_condition"
	CodeUnsupportedFormat    = "unsupported_format"
	CodeCursorInvalid        = "cursor_invalid"
	CodeCursorMismatch       = "cursor_mismatch"
	CodeInvalidArgument      = "invalid_argument"
)

// Error is a watch failure with a stable code interfaces can report.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func newError(code, msg string) *Error { return &Error{Code: code, Message: msg} }

// Default limits. Interfaces may pass smaller or larger values.
const (
	DefaultMaxEvents    = 20
	DefaultMaxBytes     = 256 << 10
	DefaultPollInterval = time.Second
)

// Spec names what to watch. For signal watches SessionID is empty.
type Spec struct {
	SessionID   string
	Until       []string
	Phrase      string
	Channel     string
	MintPhrase  bool
	MintChannel bool
}

// Limits bound one result. A record's events are never split, so the first
// record is returned even if it alone exceeds a limit.
type Limits struct {
	MaxEvents int
	MaxBytes  int
}

func (l Limits) withDefaults() Limits {
	if l.MaxEvents <= 0 {
		l.MaxEvents = DefaultMaxEvents
	}
	if l.MaxBytes <= 0 {
		l.MaxBytes = DefaultMaxBytes
	}
	return l
}

// Event is one thing that happened. EventID is stable across replays.
type Event struct {
	EventID   string    `json:"event_id"`
	Kind      string    `json:"kind"`
	Matched   []string  `json:"matched"`
	SessionID string    `json:"session_id,omitempty"`
	Provider  string    `json:"provider,omitempty"`
	At        time.Time `json:"at"`

	// Turn end fields.
	TurnID     string `json:"turn_id,omitempty"`
	Outcome    string `json:"outcome,omitempty"`
	Reason     string `json:"reason,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
	Partial    bool   `json:"partial,omitempty"`

	// Excerpt is the start of a message's text, or a turn's final message
	// when the transcript records one.
	Excerpt string `json:"excerpt,omitempty"`

	Signal *Signal `json:"signal,omitempty"`
}

// MaxExcerptBytes caps Event.Excerpt.
const MaxExcerptBytes = 2 << 10

// Signal is the payload of a signal event.
type Signal struct {
	ID            int64  `json:"id"`
	Channel       string `json:"channel"`
	Body          string `json:"body"`
	FromSessionID string `json:"from_session_id,omitempty"`
}

// Result is the outcome of Arm or Wait. Cursor is always set.
type Result struct {
	Status      string
	Events      []Event
	Cursor      string
	Phrase      string
	Channel     string
	TargetState string
	GapReason   string
	// Source names the transcript file now being read, after a Codex
	// rollout change.
	Source string
}

// Watcher arms and waits. It holds no per-watch state; everything needed to
// continue travels in the cursor.
type Watcher struct {
	DB           *db.DB
	PollInterval time.Duration
}

func (w *Watcher) pollInterval() time.Duration {
	if w.PollInterval > 0 {
		return w.PollInterval
	}
	return DefaultPollInterval
}

// normalizeConditions validates, deduplicates and sorts condition names.
// Signal watches can't be combined with transcript conditions in v1.
func normalizeConditions(until []string) ([]string, error) {
	if len(until) == 0 {
		return nil, errors.New("at least one condition is required")
	}
	seen := map[string]bool{}
	var out []string
	for _, c := range until {
		switch c {
		case CondMessage, CondPhrase, CondTurnEnd, CondSignal:
		default:
			return nil, fmt.Errorf("unknown condition %q", c)
		}
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	slices.Sort(out)
	if seen[CondSignal] && len(out) > 1 {
		return nil, errors.New("signal can't be combined with transcript conditions")
	}
	return out, nil
}

// Arm validates spec and returns a cursor at the current end of the watched
// stream. It never waits.
func (w *Watcher) Arm(ctx context.Context, spec Spec) (Result, error) {
	conds, err := normalizeConditions(spec.Until)
	if err != nil {
		return Result{}, newError(CodeInvalidArgument, err.Error())
	}
	if conds[0] != CondSignal {
		return w.armTranscript(ctx, spec, conds)
	}
	if spec.SessionID != "" {
		return Result{}, newError(CodeInvalidArgument, "signal watches don't take a session id")
	}
	if spec.Phrase != "" || spec.MintPhrase {
		return Result{}, newError(CodeInvalidArgument, "phrase applies only to the phrase condition")
	}
	channel := spec.Channel
	switch {
	case channel != "" && spec.MintChannel:
		return Result{}, newError(CodeInvalidArgument, "give a channel or ask for one to be minted, not both")
	case channel == "" && !spec.MintChannel:
		return Result{}, newError(CodeInvalidArgument, "a signal watch needs a channel; pass one or ask for one to be minted")
	case channel == "":
		channel, err = mintChannel()
		if err != nil {
			return Result{}, err
		}
	}
	if err := db.ValidateSignalChannel(channel); err != nil {
		return Result{}, newError(CodeInvalidArgument, err.Error())
	}
	high, err := w.DB.SignalHighWater(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("read signal position: %w", err)
	}
	c := cursor{V: cursorVersion, DV: decoderVersion, Until: conds, Channel: channel, Signal: high}
	return Result{Status: StatusArmed, Events: []Event{}, Cursor: c.encode(), Channel: channel}, nil
}

// Wait returns events after the cursor, waiting until at least one arrives or
// ctx's deadline passes. At the deadline it returns StatusWaiting with a cursor
// to continue from. If ctx is cancelled for another reason, it returns the
// context's error. spec may repeat the watch's conditions; if it does, they
// must match the cursor's.
func (w *Watcher) Wait(ctx context.Context, cursorStr string, spec Spec, limits Limits) (Result, error) {
	c, err := decodeCursor(cursorStr)
	if err != nil {
		return Result{}, err
	}
	if !c.sameConditions(spec) {
		return Result{}, newError(CodeCursorMismatch, "the conditions given don't match the cursor's; omit them or arm a new watch")
	}
	limits = limits.withDefaults()
	if c.Until[0] != CondSignal {
		return w.waitTranscript(ctx, c, limits)
	}

	ticker := time.NewTicker(w.pollInterval())
	defer ticker.Stop()
	for {
		res, done, err := w.pollSignals(ctx, c, limits)
		if err != nil {
			if ctx.Err() != nil {
				return w.waitEnded(ctx, c)
			}
			return Result{}, err
		}
		if done {
			return res, nil
		}
		select {
		case <-ctx.Done():
			return w.waitEnded(ctx, c)
		case <-ticker.C:
		}
	}
}

// waitEnded turns a finished context into a result: a deadline means the
// caller should continue with the same cursor; anything else is an error.
func (w *Watcher) waitEnded(ctx context.Context, c cursor) (Result, error) {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return Result{Status: StatusWaiting, Events: []Event{}, Cursor: c.encode(), Channel: c.Channel, Phrase: c.Phrase}, nil
	}
	return Result{}, ctx.Err()
}

func (w *Watcher) pollSignals(ctx context.Context, c cursor, limits Limits) (Result, bool, error) {
	page, err := w.DB.SignalsAfter(ctx, c.Channel, c.Signal, limits.MaxEvents)
	if err != nil {
		return Result{}, false, fmt.Errorf("read signals: %w", err)
	}
	if page.PrunedThroughID > c.Signal {
		high, err := w.DB.SignalHighWater(ctx)
		if err != nil {
			return Result{}, false, fmt.Errorf("read signal position: %w", err)
		}
		fresh := c
		fresh.Signal = high
		return Result{
			Status: StatusCursorExpired, Events: []Event{}, Cursor: fresh.encode(), Channel: c.Channel,
		}, true, nil
	}
	if len(page.Signals) == 0 {
		return Result{}, false, nil
	}
	events := make([]Event, 0, len(page.Signals))
	size := 0
	for _, s := range page.Signals {
		ev := signalEvent(s)
		n := eventSize(ev)
		if len(events) > 0 && size+n > limits.MaxBytes {
			break
		}
		events = append(events, ev)
		size += n
		c.Signal = s.ID
	}
	return Result{Status: StatusEvent, Events: events, Cursor: c.encode(), Channel: c.Channel}, true, nil
}

func signalEvent(s db.WatchSignal) Event {
	return Event{
		EventID: "signal:" + strconv.FormatInt(s.ID, 10),
		Kind:    CondSignal,
		Matched: []string{CondSignal},
		At:      s.CreatedAt.UTC(),
		Signal: &Signal{
			ID: s.ID, Channel: s.Channel, Body: s.Body, FromSessionID: s.FromSessionID,
		},
	}
}

func eventSize(ev Event) int {
	b, err := MarshalJSON(ev)
	if err != nil {
		return 0
	}
	return len(b)
}

// MarshalJSON encodes v the way watch results are sized: encoding/json with
// HTML escaping off, so "<", ">" and "&" take one byte each. Interfaces
// should encode results with it so the byte budget matches their output.
func MarshalJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte{'\n'}), nil
}

func mintChannel() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("mint channel: %w", err)
	}
	return "watch-" + hex.EncodeToString(b[:]), nil
}

// SendSignal posts a signal to a channel. See db.SendSignal for idempotency
// and retention.
func (w *Watcher) SendSignal(ctx context.Context, opts db.SendSignalOptions) (db.WatchSignal, bool, error) {
	return w.DB.SendSignal(ctx, opts)
}
