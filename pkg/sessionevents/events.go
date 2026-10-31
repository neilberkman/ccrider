// Package sessionevents defines the provider-neutral events transcript
// decoders produce for session watches: assistant messages and turn ends.
// Each provider package implements a Decoder over its own record format.
package sessionevents

import (
	"errors"
	"time"
)

// Event kinds.
const (
	KindMessage   = "message"
	KindTurnEnded = "turn_ended"
)

// Turn outcomes.
const (
	OutcomeCompleted   = "completed"
	OutcomeRefused     = "refused"
	OutcomeFailed      = "failed"
	OutcomeInterrupted = "interrupted"
)

// Turn states.
const (
	TurnIdle    = "idle"
	TurnRunning = "running"
	TurnUnknown = "unknown"
)

// MaxReasonBytes caps Event.Reason.
const MaxReasonBytes = 200

// ErrUnsupportedFormat means a record isn't in a format the decoder supports,
// for example a Codex rollout record without an ordinal.
var ErrUnsupportedFormat = errors.New("unsupported transcript format")

// Event is one thing a record says happened.
type Event struct {
	Kind string
	// RecordKey identifies the source record within its file: the record
	// uuid for Claude Code, the ordinal for Codex. Callers build event ids
	// from it.
	RecordKey string
	At        time.Time

	// Text is the assistant's visible text, for KindMessage.
	Text string

	// Turn end fields, for KindTurnEnded.
	TurnID     string
	Outcome    string
	Reason     string
	DurationMS int64
	Excerpt    string
	// Partial means the decoder started without knowing the turn's start,
	// so TurnID may be empty and the outcome may miss earlier evidence.
	Partial bool
}

// State is everything a decoder needs to resume exactly where it stopped.
// It is stored in watch cursors, so field names are short and stable.
type State struct {
	Turn               string `json:"t"`
	TurnID             string `json:"id,omitempty"`
	Refused            bool   `json:"r,omitempty"`
	LastAssistantError bool   `json:"e,omitempty"`
}

// Decoder turns transcript records into events. Decode is called once per
// complete record, in file order. State and SetState snapshot and restore
// the decoder so a watch can resume from a cursor and get the same events it
// would have got decoding without a break.
type Decoder interface {
	Decode(record []byte) ([]Event, error)
	State() State
	SetState(State)
}

// TruncateUTF8 shortens s to at most n bytes without splitting a character.
func TruncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && cut < len(s) && s[cut]&0xC0 == 0x80 {
		cut--
	}
	return s[:cut]
}
