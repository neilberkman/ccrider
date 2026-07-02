package pisessions

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseFileBasicMetadata(t *testing.T) {
	session, err := ParseFile("testdata/basic.jsonl")
	if err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}

	if session.SessionID != "019edafc-796a-79ce-a42b-f1d986bd3e8c" {
		t.Fatalf("SessionID = %q", session.SessionID)
	}
	if session.FilePath != "testdata/basic.jsonl" {
		t.Fatalf("FilePath = %q", session.FilePath)
	}
	if session.FileSize == 0 {
		t.Fatal("FileSize is zero")
	}
	if session.FileMtime.IsZero() {
		t.Fatal("FileMtime is zero")
	}
}

func TestParseFileMessages(t *testing.T) {
	session, err := ParseFile("testdata/basic.jsonl")
	if err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}

	if len(session.Messages) != 2 {
		t.Fatalf("len(Messages) = %d, want 2", len(session.Messages))
	}

	m0 := session.Messages[0]
	if m0.Type != "user" || m0.Sender != "human" {
		t.Fatalf("message 0 type/sender = %q/%q, want user/human", m0.Type, m0.Sender)
	}
	if m0.TextContent != "Fix the login flow" {
		t.Fatalf("message 0 text = %q", m0.TextContent)
	}
	if m0.CWD != "/tmp/pi-demo" {
		t.Fatalf("message 0 CWD = %q", m0.CWD)
	}
	if m0.UUID == "" || m0.ParentUUID != "" {
		t.Fatalf("message 0 uuid/parent = %q/%q", m0.UUID, m0.ParentUUID)
	}

	m1 := session.Messages[1]
	if m1.Type != "assistant" || m1.Sender != "assistant" {
		t.Fatalf("message 1 type/sender = %q/%q, want assistant/assistant", m1.Type, m1.Sender)
	}
	if m1.ParentUUID != "" {
		t.Fatalf("message 1 ParentUUID = %q, want empty", m1.ParentUUID)
	}
	want := "I'll inspect the auth handler.\n\nThen I'll add a regression test."
	if m1.TextContent != want {
		t.Fatalf("message 1 text = %q, want %q", m1.TextContent, want)
	}
	if m1.Version != "openai-codex/gpt-5.5" {
		t.Fatalf("message 1 Version = %q, want openai-codex/gpt-5.5", m1.Version)
	}

	if session.Summary != "Fix the login flow" {
		t.Fatalf("Summary = %q", session.Summary)
	}
}

func TestParseFileSkipsCustomEventsAndToolCalls(t *testing.T) {
	session, err := ParseFile("testdata/noise_and_tools.jsonl")
	if err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}

	for _, msg := range session.Messages {
		if strings.Contains(msg.TextContent, "must not index") {
			t.Fatalf("custom event leaked into messages: %#v", msg)
		}
		if strings.Contains(msg.TextContent, "hidden tool call") {
			t.Fatalf("tool-call payload leaked into messages: %#v", msg)
		}
	}
}

func TestParseFileToolResultMessage(t *testing.T) {
	session, err := ParseFile("testdata/noise_and_tools.jsonl")
	if err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}

	if len(session.Messages) != 3 {
		t.Fatalf("len(Messages) = %d, want 3", len(session.Messages))
	}
	tool := session.Messages[1]
	if tool.Type != "tool" || tool.Sender != "tool" {
		t.Fatalf("tool type/sender = %q/%q, want tool/tool", tool.Type, tool.Sender)
	}
	if tool.TextContent != "frobnicator passed" {
		t.Fatalf("tool text = %q", tool.TextContent)
	}
}

func TestParseFileUsesSessionCWDForProjectPath(t *testing.T) {
	session, err := ParseFile("testdata/noise_and_tools.jsonl")
	if err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}

	for i, msg := range session.Messages {
		if msg.CWD != "/tmp/pi-noise" {
			t.Fatalf("message %d CWD = %q, want /tmp/pi-noise", i, msg.CWD)
		}
	}
}

func TestParseFileRedactedRealFixture(t *testing.T) {
	session, err := ParseFile("testdata/redacted_real.jsonl")
	if err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}

	if session.SessionID != "real-session" {
		t.Fatalf("SessionID = %q", session.SessionID)
	}
	if len(session.Messages) != 3 {
		t.Fatalf("len(Messages) = %d, want user, assistant text, and toolResult messages", len(session.Messages))
	}
	msg := session.Messages[0]
	if msg.Type != "user" || msg.Sender != "human" {
		t.Fatalf("message type/sender = %q/%q, want user/human", msg.Type, msg.Sender)
	}
	if msg.CWD != "/redacted/pi-project" {
		t.Fatalf("CWD = %q, want /redacted/pi-project", msg.CWD)
	}
	if msg.TextContent != "redacted user text from real fixture" {
		t.Fatalf("TextContent = %q", msg.TextContent)
	}
	if msg.ParentUUID != "" || msg.UUID == "evt-user" {
		t.Fatalf("raw Pi ids leaked into UUID fields: %#v", msg)
	}
	assistant := session.Messages[1]
	if assistant.Version == "" {
		t.Fatalf("assistant Version is empty; message payload provider/model was not captured")
	}
	if assistant.Version != "openai-codex/gpt-5.5" {
		t.Fatalf("assistant Version = %q, want openai-codex/gpt-5.5", assistant.Version)
	}
	tool := session.Messages[2]
	if tool.Type != "tool" || tool.Sender != "tool" {
		t.Fatalf("tool type/sender = %q/%q, want tool/tool", tool.Type, tool.Sender)
	}
	if tool.TextContent != "redacted camel-case tool result text" {
		t.Fatalf("tool TextContent = %q", tool.TextContent)
	}
}

func TestParseFileDeterministicUUIDs(t *testing.T) {
	s1, err := ParseFile("testdata/basic.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	s2, err := ParseFile("testdata/basic.jsonl")
	if err != nil {
		t.Fatal(err)
	}

	for i := range s1.Messages {
		if s1.Messages[i].UUID == "" {
			t.Fatalf("message %d UUID is empty", i)
		}
		if s1.Messages[i].UUID != s2.Messages[i].UUID {
			t.Fatalf("message %d UUID mismatch: %q != %q", i, s1.Messages[i].UUID, s2.Messages[i].UUID)
		}
	}
}

func TestParseFileUUIDIncludesFilenameStem(t *testing.T) {
	dir := t.TempDir()
	body := `{"type":"session","id":"same-metadata-id","cwd":"/tmp/pi"}
{"type":"message","id":"same-event-id","message":{"role":"user","timestamp":1781783280000,"content":[{"type":"text","text":"same text"}]}}
`
	path1 := filepath.Join(dir, "2026-06-18T13-47-19-786Z_same-metadata-id.jsonl")
	path2 := filepath.Join(dir, "copy_same-metadata-id.jsonl")
	if err := os.WriteFile(path1, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path2, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}

	s1, err := ParseFile(path1)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := ParseFile(path2)
	if err != nil {
		t.Fatal(err)
	}

	if s1.Messages[0].UUID == s2.Messages[0].UUID {
		t.Fatalf("UUID collision across filename stems: %q", s1.Messages[0].UUID)
	}
}

func TestParseFileContentStoresMessagePayloadOnly(t *testing.T) {
	session, err := ParseFile("testdata/basic.jsonl")
	if err != nil {
		t.Fatal(err)
	}

	content := string(session.Messages[0].Content)
	if strings.Contains(content, `"type":"message"`) {
		t.Fatalf("Content contains raw line envelope: %s", content)
	}
	var payload messagePayload
	if err := json.Unmarshal(session.Messages[0].Content, &payload); err != nil {
		t.Fatalf("Content is not a message payload: %v", err)
	}
	if payload.Role != "user" {
		t.Fatalf("Content role = %q, want user", payload.Role)
	}
}

func TestParseFileTimestampFallbacks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fallback.jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"session","id":"fallback","cwd":"/tmp/pi"}
{"type":"message","id":"u1","timestamp":"not-rfc3339","message":{"role":"user","timestamp":1781783280000,"content":[{"type":"text","text":"use payload timestamp"}]}}
`), 0644); err != nil {
		t.Fatal(err)
	}

	session, err := ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := time.UnixMilli(1781783280000).UTC()
	if got := session.Messages[0].Timestamp; !got.Equal(want) {
		t.Fatalf("Timestamp = %s, want %s", got, want)
	}
}
