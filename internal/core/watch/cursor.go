package watch

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/neilberkman/ccrider/internal/core/db"
	"github.com/neilberkman/ccrider/pkg/sessionevents"
)

// cursorVersion is the cursor format version. decoderVersion changes whenever
// a transcript decoder changes meaning; cursors from another decoder version
// are rejected so callers re-arm instead of resuming with different rules.
const (
	cursorVersion  = 1
	decoderVersion = 1
)

// cursor is the complete state needed to continue a watch. It travels to the
// caller as opaque base64url JSON, so a restarted server or a CLI process can
// resume from it alone. Transcript fields are reserved for transcript watches.
type cursor struct {
	V       int      `json:"v"`
	DV      int      `json:"dv"`
	Until   []string `json:"until"`
	Phrase  string   `json:"phrase,omitempty"`
	Channel string   `json:"channel,omitempty"`

	SessionID string `json:"sid,omitempty"`
	Provider  string `json:"prov,omitempty"`
	Source    string `json:"src,omitempty"`
	Dev       uint64 `json:"dev,omitempty"`
	Ino       uint64 `json:"ino,omitempty"`
	Offset    int64  `json:"off,omitempty"`
	Tail      string `json:"tail,omitempty"`
	TailLen   int64  `json:"tn,omitempty"`
	Gen       int64  `json:"gen,omitempty"`
	// State is the transcript decoder's state at Offset.
	State *sessionevents.State `json:"ds,omitempty"`

	Signal int64 `json:"sig,omitempty"`
}

func (c cursor) encode() string {
	b, err := json.Marshal(c)
	if err != nil {
		// cursor holds only strings and integers; Marshal cannot fail.
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(s string) (cursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return cursor{}, newError(CodeCursorInvalid, "cursor is not valid base64url")
	}
	var c cursor
	if err := json.Unmarshal(raw, &c); err != nil {
		return cursor{}, newError(CodeCursorInvalid, "cursor is not valid JSON")
	}
	if c.V != cursorVersion {
		return cursor{}, newError(CodeCursorInvalid, fmt.Sprintf("cursor version %d is not supported; arm the watch again", c.V))
	}
	if c.DV != decoderVersion {
		return cursor{}, newError(CodeCursorInvalid, fmt.Sprintf("cursor was made by decoder version %d, this ccrider uses %d; arm the watch again", c.DV, decoderVersion))
	}
	conds, err := normalizeConditions(c.Until)
	if err != nil {
		return cursor{}, newError(CodeCursorInvalid, "cursor conditions are invalid: "+err.Error())
	}
	c.Until = conds
	if err := c.validate(); err != nil {
		return cursor{}, newError(CodeCursorInvalid, err.Error())
	}
	return c, nil
}

// validate checks the fields a cursor's conditions require and rejects fields
// that belong to another kind of watch.
func (c cursor) validate() error {
	if c.Until[0] == CondSignal {
		if err := db.ValidateSignalChannel(c.Channel); err != nil {
			return err
		}
		if c.Signal < 0 {
			return errors.New("signal position is negative")
		}
		if c.SessionID != "" || c.Provider != "" || c.Source != "" || c.Phrase != "" ||
			c.Dev != 0 || c.Ino != 0 || c.Offset != 0 || c.Tail != "" ||
			c.TailLen != 0 || c.Gen != 0 || c.State != nil {
			return errors.New("signal cursor carries transcript fields")
		}
		return nil
	}
	if c.Channel != "" || c.Signal != 0 {
		return errors.New("transcript cursor carries signal fields")
	}
	if c.SessionID == "" || c.Source == "" {
		return errors.New("transcript cursor has no session")
	}
	if c.Provider != providerClaude && c.Provider != providerCodex {
		return fmt.Errorf("transcript cursor has unsupported provider %q", c.Provider)
	}
	if c.Offset < 0 || c.TailLen < 0 || c.TailLen > c.Offset || c.Gen < 0 || (c.Offset > 0 && (c.Tail == "" || c.TailLen == 0)) {
		return errors.New("transcript cursor position is inconsistent")
	}
	if c.State == nil {
		return errors.New("transcript cursor has no decoder state")
	}
	switch c.State.Turn {
	case sessionevents.TurnIdle, sessionevents.TurnRunning, sessionevents.TurnUnknown:
	default:
		return fmt.Errorf("transcript cursor has unknown turn state %q", c.State.Turn)
	}
	if slices.Contains(c.Until, CondPhrase) {
		if err := validatePhrase(c.Phrase); err != nil {
			return err
		}
	} else if c.Phrase != "" {
		return errors.New("cursor has a phrase but no phrase condition")
	}
	return nil
}

// sameConditions reports whether conditions a caller supplied alongside a
// cursor agree with the cursor's own. Unsupplied fields are not compared.
func (c cursor) sameConditions(spec Spec) bool {
	if len(spec.Until) > 0 {
		conds, err := normalizeConditions(spec.Until)
		if err != nil || !slices.Equal(conds, c.Until) {
			return false
		}
	}
	if spec.Phrase != "" && spec.Phrase != c.Phrase {
		return false
	}
	if spec.Channel != "" && spec.Channel != c.Channel {
		return false
	}
	return true
}
