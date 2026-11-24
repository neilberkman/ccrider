package ccsessions

import (
	"context"
	"errors"
	"io/fs"
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

func TestParseFile_CorruptedLine(t *testing.T) {
	session, err := ParseFile("testdata/corrupted-line.jsonl")
	if err != nil {
		t.Fatalf("ParseFile() should skip corrupted lines, got error = %v", err)
	}

	if session.Summary != "Corrupted session" {
		t.Errorf("Summary = %v, want 'Corrupted session'", session.Summary)
	}

	// Should have 2 messages (corrupted line 3 skipped)
	if len(session.Messages) != 2 {
		t.Errorf("Message count = %v, want 2 (corrupted line should be skipped)", len(session.Messages))
	}

	if session.Messages[0].TextContent != "before corruption" {
		t.Errorf("First message = %q, want 'before corruption'", session.Messages[0].TextContent)
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

func TestParseFile_Entrypoint(t *testing.T) {
	session, err := ParseFile("testdata/headless-session.jsonl")
	if err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}
	// The first record (a queue operation) names no entrypoint; the first
	// record that does decides it.
	if session.Entrypoint != "sdk-cli" {
		t.Errorf("Entrypoint = %q, want %q", session.Entrypoint, "sdk-cli")
	}
}

func TestParseFile_NoEntrypoint(t *testing.T) {
	session, err := ParseFile("testdata/no-summary.jsonl")
	if err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}
	if session.Entrypoint != "" {
		t.Errorf("Entrypoint = %q, want empty for a transcript that names none", session.Entrypoint)
	}
}

func TestReadEntrypoint(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"testdata/headless-session.jsonl", "sdk-cli"},
		{"testdata/no-summary.jsonl", ""},
	}
	for _, tt := range tests {
		got, err := ReadEntrypoint(context.Background(), tt.path)
		if err != nil {
			t.Fatalf("ReadEntrypoint(%s) error = %v", tt.path, err)
		}
		if got != tt.want {
			t.Errorf("ReadEntrypoint(%s) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestReadEntrypoint_MissingFile(t *testing.T) {
	if _, err := ReadEntrypoint(context.Background(), "testdata/does-not-exist.jsonl"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("ReadEntrypoint(missing) error = %v, want fs.ErrNotExist", err)
	}
}
