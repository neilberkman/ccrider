package ampsessions

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestParseExport(t *testing.T) {
	session, err := Parse([]byte(exportFixture("T-example")))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if session.SessionID != "T-example" || session.ImportID != "T-example" {
		t.Fatalf("session ids = %q/%q, want T-example", session.SessionID, session.ImportID)
	}
	if session.Summary != "Add Amp support" {
		t.Errorf("Summary = %q", session.Summary)
	}
	if session.ProjectPath != "/Users/example/project with space" {
		t.Errorf("ProjectPath = %q", session.ProjectPath)
	}
	if session.FileMtime != time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC) {
		t.Errorf("FileMtime = %v", session.FileMtime)
	}
	if len(session.Messages) != 3 {
		t.Fatalf("messages = %d, want 3 text-bearing messages", len(session.Messages))
	}

	user := session.Messages[0]
	if user.Type != "user" || user.Sender != "human" || user.TextContent != "Please add Amp support." {
		t.Errorf("user message = %#v", user)
	}
	if user.Timestamp != time.UnixMilli(1784721000000).UTC() {
		t.Errorf("user timestamp = %v", user.Timestamp)
	}
	if user.GitBranch != "feat/amp" || user.Version != "0.0.test" {
		t.Errorf("user metadata = branch %q version %q", user.GitBranch, user.Version)
	}

	assistant := session.Messages[1]
	if assistant.TextContent != "I can do that.\nImplemented." {
		t.Errorf("assistant text = %q", assistant.TextContent)
	}
	if assistant.Timestamp != time.UnixMilli(1784721001000).UTC() {
		t.Errorf("assistant timestamp = %v", assistant.Timestamp)
	}
	if assistant.Sequence != 2 {
		t.Errorf("assistant sequence = %d, want original protocol sequence 2", assistant.Sequence)
	}

	stringID := session.Messages[2]
	if stringID.TextContent != "String ids work too." || stringID.Sequence != 5 {
		t.Errorf("string-id message = %#v", stringID)
	}
	if len(user.Content) == 0 || !json.Valid(user.Content) {
		t.Errorf("raw user content was not preserved: %q", user.Content)
	}
}

func TestParseExportUUIDIncludesThreadID(t *testing.T) {
	first, err := Parse([]byte(exportFixture("T-first")))
	if err != nil {
		t.Fatal(err)
	}
	second, err := Parse([]byte(exportFixture("T-second")))
	if err != nil {
		t.Fatal(err)
	}
	if first.Messages[0].UUID == second.Messages[0].UUID {
		t.Fatalf("message UUID collided across threads: %q", first.Messages[0].UUID)
	}
	reparsed, err := Parse([]byte(exportFixture("T-first")))
	if err != nil {
		t.Fatal(err)
	}
	if first.Messages[0].UUID != reparsed.Messages[0].UUID {
		t.Fatal("message UUID is not deterministic")
	}
}

func TestParseExportSummaryFallbackAndUnknownFields(t *testing.T) {
	fixture := `{
		"id":"T-fallback",
		"title":"",
		"futureRootField":{"anything":true},
		"messages":[
			{"role":"future-role","messageId":1,"content":[{"type":"text","text":"ignored"}]},
			{"role":"user","messageId":2,"futureMessageField":true,"content":[
				{"type":"future-block","text":"not indexed","extra":1},
				{"type":"text","text":"Fallback title"}
			]}
		]
	}`
	session, err := Parse([]byte(fixture))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if session.Summary != "Fallback title" {
		t.Errorf("Summary = %q, want fallback", session.Summary)
	}
	if len(session.Messages) != 1 || session.Messages[0].TextContent != "Fallback title" {
		t.Fatalf("Messages = %#v", session.Messages)
	}
}

func TestParseExportRejectsMissingEssentialIDs(t *testing.T) {
	_, err := Parse([]byte(`{"messages":[]}`))
	if err == nil || !strings.Contains(err.Error(), "thread id is missing") {
		t.Fatalf("Parse() error = %v, want missing thread id", err)
	}
}

func TestParseExportUsesSequenceWhenLegacyMessageIDIsMissing(t *testing.T) {
	fixture := `{
		"id":"T-legacy",
		"messages":[
			{"role":"user","content":[{"type":"text","text":"first"}]},
			{"role":"assistant","messageId":null,"content":[{"type":"text","text":"second"}]},
			{"role":"user","content":[{"type":"tool_result"}]}
		]
	}`
	first, err := Parse([]byte(fixture))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	second, err := Parse([]byte(fixture))
	if err != nil {
		t.Fatalf("Parse() second error = %v", err)
	}
	if len(first.Messages) != 2 {
		t.Fatalf("messages = %d, want 2 text messages", len(first.Messages))
	}
	if first.Messages[0].UUID == first.Messages[1].UUID {
		t.Fatal("sequence-derived UUIDs collided")
	}
	if first.Messages[0].UUID != second.Messages[0].UUID || first.Messages[1].UUID != second.Messages[1].UUID {
		t.Fatal("sequence-derived UUIDs are not deterministic")
	}
}

func TestParseExportRejectsMalformedMessageID(t *testing.T) {
	fixture := `{"id":"T-one","messages":[{"role":"user","messageId":{"bad":true},"content":[{"type":"text","text":"hello"}]}]}`
	_, err := Parse([]byte(fixture))
	if err == nil || !strings.Contains(err.Error(), "unsupported message id") {
		t.Fatalf("Parse() error = %v, want unsupported message id", err)
	}
}

func TestParseExportRejectsDuplicateMessageID(t *testing.T) {
	fixture := `{"id":"T-one","messages":[
		{"role":"user","messageId":7,"content":[{"type":"text","text":"first"}]},
		{"role":"assistant","messageId":7,"content":[{"type":"text","text":"second"}]}
	]}`
	_, err := Parse([]byte(fixture))
	if err == nil || !strings.Contains(err.Error(), `duplicate message id "7"`) {
		t.Fatalf("Parse() error = %v, want duplicate message id", err)
	}
}

func TestParseExportIgnoresDuplicateIDOnTextFreeEvent(t *testing.T) {
	fixture := `{"id":"T-one","messages":[
		{"role":"assistant","messageId":7,"content":[{"type":"tool_use","name":"shell_command"}]},
		{"role":"assistant","messageId":7,"content":[{"type":"text","text":"Retained answer"}]}
	]}`
	session, err := Parse([]byte(fixture))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(session.Messages) != 1 || session.Messages[0].TextContent != "Retained answer" {
		t.Fatalf("Messages = %#v, want one retained text message", session.Messages)
	}
}

func TestParseExportRejectsMissingOrNullMessages(t *testing.T) {
	for _, fixture := range []string{
		`{"id":"T-one"}`,
		`{"id":"T-one","messages":null}`,
	} {
		_, err := Parse([]byte(fixture))
		if err == nil || !strings.Contains(err.Error(), "messages are missing or null") {
			t.Fatalf("Parse(%s) error = %v", fixture, err)
		}
	}

	if _, err := Parse([]byte(`{"id":"T-one","messages":[]}`)); err != nil {
		t.Fatalf("Parse() rejected an explicitly empty transcript: %v", err)
	}
}

func TestParseExportRejectsMalformedKnownTextBlock(t *testing.T) {
	fixture := `{
		"id":"T-one",
		"messages":[{"role":"user","messageId":1,"content":[
			{"type":"text","text":{"unexpected":"object"}}
		]}]
	}`
	_, err := Parse([]byte(fixture))
	if err == nil || !strings.Contains(err.Error(), "text block 1") {
		t.Fatalf("Parse() error = %v, want malformed text block error", err)
	}
}

func exportFixture(threadID string) string {
	return `{
		"id":"` + threadID + `",
		"title":"Add Amp support",
		"created":1784720000000,
		"updatedAt":"2026-07-22T12:00:00Z",
		"env":{"initial":{
			"trees":[{"uri":"file:///Users/example/project%20with%20space","repository":{"ref":"refs/heads/feat/amp"}}],
			"platform":{"clientVersion":"0.0.test"}
		}},
		"messages":[
			{"role":"user","messageId":1,"meta":{"sentAt":1784721000000},"content":[
				{"type":"text","text":"Please add Amp support."}
			]},
			{"role":"assistant","messageId":2,"content":[
				{"type":"thinking","thinking":"private reasoning","startTime":1784721001000,"future":true},
				{"type":"text","text":"I can do that.","startTime":1784721002000},
				{"type":"tool_use","name":"shell_command","input":{"command":"go test"}},
				{"type":"text","text":"Implemented.","startTime":1784721003000}
			]},
			{"role":"user","messageId":3,"content":[{"type":"tool_result","run":{"output":"ok"}}]},
			{"role":"assistant","messageId":4,"content":[{"type":"tool_use","name":"finder"}]},
			{"role":"assistant","messageId":"message-five","content":[{"type":"text","text":"String ids work too."}]}
		]
	}`
}
