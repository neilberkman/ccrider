package ccsessions

import (
	"testing"
)

func TestParseFile(t *testing.T) {
	session, err := ParseFile("testdata/sample.jsonl")
	if err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}

	// Check summary
	if session.Summary != "Test session" {
		t.Errorf("Summary = %v, want 'Test session'", session.Summary)
	}

	// Check message count
	if len(session.Messages) != 2 {
		t.Errorf("Message count = %v, want 2", len(session.Messages))
	}

	// Check first message
	if session.Messages[0].Type != "user" {
		t.Errorf("First message type = %v, want 'user'", session.Messages[0].Type)
	}
}

func TestParseFile_InvalidPath(t *testing.T) {
	_, err := ParseFile("nonexistent.jsonl")
	if err == nil {
		t.Error("ParseFile() should return error for invalid path")
	}
}

func TestParseFile_AgentSession(t *testing.T) {
	session, err := ParseFile("testdata/agent-session.jsonl")
	if err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}

	// Agent sessions don't have summary
	if session.Summary != "" {
		t.Errorf("Agent session should have empty summary, got %v", session.Summary)
	}

	// Should extract sessionId from messages
	if session.SessionID != "test-session-123" {
		t.Errorf("SessionID = %v, want 'test-session-123'", session.SessionID)
	}

	// Check message count
	if len(session.Messages) != 2 {
		t.Errorf("Message count = %v, want 2", len(session.Messages))
	}

	// Check first message is sidechain
	if !session.Messages[0].IsSidechain {
		t.Error("First message should be sidechain")
	}
}

func TestParseFile_NoSummary(t *testing.T) {
	session, err := ParseFile("testdata/no-summary.jsonl")
	if err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}

	// Should extract sessionId from messages
	if session.SessionID != "test-session-456" {
		t.Errorf("SessionID = %v, want 'test-session-456'", session.SessionID)
	}

	// Check message count
	if len(session.Messages) != 2 {
		t.Errorf("Message count = %v, want 2", len(session.Messages))
	}
}
