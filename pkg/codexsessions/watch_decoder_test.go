package codexsessions

import (
	"errors"
	"testing"

	"github.com/neilberkman/ccrider/pkg/sessionevents"
)

// Records below keep the shape of real Codex 0.165.0 rollout records with
// content replaced.
const (
	xMeta        = `{"timestamp":"2026-10-30T09:44:51Z","ordinal":0,"type":"session_meta","payload":{"id":"thread-1","timestamp":"2026-10-30T09:44:51Z"}}`
	xChildMeta   = `{"timestamp":"2026-10-30T09:44:51Z","ordinal":0,"type":"session_meta","payload":{"id":"child-1","timestamp":"2026-10-30T09:44:51Z","subagent_history_start_ordinal":5}}`
	xStarted     = `{"timestamp":"2026-10-30T09:45:00Z","ordinal":1,"type":"event_msg","payload":{"type":"task_started","turn_id":"t1"}}`
	xAssistant   = `{"timestamp":"2026-10-30T09:45:10Z","ordinal":2,"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Done. DONE-PHRASE"}]}}`
	xUser        = `{"timestamp":"2026-10-30T09:45:11Z","ordinal":3,"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"say DONE-PHRASE"}]}}`
	xAgentMsg    = `{"timestamp":"2026-10-30T09:45:12Z","ordinal":4,"type":"response_item","payload":{"type":"agent_message","author":"/root","recipient":"/root/w","content":[{"type":"input_text","text":"DONE-PHRASE"}]}}`
	xLegacyAgent = `{"timestamp":"2026-10-30T09:45:13Z","ordinal":5,"type":"event_msg","payload":{"type":"agent_message","message":"DONE-PHRASE"}}`
	xComplete    = `{"timestamp":"2026-10-30T09:49:03Z","ordinal":6,"type":"event_msg","payload":{"type":"task_complete","turn_id":"t1","last_agent_message":"Implemented the fix.","duration_ms":252318}}`
	xFailed      = `{"timestamp":"2026-10-19T05:49:11Z","ordinal":7,"type":"event_msg","payload":{"type":"task_complete","turn_id":"t2","last_agent_message":null,"error":{"message":"Selected model is at capacity.","codex_error_info":"server_overloaded"}}}`
	xFailedObj   = `{"timestamp":"2026-10-19T05:49:12Z","ordinal":8,"type":"event_msg","payload":{"type":"task_complete","turn_id":"t3","error":{"message":"connection failed","codex_error_info":{"http_connection_failed":{"http_status_code":null}}}}}`
	xFailedMsg   = `{"timestamp":"2026-10-19T05:49:13Z","ordinal":9,"type":"event_msg","payload":{"type":"task_complete","turn_id":"t4","error":{"message":"something broke"}}}`
	xAborted     = `{"timestamp":"2026-10-29T04:39:21Z","ordinal":10,"type":"event_msg","payload":{"type":"turn_aborted","turn_id":"t5","reason":"interrupted","duration_ms":522}}`
	xStartedB    = `{"timestamp":"2026-10-30T09:50:00Z","ordinal":11,"type":"event_msg","payload":{"type":"task_started","turn_id":"t6"}}`
	xStartedC    = `{"timestamp":"2026-10-30T09:51:00Z","ordinal":12,"type":"event_msg","payload":{"type":"task_started","turn_id":"t7"}}`
	xNoOrdinal   = `{"timestamp":"2026-01-01T00:00:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"old"}}`
)

func decodeCodex(t *testing.T, metaLine string, records ...string) []sessionevents.Event {
	t.Helper()
	meta, ok := ParseRolloutMeta([]byte(metaLine))
	if !ok {
		t.Fatal("bad meta")
	}
	d := NewCodexDecoder(meta, sessionevents.State{})
	var out []sessionevents.Event
	for _, r := range records {
		evs, err := d.Decode([]byte(r))
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		out = append(out, evs...)
	}
	return out
}

func TestCodexDecoderOutcomes(t *testing.T) {
	evs := decodeCodex(t, xMeta, xStarted, xAssistant, xComplete, xFailed, xFailedObj, xFailedMsg, xAborted)
	type end struct{ outcome, reason, turn, excerpt string }
	var got []end
	for _, e := range evs {
		if e.Kind == sessionevents.KindTurnEnded {
			got = append(got, end{e.Outcome, e.Reason, e.TurnID, e.Excerpt})
		}
	}
	want := []end{
		{"completed", "", "t1", "Implemented the fix."},
		{"failed", "server_overloaded", "t2", ""},
		{"failed", "http_connection_failed", "t3", ""},
		{"failed", "something broke", "t4", ""},
		{"interrupted", "interrupted", "t5", ""},
	}
	if len(got) != len(want) {
		t.Fatalf("ends = %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("end %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestCodexDecoderVisibleTextAndKeys(t *testing.T) {
	evs := decodeCodex(t, xMeta, xStarted, xAssistant, xUser, xAgentMsg, xLegacyAgent)
	var msgs []sessionevents.Event
	for _, e := range evs {
		if e.Kind == sessionevents.KindMessage {
			msgs = append(msgs, e)
		}
	}
	if len(msgs) != 1 || msgs[0].Text != "Done. DONE-PHRASE" || msgs[0].RecordKey != "2" {
		t.Fatalf("messages = %+v", msgs)
	}
}

func TestCodexDecoderSupersededTurnHasNoEnd(t *testing.T) {
	meta, _ := ParseRolloutMeta([]byte(xMeta))
	d := NewCodexDecoder(meta, sessionevents.State{})
	for _, r := range []string{xStartedB, xStartedC} {
		evs, err := d.Decode([]byte(r))
		if err != nil || len(evs) != 0 {
			t.Fatalf("evs=%+v err=%v", evs, err)
		}
	}
	if s := d.State(); s.Turn != sessionevents.TurnRunning || s.TurnID != "t7" {
		t.Fatalf("state = %+v", s)
	}
}

func TestCodexDecoderChildSkipsInheritedRecords(t *testing.T) {
	// Ordinals below 5 are inherited from the parent.
	evs := decodeCodex(t, xChildMeta, xStarted, xAssistant, xComplete)
	for _, e := range evs {
		if e.RecordKey == "1" || e.RecordKey == "2" {
			t.Fatalf("inherited record produced %+v", e)
		}
	}
	if len(evs) != 1 || evs[0].Kind != sessionevents.KindTurnEnded {
		t.Fatalf("evs = %+v, want only the child's own turn end", evs)
	}
}

func TestCodexDecoderRejectsLegacyRollouts(t *testing.T) {
	meta, _ := ParseRolloutMeta([]byte(xMeta))
	d := NewCodexDecoder(meta, sessionevents.State{})
	if _, err := d.Decode([]byte(xNoOrdinal)); !errors.Is(err, sessionevents.ErrUnsupportedFormat) {
		t.Fatalf("err = %v, want ErrUnsupportedFormat", err)
	}
}
