package codexsessions

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseFile(t *testing.T) {
	session, err := ParseFile("testdata/sample.jsonl")
	if err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}

	if session.SessionID != "019c268a-86db-7022-958a-d18b1c5b99ad" {
		t.Errorf("SessionID = %q, want %q", session.SessionID, "019c268a-86db-7022-958a-d18b1c5b99ad")
	}

	if len(session.Messages) != 4 {
		t.Fatalf("len(Messages) = %d, want 4", len(session.Messages))
	}

	m0 := session.Messages[0]
	if m0.Type != "user" || m0.Sender != "human" {
		t.Errorf("Messages[0] type=%q sender=%q, want user/human", m0.Type, m0.Sender)
	}
	if m0.TextContent != "Fix the bug in the login handler" {
		t.Errorf("Messages[0] text = %q", m0.TextContent)
	}
	if m0.CWD != "/home/testuser/myproject" {
		t.Errorf("Messages[0] CWD = %q, want /home/testuser/myproject", m0.CWD)
	}

	m1 := session.Messages[1]
	if m1.Type != "assistant" || m1.Sender != "assistant" {
		t.Errorf("Messages[1] type=%q sender=%q, want assistant/assistant", m1.Type, m1.Sender)
	}

	m2 := session.Messages[2]
	if m2.CWD != "/home/testuser/myproject/src" {
		t.Errorf("Messages[2] CWD = %q, want /home/testuser/myproject/src (from turn_context)", m2.CWD)
	}

	if session.Summary != "Fix the bug in the login handler" {
		t.Errorf("Summary = %q, want first user message", session.Summary)
	}
}

func TestParseFile_DeterministicUUIDs(t *testing.T) {
	s1, err := ParseFile("testdata/sample.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	s2, err := ParseFile("testdata/sample.jsonl")
	if err != nil {
		t.Fatal(err)
	}

	for i := range s1.Messages {
		if s1.Messages[i].UUID != s2.Messages[i].UUID {
			t.Errorf("Message %d UUID mismatch: %q != %q", i, s1.Messages[i].UUID, s2.Messages[i].UUID)
		}
		if s1.Messages[i].UUID == "" {
			t.Errorf("Message %d UUID is empty", i)
		}
	}
}

func TestParseFile_ResponseItemOnly(t *testing.T) {
	session, err := ParseFile("testdata/response_item_only.jsonl")
	if err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}

	if session.SessionID != "test-ri-session" {
		t.Errorf("SessionID = %q, want %q", session.SessionID, "test-ri-session")
	}

	if len(session.Messages) != 4 {
		t.Fatalf("len(Messages) = %d, want 4", len(session.Messages))
	}

	// Verify alternating user/assistant from response_item events
	m0 := session.Messages[0]
	if m0.Type != "user" || m0.Sender != "human" {
		t.Errorf("Messages[0] type=%q sender=%q, want user/human", m0.Type, m0.Sender)
	}
	if m0.TextContent != "Refactor the database module" {
		t.Errorf("Messages[0] text = %q", m0.TextContent)
	}

	m1 := session.Messages[1]
	if m1.Type != "assistant" || m1.Sender != "assistant" {
		t.Errorf("Messages[1] type=%q sender=%q, want assistant/assistant", m1.Type, m1.Sender)
	}

	// Verify function_call items are skipped (not type=message)
	m2 := session.Messages[2]
	if m2.TextContent != "Also add connection pooling" {
		t.Errorf("Messages[2] text = %q, want 'Also add connection pooling'", m2.TextContent)
	}

	if session.Summary != "Refactor the database module" {
		t.Errorf("Summary = %q, want first user message", session.Summary)
	}
}

func TestParseFile_DualBufferSelectsLargerSource(t *testing.T) {
	// sample.jsonl has 4 event_msg messages and 1 response_item message
	// Parser should select event_msg since it has more messages
	session, err := ParseFile("testdata/sample.jsonl")
	if err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}

	if len(session.Messages) != 4 {
		t.Fatalf("len(Messages) = %d, want 4 (from event_msg, not 1 from response_item)", len(session.Messages))
	}

	// First message should be the event_msg user message, not response_item
	if session.Messages[0].TextContent != "Fix the bug in the login handler" {
		t.Errorf("Messages[0] text = %q, want event_msg content", session.Messages[0].TextContent)
	}
}

func TestIsSystemBoilerplate(t *testing.T) {
	tests := []struct {
		text string
		want bool
	}{
		{"# AGENTS.md instructions for /Users/neil/scrubbed", true},
		{"<environment_context>\n<cwd>/Users/neil</cwd>", true},
		{"<system-reminder>something</system-reminder>", true},
		{"Fix the bug in the login handler", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := isSystemBoilerplate(tt.text); got != tt.want {
			t.Errorf("isSystemBoilerplate(%q) = %v, want %v", tt.text[:min(len(tt.text), 40)], got, tt.want)
		}
	}
}

func TestParseFile_SkipsBoilerplateUserMessages(t *testing.T) {
	session, err := ParseFile("testdata/boilerplate_filter.jsonl")
	if err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}

	// Should have 2 messages: the real user message and the assistant reply
	// The AGENTS.md and environment_context messages should be filtered out
	if len(session.Messages) != 2 {
		t.Fatalf("len(Messages) = %d, want 2 (boilerplate filtered)", len(session.Messages))
	}

	if session.Messages[0].TextContent != "Fix the login bug" {
		t.Errorf("Messages[0] text = %q, want real user message", session.Messages[0].TextContent)
	}
	if session.Summary != "Fix the login bug" {
		t.Errorf("Summary = %q, want real user message not boilerplate", session.Summary)
	}
}

func TestParseFile_SkipsNonMessageEvents(t *testing.T) {
	session, err := ParseFile("testdata/sample.jsonl")
	if err != nil {
		t.Fatal(err)
	}

	for _, msg := range session.Messages {
		if msg.Type != "user" && msg.Type != "assistant" {
			t.Errorf("unexpected message type %q (should only have user/assistant)", msg.Type)
		}
	}
}

func TestParseFile_ResumeVia(t *testing.T) {
	const (
		id   = "0199a001-0000-7000-8000-00000000c41d"
		root = "0199a000-0000-7000-8000-000000000001"
	)
	meta := func(sessionID, version, source string) string {
		return `{"timestamp":"2026-10-05T00:02:00.119Z","type":"session_meta","payload":{"session_id":"` + sessionID +
			`","id":"` + id + `","cwd":"/p","cli_version":"0.157.1","multi_agent_version":"` + version +
			`","source":` + source + `}}` + "\n"
	}
	const (
		threadSpawn = `{"subagent":{"thread_spawn":{"parent_thread_id":"` + root + `","depth":1}}}`
		userMsg     = `{"timestamp":"2026-10-05T00:02:02.000Z","type":"event_msg","payload":{"type":"user_message","message":"hi"}}` + "\n"
	)

	tests := []struct {
		name    string
		content string
		want    string
	}{
		{"v2 thread_spawn sub-agent resumes through its root", meta(root, "v2", threadSpawn), root},
		{"v1 thread_spawn sub-agent resumes directly", meta(root, "v1", threadSpawn), ""},
		{"guardian reviewer resumes directly", meta(root, "disabled", `{"subagent":{"other":"guardian"}}`), ""},
		{"review sub-agent resumes directly", meta(root, "v2", `{"subagent":"review"}`), ""},
		{"root thread resumes directly", meta(id, "v2", `"cli"`), ""},
		{"v2 sub-agent without a root id resumes directly", meta("", "v2", threadSpawn), ""},
		{
			// codex fork of a sub-agent starts a new root thread whose file
			// replays the sub-agent's session_meta second.
			"fork of a v2 sub-agent is its own root",
			meta(id, "v2", `"cli"`) + `{"timestamp":"2026-10-05T00:02:00.120Z","type":"session_meta","payload":{"session_id":"` + root +
				`","id":"0199a000-0000-7000-8000-000000000002","cwd":"/p","multi_agent_version":"v2","source":` + threadSpawn + `}}` + "\n",
			"",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "rollout-2026-10-05T00-02-00-"+id+".jsonl")
			if err := os.WriteFile(path, []byte(tt.content+userMsg), 0o600); err != nil {
				t.Fatal(err)
			}
			session, err := ParseFile(path)
			if err != nil {
				t.Fatalf("ParseFile() error = %v", err)
			}
			if session.ResumeVia != tt.want {
				t.Errorf("ResumeVia = %q, want %q", session.ResumeVia, tt.want)
			}
			via, err := ReadResumeVia(path)
			if err != nil {
				t.Fatalf("ReadResumeVia() error = %v", err)
			}
			if via != tt.want {
				t.Errorf("ReadResumeVia() = %q, want %q", via, tt.want)
			}
		})
	}
}

// A forked sub-agent replays the session_meta of the thread it forked from
// after its own; ResumeVia comes from the file's own (first) session_meta.
func TestParseFile_ResumeViaUsesFirstSessionMeta(t *testing.T) {
	session, err := ParseFile("testdata/subagent_v2.jsonl")
	if err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}
	if want := "0199a000-0000-7000-8000-000000000001"; session.ResumeVia != want {
		t.Errorf("ResumeVia = %q, want root thread %q", session.ResumeVia, want)
	}
	if via, err := ReadResumeVia("testdata/subagent_v2.jsonl"); err != nil || via != session.ResumeVia {
		t.Errorf("ReadResumeVia() = %q, %v; want %q", via, err, session.ResumeVia)
	}
}
