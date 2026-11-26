package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/neilberkman/ccrider/internal/core/config"
	"github.com/neilberkman/ccrider/internal/core/db"
	"github.com/neilberkman/ccrider/internal/core/search"
)

func TestRefreshBeforeReadReturnsWhenTheCallIsCancelled(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	database, err := db.New(filepath.Join(t.TempDir(), "cached.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	syncer := newServerSyncer(database)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	refreshBeforeRead(ctx, syncer)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("refreshBeforeRead() took %v after its call was cancelled, want an immediate return", elapsed)
	}
}

func TestRefreshBeforeReadCompletesAfterSessionFailure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	sessionDir := filepath.Join(home, ".claude", "projects")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "bad.jsonl"), []byte("not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	database, err := db.New(filepath.Join(t.TempDir(), "cached.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()

	before := time.Now()
	refreshBeforeRead(context.Background(), newServerSyncer(database))
	// A session that fails to import still lets the pass complete, so the
	// next tool call is not stuck re-running it.
	started, err := database.LastSyncPassStarted()
	if err != nil || started.UnixMilli() < before.UnixMilli() {
		t.Fatalf("last pass started %v (err %v), want a complete pass after %v", started, err, before)
	}
}

// Every session payload the MCP server returns must carry a pre-built
// resume_command with the cd prefix. Agents consuming these tools were
// stitching bare resume commands from project + session_id, which fail
// from any cwd other than the project directory.

func TestToSessionMatchResumeCommand(t *testing.T) {
	tests := []struct {
		name      string
		provider  string
		sessionID string
		project   string
		cfg       *config.Config
		want      string
	}{
		{
			name:      "claude with configured flags",
			provider:  "claude",
			sessionID: "sess-1",
			project:   "/Users/neil/bin",
			cfg:       &config.Config{ClaudeFlags: []string{"--dangerously-skip-permissions"}},
			want:      "cd '/Users/neil/bin' && claude --dangerously-skip-permissions --resume 'sess-1'",
		},
		{
			name:      "codex ignores claude flags",
			provider:  "codex",
			sessionID: "sess-1",
			project:   "/Users/neil/bin",
			cfg:       &config.Config{ClaudeFlags: []string{"--dangerously-skip-permissions"}},
			want:      "cd '/Users/neil/bin' && codex resume 'sess-1'",
		},
		{
			name:      "codex with configured flags before subcommand",
			provider:  "codex",
			sessionID: "rollout-2026-06-04T14-40-56-019e93f0-3efe-7742-9598-bb06b36fb25a",
			project:   "/Users/neil/bin",
			cfg:       &config.Config{CodexFlags: []string{"--dangerously-bypass-approvals-and-sandbox"}},
			want:      "cd '/Users/neil/bin' && codex --dangerously-bypass-approvals-and-sandbox resume '019e93f0-3efe-7742-9598-bb06b36fb25a'",
		},
		{
			name:      "copilot with configured flags",
			provider:  "copilot",
			sessionID: "sess-1",
			project:   "/Users/neil/bin",
			cfg:       &config.Config{CopilotFlags: []string{"--allow-all-tools"}},
			want:      "cd '/Users/neil/bin' && copilot --allow-all-tools --resume='sess-1'",
		},
		{
			name:      "opencode with configured flags",
			provider:  "opencode",
			sessionID: "ses_1",
			project:   "/Users/neil/bin",
			cfg:       &config.Config{OpenCodeFlags: []string{"--model", "anthropic/claude-sonnet-4"}},
			want:      "cd '/Users/neil/bin' && opencode --model anthropic/claude-sonnet-4 --session 'ses_1'",
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
			name:      "opencode",
			provider:  "opencode",
			sessionID: "ses_1",
			project:   "/Users/neil/bin",
			want:      "cd '/Users/neil/bin' && opencode --session 'ses_1'",
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
			}, tt.cfg)
			if got.ResumeCommand != tt.want {
				t.Errorf("ResumeCommand = %q, want %q", got.ResumeCommand, tt.want)
			}
		})
	}
}

func TestToSessionSummaryResumeCommand(t *testing.T) {
	tests := []struct {
		name      string
		provider  string
		sessionID string
		project   string
		cfg       *config.Config
		want      string
	}{
		{
			name:      "claude with configured flags",
			provider:  "claude",
			sessionID: "sess-1",
			project:   "/Users/neil/bin",
			cfg:       &config.Config{ClaudeFlags: []string{"--dangerously-skip-permissions"}},
			want:      "cd '/Users/neil/bin' && claude --dangerously-skip-permissions --resume 'sess-1'",
		},
		{
			name:      "codex with configured flags before subcommand",
			provider:  "codex",
			sessionID: "sess-1",
			project:   "/Users/neil/bin",
			cfg:       &config.Config{CodexFlags: []string{"--dangerously-bypass-approvals-and-sandbox"}},
			want:      "cd '/Users/neil/bin' && codex --dangerously-bypass-approvals-and-sandbox resume 'sess-1'",
		},
		{
			name:      "copilot with configured flags",
			provider:  "copilot",
			sessionID: "sess-1",
			project:   "/Users/neil/bin",
			cfg:       &config.Config{CopilotFlags: []string{"--allow-all-tools"}},
			want:      "cd '/Users/neil/bin' && copilot --allow-all-tools --resume='sess-1'",
		},
		{
			name:      "opencode with configured flags",
			provider:  "opencode",
			sessionID: "ses_1",
			project:   "/Users/neil/bin",
			cfg:       &config.Config{OpenCodeFlags: []string{"--model", "anthropic/claude-sonnet-4"}},
			want:      "cd '/Users/neil/bin' && opencode --model anthropic/claude-sonnet-4 --session 'ses_1'",
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
			name:      "opencode",
			provider:  "opencode",
			sessionID: "ses_1",
			project:   "/Users/neil/bin",
			want:      "cd '/Users/neil/bin' && opencode --session 'ses_1'",
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
			}, tt.cfg)
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

func TestSessionConvertersTruncateHugeSummaries(t *testing.T) {
	huge := strings.Repeat("a", maxResponseBytes())
	match := toSessionMatch(search.SessionSearchResult{
		SessionID:      "s1",
		SessionSummary: huge,
		ProjectPath:    "/p",
		Provider:       "claude",
	}, nil)
	if len([]rune(match.Summary)) != maxSummaryRunes+1 {
		t.Errorf("SessionMatch.Summary rune len = %d, want %d", len([]rune(match.Summary)), maxSummaryRunes+1)
	}

	summary := toSessionSummary(db.Session{
		SessionID:   "s1",
		Summary:     huge,
		ProjectPath: "/p",
		Provider:    "claude",
		UpdatedAt:   time.Date(2026, 6, 7, 0, 0, 0, 0, time.UTC),
	}, nil)
	if len([]rune(summary.Summary)) != maxSummaryRunes+1 {
		t.Errorf("SessionSummary.Summary rune len = %d, want %d", len([]rune(summary.Summary)), maxSummaryRunes+1)
	}
}

func TestFormatOpenSessionsResponseTightensSummariesBeforeDropping(t *testing.T) {
	// ~110 sessions with 200-char summaries exceed 36KB, but fit once summaries
	// are tightened to maxCompactSummaryRunes (80 chars) without dropping any.
	var groups []OpenSessionGroup
	for i := 0; i < 110; i++ {
		groups = append(groups, OpenSessionGroup{
			Project: fmt.Sprintf("/Users/dev/project-%02d", i%10),
			Sessions: []OpenSession{{
				Provider:    "claude",
				SessionID:   fmt.Sprintf("session-%03d-0000-0000-0000-000000000000", i),
				Summary:     strings.Repeat("x", maxOpenSummaryRunes),
				TTY:         fmt.Sprintf("ttys%03d", i),
				PID:         int32(1000 + i),
				Match:       "declared",
				IdleSeconds: int64(i * 60),
			}},
		})
	}
	if len(mustMarshal(OpenSessionsResponse{Groups: groups})) <= maxResponseBytes() {
		t.Fatal("test setup should exceed budget before tightening")
	}

	resp := formatOpenSessionsResponse(groups)
	if len(mustMarshal(resp)) > maxResponseBytes() {
		t.Fatalf("response exceeds budget: %d > %d", len(mustMarshal(resp)), maxResponseBytes())
	}
	if resp.Truncated {
		t.Fatalf("expected tightening summaries to avoid dropping sessions, got Truncated=true (%s)", resp.TruncatedMessage)
	}
	if countOpenSessions(resp.Groups) != 110 {
		t.Fatalf("kept %d sessions, want all 110", countOpenSessions(resp.Groups))
	}
	// Recently active sessions should keep their full summary while stale ones are compacted first.
	if got := len([]rune(resp.Groups[0].Sessions[0].Summary)); got != maxOpenSummaryRunes {
		t.Fatalf("most-active session summary rune len = %d, want %d (uncompacted)", got, maxOpenSummaryRunes)
	}
	if got := len([]rune(resp.Groups[len(resp.Groups)-1].Sessions[0].Summary)); got != maxCompactSummaryRunes+1 {
		t.Fatalf("most-idle session summary rune len = %d, want %d (compacted)", got, maxCompactSummaryRunes+1)
	}
}

func TestFormatOpenSessionsResponseDropsMostIdleWhenOverBudget(t *testing.T) {
	// 300 sessions exceed budget even after tightening summaries.
	var groups []OpenSessionGroup
	for i := 0; i < 300; i++ {
		groups = append(groups, OpenSessionGroup{
			Project: fmt.Sprintf("/Users/dev/project-%03d", i),
			Sessions: []OpenSession{{
				Provider:    "claude",
				SessionID:   fmt.Sprintf("session-%03d-0000-0000-0000-000000000000", i),
				Summary:     strings.Repeat("y", maxOpenSummaryRunes),
				TTY:         fmt.Sprintf("ttys%03d", i),
				PID:         int32(2000 + i),
				Match:       "declared",
				IdleSeconds: int64(i * 100),
			}},
		})
	}

	resp := formatOpenSessionsResponse(groups)
	if len(mustMarshal(resp)) > maxResponseBytes() {
		t.Fatalf("response exceeds budget: %d > %d", len(mustMarshal(resp)), maxResponseBytes())
	}
	if !resp.Truncated || resp.TotalSessions != 300 || resp.TruncatedMessage == "" {
		t.Fatalf("missing truncation metadata: %+v", resp)
	}
	kept := countOpenSessions(resp.Groups)
	if kept == 0 || kept >= 300 {
		t.Fatalf("unexpected kept count: %d", kept)
	}
	// Most-active sessions (lowest IdleSeconds) must be preserved over stale ones.
	var maxKeptIdle int64
	for _, g := range resp.Groups {
		if len(g.Sessions) == 0 {
			t.Fatalf("empty group left behind: %q", g.Project)
		}
		for _, s := range g.Sessions {
			if s.IdleSeconds > maxKeptIdle {
				maxKeptIdle = s.IdleSeconds
			}
		}
	}
	if maxKeptIdle >= int64(kept*100) {
		t.Fatalf("expected only the %d least-idle sessions (idle < %d), got max idle %d", kept, kept*100, maxKeptIdle)
	}
}
