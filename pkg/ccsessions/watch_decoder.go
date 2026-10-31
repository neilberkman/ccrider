package ccsessions

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/neilberkman/ccrider/pkg/sessionevents"
)

// Claude Code interrupt notices. A user record whose whole text is one of
// these ends the running turn.
var claudeInterruptNotices = map[string]bool{
	"[Request interrupted by user]":              true,
	"[Request interrupted by user for tool use]": true,
}

// User text with these prefixes is a slash command or its output, which
// doesn't run the agent by itself.
var claudeCommandPrefixes = []string{"<command-name>", "<local-command-stdout>", "<local-command-caveat>"}

// claudeWatchRecord is the subset of a Claude Code transcript record the
// watch decoder reads.
type claudeWatchRecord struct {
	Type              string          `json:"type"`
	Subtype           string          `json:"subtype"`
	UUID              string          `json:"uuid"`
	Timestamp         string          `json:"timestamp"`
	IsSidechain       bool            `json:"isSidechain"`
	IsMeta            bool            `json:"isMeta"`
	IsCompactSummary  bool            `json:"isCompactSummary"`
	IsAPIErrorMessage bool            `json:"isApiErrorMessage"`
	DurationMS        int64           `json:"durationMs"`
	Entrypoint        string          `json:"entrypoint"`
	Message           json.RawMessage `json:"message"`
}

type claudeWatchMessage struct {
	Content json.RawMessage `json:"content"`
}

// ClaudeDecoder decodes Claude Code transcript records into watch events.
// See docs/plans/2026-10-30-session-watch-spec.md, "Claude Code turn decoder".
type ClaudeDecoder struct {
	state sessionevents.State
}

// NewClaudeDecoder returns a decoder starting in state.
func NewClaudeDecoder(state sessionevents.State) *ClaudeDecoder {
	d := &ClaudeDecoder{}
	d.SetState(state)
	return d
}

func (d *ClaudeDecoder) State() sessionevents.State { return d.state }

func (d *ClaudeDecoder) SetState(s sessionevents.State) {
	if s.Turn == "" {
		s.Turn = sessionevents.TurnIdle
	}
	d.state = s
}

// ClaudeEntrypoint returns the record's entrypoint ("cli" for interactive
// sessions, "sdk-cli" for headless ones), or "" if it has none.
func ClaudeEntrypoint(record []byte) string {
	var r struct {
		Entrypoint string `json:"entrypoint"`
	}
	if json.Unmarshal(record, &r) != nil {
		return ""
	}
	return r.Entrypoint
}

func (d *ClaudeDecoder) Decode(record []byte) ([]sessionevents.Event, error) {
	var r claudeWatchRecord
	if err := json.Unmarshal(record, &r); err != nil {
		return nil, err
	}
	if r.IsSidechain {
		return nil, nil
	}
	at := parseClaudeTime(r.Timestamp)
	switch r.Type {
	case "user":
		return d.decodeUser(r, at)
	case "assistant":
		return d.decodeAssistant(r, at)
	case "system":
		return d.decodeSystem(r, at), nil
	}
	return nil, nil
}

func (d *ClaudeDecoder) decodeUser(r claudeWatchRecord, at time.Time) ([]sessionevents.Event, error) {
	if r.IsMeta || r.IsCompactSummary {
		return nil, nil
	}
	text, hasToolResult, err := claudeUserText(r.Message)
	if err != nil {
		return nil, err
	}
	if hasToolResult {
		return nil, nil
	}
	trimmed := strings.TrimSpace(text)
	if claudeInterruptNotices[trimmed] {
		if d.state.Turn == sessionevents.TurnIdle {
			return nil, nil
		}
		return []sessionevents.Event{d.endTurn(r, at, sessionevents.OutcomeInterrupted)}, nil
	}
	if trimmed == "" {
		return nil, nil
	}
	for _, prefix := range claudeCommandPrefixes {
		if strings.HasPrefix(trimmed, prefix) {
			return nil, nil
		}
	}
	if d.state.Turn == sessionevents.TurnIdle {
		d.startTurn(r.UUID)
	}
	return nil, nil
}

func (d *ClaudeDecoder) decodeAssistant(r claudeWatchRecord, at time.Time) ([]sessionevents.Event, error) {
	// Validate before changing state, so a malformed record leaves the
	// decoder where it was.
	text, err := claudeAssistantText(r.Message)
	if err != nil {
		return nil, err
	}
	if d.state.Turn == sessionevents.TurnIdle {
		d.startTurn(r.UUID)
	}
	d.state.LastAssistantError = r.IsAPIErrorMessage
	if r.IsAPIErrorMessage || text == "" {
		return nil, nil
	}
	return []sessionevents.Event{{
		Kind: sessionevents.KindMessage, RecordKey: r.UUID, At: at, Text: text,
	}}, nil
}

func (d *ClaudeDecoder) decodeSystem(r claudeWatchRecord, at time.Time) []sessionevents.Event {
	switch r.Subtype {
	case "model_refusal_no_fallback":
		if d.state.Turn != sessionevents.TurnIdle {
			d.state.Refused = true
		}
	case "turn_duration":
		if d.state.Turn == sessionevents.TurnIdle {
			return nil // a boundary with no activity since the last one
		}
		outcome := sessionevents.OutcomeCompleted
		switch {
		case d.state.Refused:
			outcome = sessionevents.OutcomeRefused
		case d.state.LastAssistantError:
			outcome = sessionevents.OutcomeFailed
		}
		ev := d.endTurn(r, at, outcome)
		ev.DurationMS = r.DurationMS
		return []sessionevents.Event{ev}
	}
	return nil
}

func (d *ClaudeDecoder) startTurn(uuid string) {
	d.state = sessionevents.State{Turn: sessionevents.TurnRunning, TurnID: uuid}
}

func (d *ClaudeDecoder) endTurn(r claudeWatchRecord, at time.Time, outcome string) sessionevents.Event {
	ev := sessionevents.Event{
		Kind: sessionevents.KindTurnEnded, RecordKey: r.UUID, At: at,
		TurnID: d.state.TurnID, Outcome: outcome,
		Partial: d.state.Turn == sessionevents.TurnUnknown,
	}
	d.state = sessionevents.State{Turn: sessionevents.TurnIdle}
	return ev
}

// claudeContent parses a message's content: a string, or a list of blocks.
// Anything else is a malformed record.
func claudeContent(raw json.RawMessage) (string, []sessionevents.Block, error) {
	if sessionevents.IsJSONNull(raw) {
		return "", nil, fmt.Errorf("message is missing or null")
	}
	var m claudeWatchMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return "", nil, fmt.Errorf("message: %w", err)
	}
	if sessionevents.IsJSONString(m.Content) {
		var s string
		if err := json.Unmarshal(m.Content, &s); err != nil {
			return "", nil, fmt.Errorf("message content: %w", err)
		}
		return s, nil, nil
	}
	blocks, err := sessionevents.ParseBlocks(m.Content, "text")
	if err != nil {
		return "", nil, fmt.Errorf("message %w", err)
	}
	return "", blocks, nil
}

// claudeUserText returns a user record's text blocks joined by newlines, and
// whether it carries a tool result.
func claudeUserText(raw json.RawMessage) (string, bool, error) {
	s, blocks, err := claudeContent(raw)
	if err != nil || blocks == nil {
		return s, false, err
	}
	var texts []string
	toolResult := false
	for _, b := range blocks {
		switch b.Type {
		case "text":
			texts = append(texts, b.Text)
		case "tool_result":
			toolResult = true
		}
	}
	return strings.Join(texts, "\n"), toolResult, nil
}

// claudeAssistantText returns the assistant's own text blocks joined by
// newlines. Tool calls and thinking are excluded.
func claudeAssistantText(raw json.RawMessage) (string, error) {
	s, blocks, err := claudeContent(raw)
	if err != nil || blocks == nil {
		return s, err
	}
	var texts []string
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			texts = append(texts, b.Text)
		}
	}
	return strings.Join(texts, "\n"), nil
}

func parseClaudeTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}
