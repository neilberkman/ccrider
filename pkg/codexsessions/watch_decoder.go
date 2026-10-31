package codexsessions

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/neilberkman/ccrider/pkg/sessionevents"
)

// codexWatchRecord is the subset of a Codex rollout record the watch decoder
// reads. Ordinal is a pointer so a missing ordinal (legacy rollouts) can be
// told apart from ordinal 0.
type codexWatchRecord struct {
	Timestamp string          `json:"timestamp"`
	Ordinal   *int64          `json:"ordinal"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type codexWatchPayload struct {
	Type             string          `json:"type"`
	Role             string          `json:"role"`
	Content          json.RawMessage `json:"content"`
	TurnID           string          `json:"turn_id"`
	LastAgentMessage *string         `json:"last_agent_message"`
	Error            json.RawMessage `json:"error"`
	Reason           string          `json:"reason"`
	DurationMS       int64           `json:"duration_ms"`
}

type codexTurnError struct {
	Message        string          `json:"message"`
	CodexErrorInfo json.RawMessage `json:"codex_error_info"`
}

// RolloutMeta is what the watch decoder needs from a rollout's session_meta,
// which is the file's first record and lies before any resume offset.
type RolloutMeta struct {
	ThreadID  string
	Timestamp time.Time
	// HistoryStartOrdinal is the first ordinal that belongs to this thread in
	// a child rollout; earlier records are inherited context. 0 when absent.
	HistoryStartOrdinal int64
}

// ParseRolloutMeta reads a rollout's session_meta record.
func ParseRolloutMeta(record []byte) (RolloutMeta, bool) {
	var r struct {
		Type    string `json:"type"`
		Payload struct {
			ID                          string `json:"id"`
			Timestamp                   string `json:"timestamp"`
			SubagentHistoryStartOrdinal *int64 `json:"subagent_history_start_ordinal"`
		} `json:"payload"`
	}
	if json.Unmarshal(record, &r) != nil || r.Type != "session_meta" || r.Payload.ID == "" {
		return RolloutMeta{}, false
	}
	m := RolloutMeta{ThreadID: r.Payload.ID}
	if t, err := time.Parse(time.RFC3339Nano, r.Payload.Timestamp); err == nil {
		m.Timestamp = t.UTC()
	}
	if r.Payload.SubagentHistoryStartOrdinal != nil {
		m.HistoryStartOrdinal = *r.Payload.SubagentHistoryStartOrdinal
	}
	return m, true
}

// RecordTimestamp returns a rollout record's timestamp, or the zero time.
func RecordTimestamp(record []byte) time.Time {
	var r struct {
		Timestamp string `json:"timestamp"`
	}
	if json.Unmarshal(record, &r) != nil {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, r.Timestamp)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

// CodexDecoder decodes Codex rollout records into watch events.
// See docs/plans/2026-10-30-session-watch-spec.md, "Codex turn decoder".
type CodexDecoder struct {
	meta  RolloutMeta
	state sessionevents.State
}

// NewCodexDecoder returns a decoder for a rollout with the given metadata,
// starting in state.
func NewCodexDecoder(meta RolloutMeta, state sessionevents.State) *CodexDecoder {
	d := &CodexDecoder{meta: meta}
	d.SetState(state)
	return d
}

func (d *CodexDecoder) State() sessionevents.State { return d.state }

func (d *CodexDecoder) SetState(s sessionevents.State) {
	if s.Turn == "" {
		s.Turn = sessionevents.TurnIdle
	}
	d.state = s
}

func (d *CodexDecoder) Decode(record []byte) ([]sessionevents.Event, error) {
	var r codexWatchRecord
	if err := json.Unmarshal(record, &r); err != nil {
		return nil, err
	}
	if r.Type == "session_meta" {
		return nil, nil
	}
	if r.Ordinal == nil {
		return nil, sessionevents.ErrUnsupportedFormat
	}
	if *r.Ordinal < d.meta.HistoryStartOrdinal {
		return nil, nil // inherited from the parent thread
	}
	// Only records the decoder acts on are parsed strictly; a malformed one
	// is an error, so the watch reports it instead of skipping it.
	if r.Type != "response_item" && r.Type != "event_msg" {
		return nil, nil
	}
	// The discriminators themselves must be strings: a malformed type or role
	// on a record the decoder might act on is reported, not skipped.
	var kind struct {
		Type json.RawMessage `json:"type"`
		Role json.RawMessage `json:"role"`
	}
	if err := json.Unmarshal(r.Payload, &kind); err != nil {
		return nil, fmt.Errorf("%s payload: %w", r.Type, err)
	}
	if !sessionevents.IsJSONString(kind.Type) {
		return nil, fmt.Errorf("%s payload has no string type", r.Type)
	}
	var payloadType, role string
	_ = json.Unmarshal(kind.Type, &payloadType)
	if r.Type == "response_item" && payloadType == "message" {
		if !sessionevents.IsJSONString(kind.Role) {
			return nil, fmt.Errorf("response_item message has no string role")
		}
		_ = json.Unmarshal(kind.Role, &role)
	}
	if !codexHandled(r.Type, payloadType, role) {
		return nil, nil
	}
	var p codexWatchPayload
	if err := json.Unmarshal(r.Payload, &p); err != nil {
		return nil, fmt.Errorf("%s %s payload: %w", r.Type, payloadType, err)
	}
	if r.Type == "response_item" {
		if _, err := sessionevents.ParseBlocks(p.Content, "output_text"); err != nil {
			return nil, fmt.Errorf("assistant message %w", err)
		}
	} else {
		var ids struct {
			TurnID json.RawMessage `json:"turn_id"`
		}
		_ = json.Unmarshal(r.Payload, &ids)
		if !sessionevents.IsJSONString(ids.TurnID) {
			return nil, fmt.Errorf("%s has no string turn_id", payloadType)
		}
	}
	key := strconv.FormatInt(*r.Ordinal, 10)
	at := RecordTimestamp(record)

	switch {
	case r.Type == "response_item" && p.Type == "message" && p.Role == "assistant":
		if text := codexOutputText(p.Content); text != "" {
			return []sessionevents.Event{{Kind: sessionevents.KindMessage, RecordKey: key, At: at, Text: text}}, nil
		}
	case r.Type == "event_msg" && p.Type == "task_started":
		d.state = sessionevents.State{Turn: sessionevents.TurnRunning, TurnID: p.TurnID}
	case r.Type == "event_msg" && p.Type == "task_complete":
		ev := d.end(key, at, p)
		if reason, failed := codexTerminalError(p.Error); failed {
			ev.Outcome = sessionevents.OutcomeFailed
			ev.Reason = reason
		} else {
			ev.Outcome = sessionevents.OutcomeCompleted
			if p.LastAgentMessage != nil {
				ev.Excerpt = *p.LastAgentMessage
			}
		}
		return []sessionevents.Event{ev}, nil
	case r.Type == "event_msg" && p.Type == "turn_aborted":
		ev := d.end(key, at, p)
		ev.Outcome = sessionevents.OutcomeInterrupted
		ev.Reason = sessionevents.TruncateUTF8(p.Reason, sessionevents.MaxReasonBytes)
		return []sessionevents.Event{ev}, nil
	}
	return nil, nil
}

// end closes the current turn. An end record names its own turn, which is
// reported even if it differs from the turn the decoder thought was running.
func (d *CodexDecoder) end(key string, at time.Time, p codexWatchPayload) sessionevents.Event {
	ev := sessionevents.Event{
		Kind: sessionevents.KindTurnEnded, RecordKey: key, At: at,
		TurnID: p.TurnID, DurationMS: p.DurationMS,
		Partial: d.state.Turn == sessionevents.TurnUnknown,
	}
	if ev.TurnID == "" {
		ev.TurnID = d.state.TurnID
	}
	d.state = sessionevents.State{Turn: sessionevents.TurnIdle}
	return ev
}

// codexTerminalError reports whether a task_complete error field holds a
// terminal error, and a short reason: the error code if there is one, else
// the message.
func codexTerminalError(raw json.RawMessage) (string, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return "", false
	}
	var e codexTurnError
	if json.Unmarshal(raw, &e) != nil {
		return sessionevents.TruncateUTF8(string(raw), sessionevents.MaxReasonBytes), true
	}
	reason := codexErrorCode(e.CodexErrorInfo)
	if reason == "" {
		reason = e.Message
	}
	if reason == "" {
		reason = "error"
	}
	return sessionevents.TruncateUTF8(reason, sessionevents.MaxReasonBytes), true
}

// codexErrorCode reads error.codex_error_info. Codex serializes it as a
// string for unit variants ("server_overloaded") and as a single-key object
// for variants with data ({"http_connection_failed": {...}}); either way the
// variant name is the code. Anything else gives "".
func codexErrorCode(raw json.RawMessage) string {
	var code string
	if json.Unmarshal(raw, &code) == nil {
		return code
	}
	var variant map[string]json.RawMessage
	if json.Unmarshal(raw, &variant) == nil && len(variant) == 1 {
		for name := range variant {
			return name
		}
	}
	return ""
}

// codexHandled reports whether the decoder acts on a record.
func codexHandled(recordType, payloadType, role string) bool {
	switch recordType {
	case "response_item":
		return payloadType == "message" && role == "assistant"
	case "event_msg":
		return payloadType == "task_started" || payloadType == "task_complete" || payloadType == "turn_aborted"
	}
	return false
}

// codexOutputText joins an assistant message's output_text blocks.
func codexOutputText(raw json.RawMessage) string {
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var texts []string
	for _, b := range blocks {
		if b.Type == "output_text" && b.Text != "" {
			texts = append(texts, b.Text)
		}
	}
	return strings.Join(texts, "\n")
}
