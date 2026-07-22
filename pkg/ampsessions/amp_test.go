package ampsessions

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestClientListThreadsPaginatesAndDeduplicates(t *testing.T) {
	var calls [][]string
	client := newClient(func(_ context.Context, args ...string) ([]byte, error) {
		calls = append(calls, append([]string(nil), args...))
		offset := args[len(args)-1]
		switch offset {
		case "0":
			return []byte(`[
				{"id":"T-one","updated":"2026-07-22T10:00:00Z","messageCount":2},
				{"id":"T-two","updated":"2026-07-22T11:00:00Z","messageCount":4}
			]`), nil
		case "2":
			return []byte(`[
				{"id":"T-two","updated":"2026-07-22T11:00:00Z","messageCount":4},
				{"id":"T-three","updated":"2026-07-22T12:00:00Z","messageCount":6}
			]`), nil
		case "4":
			return []byte(`[]`), nil
		default:
			t.Fatalf("unexpected offset %s", offset)
			return nil, nil
		}
	}, 2)

	refs, err := client.ListThreads()
	if err != nil {
		t.Fatalf("ListThreads() error = %v", err)
	}
	if len(refs) != 3 {
		t.Fatalf("ListThreads() returned %d refs, want 3", len(refs))
	}
	ids := []string{refs[0].ID, refs[1].ID, refs[2].ID}
	if want := []string{"T-one", "T-two", "T-three"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("thread ids = %v, want %v", ids, want)
	}
	if len(calls) != 3 {
		t.Fatalf("amp calls = %d, want 3", len(calls))
	}
	if got := strings.Join(calls[0], " "); got != "threads list --json --include-archived --limit 2 --offset 0" {
		t.Fatalf("first amp call = %q", got)
	}
}

func TestThreadRevisionChangesWithRemoteMetadata(t *testing.T) {
	messageCount := 2
	base := threadListItem{ID: "T-one", Updated: "2026-07-22T10:00:00Z", MessageCount: &messageCount}
	baseRevision, err := base.revision()
	if err != nil {
		t.Fatal(err)
	}
	stableRevision, err := base.revision()
	if err != nil {
		t.Fatal(err)
	}
	if baseRevision != stableRevision {
		t.Fatal("revision is not stable")
	}
	changedTime := base
	changedTime.Updated = "2026-07-22T10:01:00Z"
	changedTimeRevision, err := changedTime.revision()
	if err != nil {
		t.Fatal(err)
	}
	if baseRevision == changedTimeRevision {
		t.Fatal("updated timestamp did not change revision")
	}
	changedCount := base
	changedMessageCount := *base.MessageCount + 1
	changedCount.MessageCount = &changedMessageCount
	changedCountRevision, err := changedCount.revision()
	if err != nil {
		t.Fatal(err)
	}
	if baseRevision == changedCountRevision {
		t.Fatal("message count did not change revision")
	}
}

func TestClientListThreadsReturnsActionableErrors(t *testing.T) {
	tests := []struct {
		name    string
		run     runFunc
		wantErr string
	}{
		{
			name: "command failure",
			run: func(context.Context, ...string) ([]byte, error) {
				return nil, errors.New("not authenticated")
			},
			wantErr: "list Amp threads: not authenticated",
		},
		{
			name: "invalid json",
			run: func(context.Context, ...string) ([]byte, error) {
				return []byte(`not-json`), nil
			},
			wantErr: "parse Amp thread list at offset 0",
		},
		{
			name: "missing id",
			run: func(context.Context, ...string) ([]byte, error) {
				return []byte(`[{}]`), nil
			},
			wantErr: "thread id is missing",
		},
		{
			name: "missing revision metadata",
			run: func(context.Context, ...string) ([]byte, error) {
				return []byte(`[{"id":"T-one","messageCount":1}]`), nil
			},
			wantErr: "has no updated timestamp",
		},
		{
			name: "missing message count",
			run: func(context.Context, ...string) ([]byte, error) {
				return []byte(`[{"id":"T-one","updated":"2026-07-22T10:00:00Z"}]`), nil
			},
			wantErr: "has no message count",
		},
		{
			name: "negative message count",
			run: func(context.Context, ...string) ([]byte, error) {
				return []byte(`[{"id":"T-one","updated":"2026-07-22T10:00:00Z","messageCount":-1}]`), nil
			},
			wantErr: "has invalid message count -1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newClient(tt.run, 100)
			_, err := client.ListThreads()
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("ListThreads() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestClientExportThreadRejectsMismatchedID(t *testing.T) {
	client := newClient(func(_ context.Context, args ...string) ([]byte, error) {
		if got := strings.Join(args, " "); got != "threads export T-requested" {
			t.Fatalf("amp args = %q", got)
		}
		return []byte(exportFixture("T-other")), nil
	}, 100)
	_, err := client.ExportThread("T-requested")
	if err == nil || !strings.Contains(err.Error(), `export contained thread id "T-other"`) {
		t.Fatalf("ExportThread() error = %v", err)
	}
}

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
