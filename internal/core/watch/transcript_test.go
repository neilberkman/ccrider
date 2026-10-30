package watch

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/neilberkman/ccrider/pkg/sessionevents"
)

const (
	claudeSID   = "11111111-2222-3333-4444-555555555555"
	codexThread = "01a0de97-d604-7521-8ee1-568aeef437a8"
	codexRepl   = "01a1096d-01fe-77b1-84b9-7b3804b97501"
)

// Records keep the shape of real Claude Code transcript records.
const (
	tPrompt   = `{"type":"user","uuid":"u1","timestamp":"2026-10-09T00:57:00Z","entrypoint":"cli","message":{"role":"user","content":"fix it"}}`
	tPrompt2  = `{"type":"user","uuid":"u2","timestamp":"2026-10-09T00:58:00Z","entrypoint":"cli","message":{"role":"user","content":"again"}}`
	tText     = `{"type":"assistant","uuid":"a1","timestamp":"2026-10-09T00:57:30Z","message":{"role":"assistant","content":[{"type":"text","text":"Done. say-the-word"}]}}`
	tText2    = `{"type":"assistant","uuid":"a2","timestamp":"2026-10-09T00:58:30Z","message":{"role":"assistant","content":[{"type":"text","text":"Second."}]}}`
	tAPIError = `{"type":"assistant","uuid":"ae","isApiErrorMessage":true,"message":{"role":"assistant","content":[{"type":"text","text":"API Error"}]}}`
	tSched    = `{"type":"system","subtype":"scheduled_task_fire","uuid":"sf"}`
	tDur      = `{"type":"system","subtype":"turn_duration","uuid":"d1","timestamp":"2026-10-09T00:57:59Z","durationMs":59000}`
	tDur2     = `{"type":"system","subtype":"turn_duration","uuid":"d2","timestamp":"2026-10-09T00:58:59Z","durationMs":59000}`
	tHeadless = `{"type":"user","uuid":"h1","entrypoint":"sdk-cli","message":{"role":"user","content":"run"}}`
)

func setupHomes(t *testing.T) (claudeFile, codexDir string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(root, "claude"))
	t.Setenv("CODEX_HOME", filepath.Join(root, "codex"))
	proj := filepath.Join(root, "claude", "projects", "-repo")
	codexDir = filepath.Join(root, "codex", "sessions", "2026", "10", "30")
	for _, d := range []string{proj, codexDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(proj, claudeSID+".jsonl"), codexDir
}

func appendLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	for _, l := range lines {
		if _, err := f.WriteString(l + "\n"); err != nil {
			t.Fatal(err)
		}
	}
}

func appendRaw(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(s); err != nil {
		t.Fatal(err)
	}
}

func arm(t *testing.T, w *Watcher, spec Spec) Result {
	t.Helper()
	res, err := w.Arm(context.Background(), spec)
	if err != nil {
		t.Fatalf("arm: %v", err)
	}
	if res.Status != StatusArmed {
		t.Fatalf("arm = %+v", res)
	}
	return res
}

func waitShort(t *testing.T, w *Watcher, cur string, limits Limits) Result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	res, err := w.Wait(ctx, cur, Spec{}, limits)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	return res
}

func outcomes(res Result) []string {
	var out []string
	for _, e := range res.Events {
		if e.Kind == sessionevents.KindTurnEnded {
			out = append(out, e.Outcome)
		}
	}
	return out
}

func TestClaudeTurnEndAfterIdleArm(t *testing.T) {
	file, _ := setupHomes(t)
	w := newWatcher(t)
	appendLines(t, file, tPrompt, tText, tDur)
	armed := arm(t, w, Spec{SessionID: claudeSID, Until: []string{CondTurnEnd}})
	if armed.TargetState != sessionevents.TurnIdle {
		t.Fatalf("target state = %q, want idle", armed.TargetState)
	}
	if res := waitShort(t, w, armed.Cursor, Limits{}); res.Status != StatusWaiting {
		t.Fatalf("idle target produced %+v", res)
	}
	appendLines(t, file, tPrompt2, tText2, tDur2)
	res := waitShort(t, w, armed.Cursor, Limits{})
	if res.Status != StatusEvent || len(res.Events) != 1 {
		t.Fatalf("result = %+v", res)
	}
	ev := res.Events[0]
	if ev.Outcome != "completed" || ev.TurnID != "u2" || ev.EventID != "claude:"+claudeSID+":d2:0" || ev.Provider != "claude" {
		t.Fatalf("event = %+v", ev)
	}
}

func TestClaudeRunningTurnAtArmFires(t *testing.T) {
	file, _ := setupHomes(t)
	w := newWatcher(t)
	appendLines(t, file, tPrompt, tText)
	armed := arm(t, w, Spec{SessionID: claudeSID, Until: []string{CondTurnEnd}})
	if armed.TargetState != sessionevents.TurnRunning {
		t.Fatalf("target state = %q, want running", armed.TargetState)
	}
	appendLines(t, file, tDur)
	res := waitShort(t, w, armed.Cursor, Limits{})
	if got := outcomes(res); len(got) != 1 || got[0] != "completed" || res.Events[0].TurnID != "u1" {
		t.Fatalf("result = %+v", res)
	}
}

func TestClaudeTwoTurnsBetweenPolls(t *testing.T) {
	file, _ := setupHomes(t)
	w := newWatcher(t)
	appendLines(t, file, tPrompt, tText, tDur)
	armed := arm(t, w, Spec{SessionID: claudeSID, Until: []string{CondTurnEnd}})
	appendLines(t, file, tPrompt2, tText2, tDur2, tPrompt, tText, tDur)
	res := waitShort(t, w, armed.Cursor, Limits{})
	if got := outcomes(res); len(got) != 2 {
		t.Fatalf("outcomes = %v, want two", got)
	}
}

func TestClaudeOutcomeSurvivesCursorBoundary(t *testing.T) {
	file, _ := setupHomes(t)
	w := newWatcher(t)
	appendLines(t, file, tPrompt, tText, tDur)
	armed := arm(t, w, Spec{SessionID: claudeSID, Until: []string{CondTurnEnd}})
	appendLines(t, file, tPrompt2, tAPIError, tSched)
	mid := waitShort(t, w, armed.Cursor, Limits{})
	if mid.Status != StatusWaiting {
		t.Fatalf("mid = %+v", mid)
	}
	appendLines(t, file, tDur2)
	res := waitShort(t, w, mid.Cursor, Limits{})
	if got := outcomes(res); len(got) != 1 || got[0] != "failed" {
		t.Fatalf("outcomes = %v, want failed", got)
	}
}

func TestClaudePhraseAndMessage(t *testing.T) {
	file, _ := setupHomes(t)
	w := newWatcher(t)
	appendLines(t, file, tPrompt)
	armed := arm(t, w, Spec{SessionID: claudeSID, Until: []string{CondPhrase, CondMessage}, Phrase: "say-the-word"})
	appendLines(t, file, `{"type":"user","uuid":"q","message":{"role":"user","content":"please say-the-word"}}`, tText2, tText)
	res := waitShort(t, w, armed.Cursor, Limits{})
	if len(res.Events) != 2 {
		t.Fatalf("events = %+v", res.Events)
	}
	if strings.Join(res.Events[0].Matched, ",") != "message" || strings.Join(res.Events[1].Matched, ",") != "message,phrase" {
		t.Fatalf("matched = %v / %v", res.Events[0].Matched, res.Events[1].Matched)
	}
	if res.Events[1].Excerpt != "Done. say-the-word" {
		t.Fatalf("excerpt = %q", res.Events[1].Excerpt)
	}
}

func TestMintPhrase(t *testing.T) {
	file, _ := setupHomes(t)
	w := newWatcher(t)
	appendLines(t, file, tPrompt)
	armed := arm(t, w, Spec{SessionID: claudeSID, Until: []string{CondPhrase}, MintPhrase: true})
	if len(strings.Split(armed.Phrase, "-")) != 4 {
		t.Fatalf("minted phrase = %q", armed.Phrase)
	}
}

func TestPartialLineWaitsForNewline(t *testing.T) {
	file, _ := setupHomes(t)
	w := newWatcher(t)
	appendLines(t, file, tPrompt, tText)
	armed := arm(t, w, Spec{SessionID: claudeSID, Until: []string{CondTurnEnd}})
	appendRaw(t, file, tDur[:20])
	if res := waitShort(t, w, armed.Cursor, Limits{}); res.Status != StatusWaiting {
		t.Fatalf("partial line produced %+v", res)
	}
	appendRaw(t, file, tDur[20:]+"\n")
	if res := waitShort(t, w, armed.Cursor, Limits{}); len(outcomes(res)) != 1 {
		t.Fatalf("completed line: %+v", res)
	}
}

func TestBatchLimitKeepsRecordsWhole(t *testing.T) {
	file, _ := setupHomes(t)
	w := newWatcher(t)
	appendLines(t, file, tPrompt)
	armed := arm(t, w, Spec{SessionID: claudeSID, Until: []string{CondMessage, CondTurnEnd}})
	appendLines(t, file, tText, tDur, tPrompt2, tText2, tDur2)
	var kinds []string
	cur := armed.Cursor
	for i := 0; i < 10 && len(kinds) < 4; i++ {
		res := waitShort(t, w, cur, Limits{MaxEvents: 1})
		if len(res.Events) > 1 {
			t.Fatalf("batch of %d with MaxEvents 1", len(res.Events))
		}
		for _, e := range res.Events {
			kinds = append(kinds, e.Kind)
		}
		cur = res.Cursor
	}
	if strings.Join(kinds, ",") != "message,turn_ended,message,turn_ended" {
		t.Fatalf("sequence = %v", kinds)
	}
}

func TestContinuityGaps(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(t *testing.T, file string)
		reason string
	}{
		{"truncated", func(t *testing.T, file string) {
			// Back to the end of the first record.
			if err := os.Truncate(file, int64(len(tPrompt)+1)); err != nil {
				t.Fatal(err)
			}
		}, GapTruncated},
		{"rewritten", func(t *testing.T, file string) {
			// Same length, different last record.
			data, _ := os.ReadFile(file)
			s := strings.Replace(string(data), `"durationMs":59000`, `"durationMs":59001`, 1)
			if err := os.WriteFile(file, []byte(s), 0o644); err != nil {
				t.Fatal(err)
			}
		}, GapRewritten},
		{"replaced", func(t *testing.T, file string) {
			data, _ := os.ReadFile(file)
			tmp := file + ".tmp"
			if err := os.WriteFile(tmp, data, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(tmp, file); err != nil {
				t.Fatal(err)
			}
		}, GapReplaced},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			file, _ := setupHomes(t)
			w := newWatcher(t)
			appendLines(t, file, tPrompt, tText, tDur)
			armed := arm(t, w, Spec{SessionID: claudeSID, Until: []string{CondTurnEnd}})
			tc.mutate(t, file)
			res := waitShort(t, w, armed.Cursor, Limits{})
			if res.Status != StatusGap || res.GapReason != tc.reason {
				t.Fatalf("result = %+v, want gap %s", res, tc.reason)
			}
			// The fresh cursor continues normally.
			appendLines(t, file, tPrompt2, tText2, tDur2)
			if next := waitShort(t, w, res.Cursor, Limits{}); len(outcomes(next)) != 1 {
				t.Fatalf("after gap: %+v", next)
			}
		})
	}
}

func TestRewriteDuringReadIsCaught(t *testing.T) {
	file, _ := setupHomes(t)
	w := newWatcher(t)
	appendLines(t, file, tPrompt, tText, tDur)
	armed := arm(t, w, Spec{SessionID: claudeSID, Until: []string{CondTurnEnd}})
	appendLines(t, file, tPrompt2, tText2, tDur2)
	afterFirstCheck = func(path string) {
		afterFirstCheck = nil
		data, _ := os.ReadFile(path)
		s := strings.Replace(string(data), `"durationMs":59000}`, `"durationMs":59009}`, 1)
		f, err := os.OpenFile(path, os.O_WRONLY, 0)
		if err != nil {
			t.Error(err)
			return
		}
		_, _ = f.WriteAt([]byte(s), 0)
		_ = f.Close()
	}
	defer func() { afterFirstCheck = nil }()
	res := waitShort(t, w, armed.Cursor, Limits{})
	if res.Status != StatusGap || res.GapReason != GapRewritten || len(res.Events) != 0 {
		t.Fatalf("result = %+v, want rewritten gap with no events", res)
	}
}

func TestCorruptRecordGap(t *testing.T) {
	file, _ := setupHomes(t)
	w := newWatcher(t)
	appendLines(t, file, tPrompt, tText, tDur)
	armed := arm(t, w, Spec{SessionID: claudeSID, Until: []string{CondTurnEnd}})
	appendLines(t, file, `{"type": not json`, tPrompt2, tText2, tDur2)
	res := waitShort(t, w, armed.Cursor, Limits{})
	if res.Status != StatusGap || res.GapReason != GapCorrupt {
		t.Fatalf("result = %+v", res)
	}
	if next := waitShort(t, w, res.Cursor, Limits{}); len(outcomes(next)) != 1 {
		t.Fatalf("after corrupt record: %+v", next)
	}
}

func TestHeadlessClaudeRejectsTurnEnd(t *testing.T) {
	file, _ := setupHomes(t)
	w := newWatcher(t)
	appendLines(t, file, tHeadless, tText)
	if _, err := w.Arm(context.Background(), Spec{SessionID: claudeSID, Until: []string{CondTurnEnd}}); codeOf(err) != CodeUnsupportedFormat {
		t.Fatalf("err = %v, want unsupported_format", err)
	}
	if _, err := w.Arm(context.Background(), Spec{SessionID: claudeSID, Until: []string{CondMessage}}); err != nil {
		t.Fatalf("message watch on headless session: %v", err)
	}
}

func TestBackwardScanLimitGivesPartial(t *testing.T) {
	file, _ := setupHomes(t)
	w := newWatcher(t)
	old := scanLimit
	scanLimit = 600
	defer func() { scanLimit = old }()
	lines := []string{tPrompt, tText, tDur, tPrompt2}
	for i := 0; i < 10; i++ {
		lines = append(lines, tText2)
	}
	appendLines(t, file, lines...)
	armed := arm(t, w, Spec{SessionID: claudeSID, Until: []string{CondTurnEnd}})
	if armed.TargetState != sessionevents.TurnUnknown {
		t.Fatalf("target state = %q, want unknown", armed.TargetState)
	}
	appendLines(t, file, tDur2)
	res := waitShort(t, w, armed.Cursor, Limits{})
	if len(res.Events) != 1 || !res.Events[0].Partial {
		t.Fatalf("result = %+v, want one partial end", res)
	}
}

// Codex.

func codexMeta(thread string, ts string) string {
	return `{"timestamp":"` + ts + `","ordinal":0,"type":"session_meta","payload":{"id":"` + thread + `","timestamp":"` + ts + `"}}`
}

func codexRec(ordinal int, ts, payload string) string {
	return `{"timestamp":"` + ts + `","ordinal":` + itoa(ordinal) + `,"type":"event_msg","payload":` + payload + `}`
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestCodexTurnEndAndFailure(t *testing.T) {
	_, dir := setupHomes(t)
	w := newWatcher(t)
	path := filepath.Join(dir, "rollout-2026-10-30T09-44-51-"+codexThread+".jsonl")
	appendLines(t, path, codexMeta(codexThread, "2026-10-30T09:44:51Z"),
		codexRec(1, "2026-10-30T09:45:00Z", `{"type":"task_started","turn_id":"t1"}`))
	armed := arm(t, w, Spec{SessionID: codexThread, Until: []string{CondTurnEnd}})
	if armed.TargetState != sessionevents.TurnRunning {
		t.Fatalf("target state = %q", armed.TargetState)
	}
	appendLines(t, path,
		codexRec(2, "2026-10-30T09:46:00Z", `{"type":"task_complete","turn_id":"t1","last_agent_message":"all done"}`),
		codexRec(3, "2026-10-30T09:47:00Z", `{"type":"task_started","turn_id":"t2"}`),
		codexRec(4, "2026-10-30T09:48:00Z", `{"type":"task_complete","turn_id":"t2","error":{"message":"at capacity","codex_error_info":"server_overloaded"}}`))
	res := waitShort(t, w, armed.Cursor, Limits{})
	if got := outcomes(res); strings.Join(got, ",") != "completed,failed" {
		t.Fatalf("outcomes = %v", got)
	}
	if res.Events[0].Excerpt != "all done" || res.Events[1].Reason != "server_overloaded" {
		t.Fatalf("events = %+v", res.Events)
	}
	wantID := "codex:rollout-2026-10-30T09-44-51-" + codexThread + ":0:2:0"
	if res.Events[0].EventID != wantID {
		t.Fatalf("event id = %q, want %q", res.Events[0].EventID, wantID)
	}
}

func TestCodexRolloutSwitchAndBack(t *testing.T) {
	_, dir := setupHomes(t)
	w := newWatcher(t)
	orig := filepath.Join(dir, "rollout-2026-09-26T09-41-27-"+codexThread+".jsonl")
	appendLines(t, orig, codexMeta(codexThread, "2026-09-26T09:41:27Z"),
		codexRec(1, "2026-10-30T09:00:00Z", `{"type":"task_complete","turn_id":"t0"}`))
	armed := arm(t, w, Spec{SessionID: codexThread, Until: []string{CondTurnEnd}})

	repl := filepath.Join(dir, "rollout-2026-10-04T17-18-21-"+codexThread+"_"+codexRepl+".jsonl")
	appendLines(t, repl, codexMeta(codexThread, "2026-10-04T17:18:21Z"),
		codexRec(1, "2026-10-30T10:00:00Z", `{"type":"task_started","turn_id":"r1"}`))
	res := waitShort(t, w, armed.Cursor, Limits{})
	if res.Status != StatusGap || res.GapReason != GapRolloutChanged || !strings.HasSuffix(res.Source, codexRepl) {
		t.Fatalf("result = %+v, want rollout_changed to the replacement", res)
	}
	if res.TargetState != sessionevents.TurnRunning {
		t.Fatalf("target state after switch = %q", res.TargetState)
	}
	// Codex resumes the original rollout, which becomes active again.
	appendLines(t, orig, codexRec(2, "2026-10-30T11:00:00Z", `{"type":"task_started","turn_id":"o2"}`))
	back := waitShort(t, w, res.Cursor, Limits{})
	if back.Status != StatusGap || back.GapReason != GapRolloutChanged || strings.HasSuffix(back.Source, codexRepl) {
		t.Fatalf("result = %+v, want rollout_changed back to the original", back)
	}
	appendLines(t, orig, codexRec(3, "2026-10-30T11:05:00Z", `{"type":"task_complete","turn_id":"o2"}`))
	done := waitShort(t, w, back.Cursor, Limits{})
	if got := outcomes(done); len(got) != 1 || done.Events[0].TurnID != "o2" {
		t.Fatalf("after switching back: %+v", done)
	}
}

func TestCodexReusedOrdinalsGetNewIDs(t *testing.T) {
	_, dir := setupHomes(t)
	w := newWatcher(t)
	path := filepath.Join(dir, "rollout-2026-10-30T09-44-51-"+codexThread+".jsonl")
	meta := codexMeta(codexThread, "2026-10-30T09:44:51Z")
	appendLines(t, path, meta)
	armed := arm(t, w, Spec{SessionID: codexThread, Until: []string{CondTurnEnd}})
	appendLines(t, path, codexRec(1, "2026-10-30T09:45:00Z", `{"type":"task_complete","turn_id":"a"}`))
	first := waitShort(t, w, armed.Cursor, Limits{})
	if len(first.Events) != 1 {
		t.Fatalf("first = %+v", first)
	}
	// Truncate back to the metadata line and write a different record with
	// the same ordinal.
	if err := os.Truncate(path, int64(len(meta)+1)); err != nil {
		t.Fatal(err)
	}
	gap := waitShort(t, w, first.Cursor, Limits{})
	if gap.Status != StatusGap || gap.GapReason != GapTruncated {
		t.Fatalf("gap = %+v", gap)
	}
	appendLines(t, path, codexRec(1, "2026-10-30T09:50:00Z", `{"type":"task_complete","turn_id":"b"}`))
	second := waitShort(t, w, gap.Cursor, Limits{})
	if len(second.Events) != 1 || second.Events[0].EventID == first.Events[0].EventID {
		t.Fatalf("reused ordinal kept id %q: %+v", first.Events[0].EventID, second)
	}
}

func TestCodexLegacyRolloutUnsupported(t *testing.T) {
	_, dir := setupHomes(t)
	w := newWatcher(t)
	path := filepath.Join(dir, "rollout-2025-01-01T00-00-00-"+codexThread+".jsonl")
	appendLines(t, path,
		`{"timestamp":"2025-01-01T00:00:00Z","type":"session_meta","payload":{"id":"`+codexThread+`"}}`,
		`{"timestamp":"2025-01-01T00:00:01Z","type":"event_msg","payload":{"type":"task_started","turn_id":"x"}}`)
	if _, err := w.Arm(context.Background(), Spec{SessionID: codexThread, Until: []string{CondTurnEnd}}); codeOf(err) != CodeUnsupportedFormat {
		t.Fatalf("err = %v, want unsupported_format", err)
	}
}

func TestTranscriptArmValidation(t *testing.T) {
	file, _ := setupHomes(t)
	w := newWatcher(t)
	appendLines(t, file, tPrompt)
	cases := []struct {
		spec Spec
		code string
	}{
		{Spec{Until: []string{CondTurnEnd}}, CodeInvalidArgument},
		{Spec{SessionID: claudeSID, Until: []string{CondPhrase}}, CodeInvalidArgument},
		{Spec{SessionID: claudeSID, Until: []string{CondPhrase}, Phrase: "x", MintPhrase: true}, CodeInvalidArgument},
		{Spec{SessionID: claudeSID, Until: []string{CondTurnEnd}, Phrase: "x"}, CodeInvalidArgument},
		{Spec{SessionID: claudeSID, Until: []string{CondTurnEnd}, Channel: "c"}, CodeInvalidArgument},
		{Spec{SessionID: "99999999-2222-3333-4444-555555555555", Until: []string{CondTurnEnd}}, CodeNotFound},
	}
	for i, tc := range cases {
		if _, err := w.Arm(context.Background(), tc.spec); codeOf(err) != tc.code {
			t.Errorf("case %d: err = %v, want %s", i, err, tc.code)
		}
	}
}
