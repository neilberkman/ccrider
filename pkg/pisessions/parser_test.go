package pisessions

import (
	"testing"
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
	if m1.ParentUUID != m0.UUID {
		t.Fatalf("message 1 ParentUUID = %q, want previous UUID %q", m1.ParentUUID, m0.UUID)
	}
	want := "I'll inspect the auth handler.\n\nThen I'll add a regression test."
	if m1.TextContent != want {
		t.Fatalf("message 1 text = %q, want %q", m1.TextContent, want)
	}

	if session.Summary != "Fix the login flow" {
		t.Fatalf("Summary = %q", session.Summary)
	}
}

func TestParseFileSkipsCustomEvents(t *testing.T) {
	session, err := ParseFile("testdata/noise_and_tools.jsonl")
	if err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}

	for _, msg := range session.Messages {
		if msg.TextContent == "must not index" || msg.TextContent == "must not index either" {
			t.Fatalf("custom event leaked into messages: %#v", msg)
		}
	}
}

func TestParseFileToolResultAsNonConversationMessage(t *testing.T) {
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

func TestParseFileRedactedRealShape(t *testing.T) {
	session, err := ParseFile("testdata/redacted_real_shape.jsonl")
	if err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}

	if session.SessionID != "real-shape-session" {
		t.Fatalf("SessionID = %q", session.SessionID)
	}
	if len(session.Messages) != 2 {
		t.Fatalf("len(Messages) = %d, want user text plus camel-case toolResult indexed", len(session.Messages))
	}
	msg := session.Messages[0]
	if msg.Type != "user" || msg.Sender != "human" {
		t.Fatalf("message type/sender = %q/%q, want user/human", msg.Type, msg.Sender)
	}
	if msg.CWD != "/redacted/pi-project" {
		t.Fatalf("CWD = %q, want /redacted/pi-project", msg.CWD)
	}
	if msg.TextContent != "redacted user text from real-shape fixture" {
		t.Fatalf("TextContent = %q", msg.TextContent)
	}
	if msg.ParentUUID == "evt-custom" || msg.UUID == "evt-user" {
		t.Fatalf("raw Pi ids leaked into normalized UUID fields: %#v", msg)
	}
	tool := session.Messages[1]
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
