package codexsessions

import (
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
