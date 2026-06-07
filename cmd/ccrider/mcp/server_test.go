package mcp

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/neilberkman/ccrider/internal/core/db"
	"github.com/neilberkman/ccrider/internal/core/search"
)

// Every session payload the MCP server returns must carry a pre-built
// resume_command with the cd prefix. Agents consuming these tools were
// stitching bare resume commands from project + session_id, which fail
// from any cwd other than the project directory.

func TestToSessionMatchResumeCommand(t *testing.T) {
	tests := []struct {
		name        string
		provider    string
		sessionID   string
		project     string
		claudeFlags []string
		want        string
	}{
		{
			name:        "claude with configured flags",
			provider:    "claude",
			sessionID:   "sess-1",
			project:     "/Users/neil/bin",
			claudeFlags: []string{"--dangerously-skip-permissions"},
			want:        "cd '/Users/neil/bin' && claude --dangerously-skip-permissions --resume 'sess-1'",
		},
		{
			name:        "codex ignores claude flags",
			provider:    "codex",
			sessionID:   "sess-1",
			project:     "/Users/neil/bin",
			claudeFlags: []string{"--dangerously-skip-permissions"},
			want:        "cd '/Users/neil/bin' && codex resume 'sess-1'",
		},
		{
			name:      "claude",
			provider:  "claude",
			sessionID: "2d0a5a2d-1111-2222-3333-444455556666",
			project:   "/Users/neil/bin",
			want:      "cd '/Users/neil/bin' && claude --resume '2d0a5a2d-1111-2222-3333-444455556666'",
		},
		{
			name:      "codex strips rollout prefix",
			provider:  "codex",
			sessionID: "rollout-2026-06-04T14-40-56-019e93f0-3efe-7742-9598-bb06b36fb25a",
			project:   "/Users/neil/bin",
			want:      "cd '/Users/neil/bin' && codex resume '019e93f0-3efe-7742-9598-bb06b36fb25a'",
		},
		{
			name:      "copilot",
			provider:  "copilot",
			sessionID: "sess-1",
			project:   "/Users/neil/bin",
			want:      "cd '/Users/neil/bin' && copilot --resume='sess-1'",
		},
		{
			name:      "empty project falls back with comment",
			provider:  "claude",
			sessionID: "sess-1",
			want:      "claude --resume 'sess-1' # project path missing in DB for session sess-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toSessionMatch(search.SessionSearchResult{
				SessionID:   tt.sessionID,
				ProjectPath: tt.project,
				Provider:    tt.provider,
			}, tt.claudeFlags)
			if got.ResumeCommand != tt.want {
				t.Errorf("ResumeCommand = %q, want %q", got.ResumeCommand, tt.want)
			}
		})
	}
}

func TestToSessionSummaryResumeCommand(t *testing.T) {
	tests := []struct {
		name        string
		provider    string
		sessionID   string
		project     string
		claudeFlags []string
		want        string
	}{
		{
			name:        "claude with configured flags",
			provider:    "claude",
			sessionID:   "sess-1",
			project:     "/Users/neil/bin",
			claudeFlags: []string{"--dangerously-skip-permissions"},
			want:        "cd '/Users/neil/bin' && claude --dangerously-skip-permissions --resume 'sess-1'",
		},
		{
			name:      "claude",
			provider:  "claude",
			sessionID: "sess-1",
			project:   "/Users/neil/bin",
			want:      "cd '/Users/neil/bin' && claude --resume 'sess-1'",
		},
		{
			name:      "codex strips rollout prefix",
			provider:  "codex",
			sessionID: "rollout-2026-06-04T14-40-56-019e93f0-3efe-7742-9598-bb06b36fb25a",
			project:   "/Users/neil/bin",
			want:      "cd '/Users/neil/bin' && codex resume '019e93f0-3efe-7742-9598-bb06b36fb25a'",
		},
		{
			name:      "copilot",
			provider:  "copilot",
			sessionID: "sess-1",
			project:   "/Users/neil/bin",
			want:      "cd '/Users/neil/bin' && copilot --resume='sess-1'",
		},
		{
			name:      "empty project falls back with comment",
			provider:  "codex",
			sessionID: "sess-1",
			want:      "codex resume 'sess-1' # project path missing in DB for session sess-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toSessionSummary(db.Session{
				SessionID:   tt.sessionID,
				ProjectPath: tt.project,
				Provider:    tt.provider,
				UpdatedAt:   time.Date(2026, 6, 7, 0, 0, 0, 0, time.UTC),
			}, tt.claudeFlags)
			if got.ResumeCommand != tt.want {
				t.Errorf("ResumeCommand = %q, want %q", got.ResumeCommand, tt.want)
			}
		})
	}
}

// The field must serialize as "resume_command" in every payload an agent sees.
func TestResumeCommandJSONFieldName(t *testing.T) {
	payloads := []interface{}{
		toSessionMatch(search.SessionSearchResult{SessionID: "s", ProjectPath: "/p", Provider: "claude"}, nil),
		toSessionSummary(db.Session{SessionID: "s", ProjectPath: "/p", Provider: "claude"}, nil),
		SessionMessagesResponse{SessionID: "s", ResumeCommand: "cd '/p' && claude --resume 's'"},
	}
	for _, p := range payloads {
		b, err := json.Marshal(p)
		if err != nil {
			t.Fatalf("marshal %T: %v", p, err)
		}
		// Round-trip: && is HTML-escaped on the wire (&), but any JSON
		// consumer decodes it back. Assert on the decoded value.
		var decoded map[string]interface{}
		if err := json.Unmarshal(b, &decoded); err != nil {
			t.Fatalf("unmarshal %T: %v", p, err)
		}
		got, _ := decoded["resume_command"].(string)
		if got != "cd '/p' && claude --resume 's'" {
			t.Errorf("%T resume_command = %q, want %q", p, got, "cd '/p' && claude --resume 's'")
		}
	}
}
