package ccsessions

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/neilberkman/ccrider/pkg/sessionevents"
)

// Records below keep the shape of real Claude Code 2.1.295 transcript
// records with content replaced.
const (
	cPrompt     = `{"type":"user","uuid":"u1","timestamp":"2026-10-09T00:57:00Z","entrypoint":"cli","message":{"role":"user","content":"fix the bug"}}`
	cPrompt2    = `{"type":"user","uuid":"u2","timestamp":"2026-10-09T00:58:00Z","message":{"role":"user","content":"and another thing"}}`
	cTaskNote   = `{"type":"user","uuid":"tn","message":{"role":"user","content":"<task-notification>\n<task-id>b1</task-id></task-notification>"}}`
	cCommand    = `{"type":"user","uuid":"cmd","message":{"role":"user","content":"<command-name>/model</command-name>"}}`
	cMeta       = `{"type":"user","uuid":"meta","isMeta":true,"message":{"role":"user","content":"caveat"}}`
	cToolResult = `{"type":"user","uuid":"tr","message":{"role":"user","content":[{"type":"tool_result","content":"ok"}]}}`
	cThinking   = `{"type":"assistant","uuid":"a0","message":{"role":"assistant","content":[{"type":"thinking","thinking":"secret DONE-PHRASE"}]}}`
	cToolUse    = `{"type":"assistant","uuid":"a1","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"echo DONE-PHRASE"}}]}}`
	cText       = `{"type":"assistant","uuid":"a2","timestamp":"2026-10-09T00:57:30Z","message":{"role":"assistant","content":[{"type":"text","text":"All fixed. DONE-PHRASE"}]}}`
	cAPIError   = `{"type":"assistant","uuid":"ae","isApiErrorMessage":true,"message":{"role":"assistant","content":[{"type":"text","text":"API Error: overloaded DONE-PHRASE"}]}}`
	cSidechain  = `{"type":"assistant","uuid":"sc","isSidechain":true,"message":{"role":"assistant","content":[{"type":"text","text":"subagent DONE-PHRASE"}]}}`
	cRefusal    = `{"type":"system","subtype":"model_refusal_no_fallback","uuid":"rf"}`
	cStopHook   = `{"type":"system","subtype":"stop_hook_summary","uuid":"sh"}`
	cSchedFire  = `{"type":"system","subtype":"scheduled_task_fire","uuid":"sf"}`
	cDuration   = `{"type":"system","subtype":"turn_duration","uuid":"td","timestamp":"2026-10-09T00:57:59Z","durationMs":27032}`
	cDuration2  = `{"type":"system","subtype":"turn_duration","uuid":"td2","durationMs":10}`
	cInterrupt  = `{"type":"user","uuid":"int","message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user]"}]}}`
	cIntTool    = `{"type":"user","uuid":"intt","message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user for tool use]"}]}}`
	cQuoted     = `{"type":"user","uuid":"q","message":{"role":"user","content":"why did it say [Request interrupted by user] earlier?"}}`
)

func decodeAll(t *testing.T, state sessionevents.State, records ...string) ([]sessionevents.Event, sessionevents.State) {
	t.Helper()
	d := NewClaudeDecoder(state)
	var out []sessionevents.Event
	for _, r := range records {
		evs, err := d.Decode([]byte(r))
		if err != nil {
			t.Fatalf("decode %s: %v", r, err)
		}
		out = append(out, evs...)
	}
	return out, d.State()
}

func turnEnds(evs []sessionevents.Event) []sessionevents.Event {
	var out []sessionevents.Event
	for _, e := range evs {
		if e.Kind == sessionevents.KindTurnEnded {
			out = append(out, e)
		}
	}
	return out
}

func TestClaudeDecoderOutcomes(t *testing.T) {
	cases := []struct {
		name    string
		records []string
		want    []string // outcomes in order
	}{
		{"normal", []string{cPrompt, cToolUse, cToolResult, cText, cDuration}, []string{"completed"}},
		{"stop hook continuation is one turn", []string{cPrompt, cText, cStopHook, cText, cStopHook, cDuration}, []string{"completed"}},
		{"refusal", []string{cPrompt, cText, cRefusal, cAPIError, cDuration}, []string{"refused"}},
		{"api error", []string{cPrompt, cAPIError, cDuration}, []string{"failed"}},
		{"api error then recovery", []string{cPrompt, cAPIError, cText, cDuration}, []string{"completed"}},
		{"plain interrupt", []string{cPrompt, cToolUse, cInterrupt}, []string{"interrupted"}},
		{"tool-use interrupt then queued work", []string{cPrompt, cToolUse, cPrompt2, cIntTool, cText, cDuration}, []string{"interrupted", "completed"}},
		{"task notification starts a turn", []string{cTaskNote, cText, cDuration}, []string{"completed"}},
		{"turn started by assistant alone", []string{cText, cDuration}, []string{"completed"}},
		{"duplicate boundary while idle", []string{cPrompt, cText, cDuration, cDuration2}, []string{"completed"}},
		{"interrupt while idle", []string{cPrompt, cText, cDuration, cInterrupt}, []string{"completed"}},
		{"quoted interrupt text is a prompt", []string{cQuoted, cText, cDuration}, []string{"completed"}},
		{"commands and meta don't start turns", []string{cCommand, cMeta, cDuration}, nil},
		{"sidechain ignored", []string{cSidechain, cDuration}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			evs, _ := decodeAll(t, sessionevents.State{}, tc.records...)
			var got []string
			for _, e := range turnEnds(evs) {
				got = append(got, e.Outcome)
			}
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("outcomes = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestClaudeDecoderTurnFields(t *testing.T) {
	evs, state := decodeAll(t, sessionevents.State{}, cPrompt, cText, cDuration)
	ends := turnEnds(evs)
	if len(ends) != 1 {
		t.Fatalf("ends = %+v", ends)
	}
	e := ends[0]
	if e.TurnID != "u1" || e.RecordKey != "td" || e.DurationMS != 27032 || e.Partial || e.At.IsZero() {
		t.Fatalf("turn end = %+v", e)
	}
	if state.Turn != sessionevents.TurnIdle {
		t.Fatalf("state after end = %+v", state)
	}
}

// Visible text excludes tool calls, thinking, API errors and sidechains.
func TestClaudeDecoderVisibleText(t *testing.T) {
	evs, _ := decodeAll(t, sessionevents.State{}, cPrompt, cThinking, cToolUse, cAPIError, cSidechain, cText)
	var texts []string
	for _, e := range evs {
		if e.Kind == sessionevents.KindMessage {
			texts = append(texts, e.Text)
		}
	}
	if len(texts) != 1 || texts[0] != "All fixed. DONE-PHRASE" {
		t.Fatalf("messages = %q", texts)
	}
}

// Review round 3 reproduced this: an API error, then a scheduled fire, then
// the duration record. A cursor boundary before the duration must not change
// the outcome.
func TestClaudeDecoderStateSurvivesCheckpoint(t *testing.T) {
	records := []string{cPrompt, cAPIError, cSchedFire, cDuration}
	continuous, _ := decodeAll(t, sessionevents.State{}, records...)

	_, mid := decodeAll(t, sessionevents.State{}, records[:3]...)
	b, err := json.Marshal(mid)
	if err != nil {
		t.Fatal(err)
	}
	var restored sessionevents.State
	if err := json.Unmarshal(b, &restored); err != nil {
		t.Fatal(err)
	}
	resumed, _ := decodeAll(t, restored, records[3:]...)
	if turnEnds(continuous)[0].Outcome != "failed" || turnEnds(resumed)[0].Outcome != "failed" {
		t.Fatalf("continuous %+v, resumed %+v; both should be failed", continuous, resumed)
	}
}

func TestClaudeDecoderUnknownStart(t *testing.T) {
	evs, _ := decodeAll(t, sessionevents.State{Turn: sessionevents.TurnUnknown}, cText, cDuration)
	ends := turnEnds(evs)
	if len(ends) != 1 || !ends[0].Partial || ends[0].TurnID != "" {
		t.Fatalf("ends = %+v, want one partial end", ends)
	}
	// From unknown, an interrupt also emits.
	evs, _ = decodeAll(t, sessionevents.State{Turn: sessionevents.TurnUnknown}, cInterrupt)
	if ends := turnEnds(evs); len(ends) != 1 || ends[0].Outcome != "interrupted" || !ends[0].Partial {
		t.Fatalf("interrupt from unknown = %+v", ends)
	}
}

func TestClaudeEntrypoint(t *testing.T) {
	if got := ClaudeEntrypoint([]byte(cPrompt)); got != "cli" {
		t.Fatalf("entrypoint = %q", got)
	}
	if got := ClaudeEntrypoint([]byte(cText)); got != "" {
		t.Fatalf("entrypoint = %q, want empty", got)
	}
}
