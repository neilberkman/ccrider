package mcp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/neilberkman/ccrider/internal/core/db"
	"github.com/neilberkman/ccrider/internal/core/watch"
)

func callTool(t *testing.T, h func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error), args map[string]any) (*mcp.CallToolResult, string) {
	t.Helper()
	var req mcp.CallToolRequest
	req.Params.Arguments = args
	res, err := h(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	text := res.Content[0].(mcp.TextContent).Text
	return res, text
}

func newWatchHandlers(t *testing.T) (*watch.Watcher, waitSlots) {
	t.Helper()
	database, err := db.New(filepath.Join(t.TempDir(), "watch.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return &watch.Watcher{DB: database, PollInterval: 10 * time.Millisecond}, make(waitSlots, maxConcurrentWaits)
}

func TestWaitForSessionSignalFlow(t *testing.T) {
	watcher, slots := newWatchHandlers(t)
	wait := makeWaitForSessionHandler(watcher, slots)
	sendSig := makeSendSessionSignalHandler(watcher)

	_, text := callTool(t, wait, map[string]any{"until": []any{"signal"}, "mint_channel": true})
	var armed WaitForSessionResponse
	if err := json.Unmarshal([]byte(text), &armed); err != nil {
		t.Fatal(err)
	}
	if armed.Status != "armed" || armed.Channel == "" || armed.Cursor == "" || !strings.Contains(armed.Instruction, armed.Channel) {
		t.Fatalf("armed = %+v", armed)
	}

	res, text := callTool(t, sendSig, map[string]any{"channel": armed.Channel, "body": "build green", "idempotency_key": "k1"})
	if res.IsError {
		t.Fatalf("send: %s", text)
	}
	_, text = callTool(t, sendSig, map[string]any{"channel": armed.Channel, "body": "build green", "idempotency_key": "k1"})
	var sent SendSessionSignalResponse
	_ = json.Unmarshal([]byte(text), &sent)
	if !sent.Duplicate {
		t.Fatalf("resend = %s, want duplicate", text)
	}
	res, text = callTool(t, sendSig, map[string]any{"channel": armed.Channel, "body": "build red", "idempotency_key": "k1"})
	if !res.IsError || !strings.Contains(text, "idempotency_conflict") {
		t.Fatalf("conflicting resend = %s", text)
	}

	_, text = callTool(t, wait, map[string]any{"cursor": armed.Cursor, "max_wait_seconds": "2"})
	var got WaitForSessionResponse
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatal(err)
	}
	if got.Status != "event" || len(got.Events) != 1 || got.Events[0].Signal.Body != "build green" {
		t.Fatalf("wait = %s", text)
	}
}

func TestWaitForSessionBusyAndValidation(t *testing.T) {
	watcher, slots := newWatchHandlers(t)
	wait := makeWaitForSessionHandler(watcher, slots)
	_, text := callTool(t, wait, map[string]any{"until": []any{"signal"}, "channel": "c"})
	var armed WaitForSessionResponse
	_ = json.Unmarshal([]byte(text), &armed)

	for i := 0; i < maxConcurrentWaits; i++ {
		if !slots.tryAcquire() {
			t.Fatal("slot unavailable")
		}
	}
	_, text = callTool(t, wait, map[string]any{"cursor": armed.Cursor})
	var busy WaitForSessionResponse
	_ = json.Unmarshal([]byte(text), &busy)
	if busy.Status != "busy" || busy.Cursor != armed.Cursor {
		t.Fatalf("over capacity = %s", text)
	}
	for i := 0; i < maxConcurrentWaits; i++ {
		slots.release()
	}

	for _, bad := range []any{0, 601, -5} {
		res, text := callTool(t, wait, map[string]any{"cursor": armed.Cursor, "max_wait_seconds": bad})
		if !res.IsError || !strings.Contains(text, "invalid_argument") {
			t.Errorf("max_wait_seconds=%v: %s", bad, text)
		}
	}
	res, text := callTool(t, wait, map[string]any{"until": []any{"turn_end"}, "session_id": "abc"})
	if !res.IsError || !strings.Contains(text, "unsupported_condition") {
		t.Fatalf("turn_end arm = %s", text)
	}
	res, text = callTool(t, wait, map[string]any{"cursor": armed.Cursor, "channel": "other"})
	if !res.IsError || !strings.Contains(text, "cursor_mismatch") {
		t.Fatalf("mismatch = %s", text)
	}
}

func TestWaitForSessionTimesOutWithCursor(t *testing.T) {
	watcher, slots := newWatchHandlers(t)
	wait := makeWaitForSessionHandler(watcher, slots)
	_, text := callTool(t, wait, map[string]any{"until": []any{"signal"}, "channel": "quiet"})
	var armed WaitForSessionResponse
	_ = json.Unmarshal([]byte(text), &armed)
	start := time.Now()
	_, text = callTool(t, wait, map[string]any{"cursor": armed.Cursor, "max_wait_seconds": 1})
	var got WaitForSessionResponse
	_ = json.Unmarshal([]byte(text), &got)
	if got.Status != "waiting" || got.Cursor != armed.Cursor {
		t.Fatalf("quiet wait = %s", text)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("1 s wait took %v; it must return within max_wait_seconds", elapsed)
	}
}

// Field limits keep every result under the server-wide response cap, even
// for bodies whose characters expand when JSON-encoded.
func TestWaitForSessionWorstCaseFitsResponseCap(t *testing.T) {
	watcher, slots := newWatchHandlers(t)
	wait := makeWaitForSessionHandler(watcher, slots)
	sendSig := makeSendSessionSignalHandler(watcher)
	channel := strings.Repeat(`"`, db.MaxSignalChannelRunes)
	_, text := callTool(t, wait, map[string]any{"until": []any{"signal"}, "channel": channel})
	var armed WaitForSessionResponse
	if err := json.Unmarshal([]byte(text), &armed); err != nil {
		t.Fatalf("%v: %s", err, text)
	}
	bodies := []string{
		strings.Repeat("<", db.MaxSignalBodyBytes),
		strings.Repeat(`"`, db.MaxSignalBodyBytes/2),
		strings.Repeat("\x01", db.MaxSignalBodyBytes/6),
	}
	sender := strings.Repeat(`\`, db.MaxSignalSenderBytes/2)
	for _, body := range bodies {
		res, text := callTool(t, sendSig, map[string]any{"channel": channel, "body": body, "from_session_id": sender})
		if res.IsError {
			t.Fatalf("send: %s", text)
		}
	}
	cur := armed.Cursor
	for received := 0; received < len(bodies); {
		res, text := callTool(t, wait, map[string]any{"cursor": cur, "max_wait_seconds": 1})
		if res.IsError {
			t.Fatalf("wait: %s", text)
		}
		if len(text) > maxResponseBytes() {
			t.Fatalf("response of %d bytes exceeds cap %d", len(text), maxResponseBytes())
		}
		var got WaitForSessionResponse
		_ = json.Unmarshal([]byte(text), &got)
		if len(got.Events) == 0 {
			t.Fatalf("no progress: %s", text[:200])
		}
		received += len(got.Events)
		cur = got.Cursor
	}
}

func TestSendSessionSignalValidation(t *testing.T) {
	watcher, _ := newWatchHandlers(t)
	sendSig := makeSendSessionSignalHandler(watcher)
	res, text := callTool(t, sendSig, map[string]any{"channel": "c"})
	if !res.IsError || !strings.Contains(text, "invalid_body") {
		t.Fatalf("missing body = %s", text)
	}
	res, text = callTool(t, sendSig, map[string]any{"body": "x"})
	if !res.IsError || !strings.Contains(text, "invalid_argument") {
		t.Fatalf("missing channel = %s", text)
	}
	res, text = callTool(t, sendSig, map[string]any{"channel": "c", "body": ""})
	if res.IsError {
		t.Fatalf("explicit empty body rejected: %s", text)
	}
	res, text = callTool(t, sendSig, map[string]any{"channel": "c", "body": strings.Repeat("\x01", db.MaxSignalBodyBytes)})
	if !res.IsError || !strings.Contains(text, "invalid_body") {
		t.Fatalf("oversized body = %s", text)
	}
	res, text = callTool(t, sendSig, map[string]any{"channel": "c", "body": "x", "from_session_id": strings.Repeat("s", 500)})
	if !res.IsError || !strings.Contains(text, "invalid_argument") {
		t.Fatalf("oversized sender = %s", text)
	}
}
