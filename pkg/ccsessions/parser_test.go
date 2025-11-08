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
