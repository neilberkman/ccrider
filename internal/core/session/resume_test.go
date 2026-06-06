package session

import (
	"strings"
	"testing"
)

func TestResumeBinary(t *testing.T) {
	cases := map[string]string{
		ProviderClaude:  "claude",
		ProviderCodex:   "codex",
		ProviderCopilot: "copilot",
		"":              "claude",
		"unknown":       "claude",
	}
	for provider, want := range cases {
		if got := ResumeBinary(provider); got != want {
			t.Errorf("ResumeBinary(%q) = %q, want %q", provider, got, want)
		}
	}
}

func TestShellQuote(t *testing.T) {
	cases := map[string]string{
		"abc":             "'abc'",
		"":                "''",
		"a b":             "'a b'",
		"it's":            `'it'\''s'`,
		"x'; rm -rf /; '": `'x'\''; rm -rf /; '\'''`,
	}
	for in, want := range cases {
		if got := ShellQuote(in); got != want {
			t.Errorf("ShellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildResumeSpec(t *testing.T) {
	tests := []struct {
		name        string
		provider    string
		fork        bool
		claudeFlags []string
		wantPrefix  string
		wantPrompt  bool
		wantBinary  string
	}{
		{
			name:       "claude default",
			provider:   ProviderClaude,
			wantPrefix: "claude --resume 'sess-1'",
			wantPrompt: true,
			wantBinary: "claude",
		},
		{
			name:        "claude with flags",
			provider:    ProviderClaude,
			claudeFlags: []string{"--dangerously-skip-permissions"},
			wantPrefix:  "claude --dangerously-skip-permissions --resume 'sess-1'",
			wantPrompt:  true,
			wantBinary:  "claude",
		},
		{
			name:       "claude fork",
			provider:   ProviderClaude,
			fork:       true,
			wantPrefix: "claude --resume 'sess-1' --fork-session",
			wantPrompt: true,
			wantBinary: "claude",
		},
		{
			name:       "codex resume",
			provider:   ProviderCodex,
			wantPrefix: "codex resume 'sess-1'",
			wantPrompt: true,
			wantBinary: "codex",
		},
		{
			name:       "codex fork",
			provider:   ProviderCodex,
			fork:       true,
			wantPrefix: "codex fork 'sess-1'",
			wantPrompt: true,
			wantBinary: "codex",
		},
		{
			name:       "copilot resume",
			provider:   ProviderCopilot,
			wantPrefix: "copilot --resume='sess-1'",
			wantPrompt: false,
			wantBinary: "copilot",
		},
		{
			name:        "codex ignores claude flags",
			provider:    ProviderCodex,
			claudeFlags: []string{"--dangerously-skip-permissions"},
			wantPrefix:  "codex resume 'sess-1'",
			wantPrompt:  true,
			wantBinary:  "codex",
		},
	}

	// Codex stores sessions under a rollout filename; resume must use the
	// trailing UUID (and quote it). Verified separately since the id differs.
	t.Run("codex rollout id extracts uuid", func(t *testing.T) {
		id := "rollout-2026-06-04T14-40-56-019e93f0-3efe-7742-9598-bb06b36fb25a"
		spec := BuildResumeSpec(ProviderCodex, id, false, nil)
		want := "codex resume '019e93f0-3efe-7742-9598-bb06b36fb25a'"
		if spec.Prefix != want {
			t.Errorf("Prefix = %q, want %q", spec.Prefix, want)
		}
	})

	// A session id containing shell metacharacters must be neutralized by
	// quoting so it cannot break out of the command.
	t.Run("malicious id is quoted", func(t *testing.T) {
		id := "x'; rm -rf /; '"
		for _, provider := range []string{ProviderClaude, ProviderCodex, ProviderCopilot} {
			spec := BuildResumeSpec(provider, id, false, nil)
			if !strings.Contains(spec.Prefix, ShellQuote(id)) {
				t.Errorf("%s prefix = %q, want it to contain the quoted id %q", provider, spec.Prefix, ShellQuote(id))
			}
		}
	})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := BuildResumeSpec(tt.provider, "sess-1", tt.fork, tt.claudeFlags)
			if spec.Prefix != tt.wantPrefix {
				t.Errorf("Prefix = %q, want %q", spec.Prefix, tt.wantPrefix)
			}
			if spec.AcceptsPrompt != tt.wantPrompt {
				t.Errorf("AcceptsPrompt = %v, want %v", spec.AcceptsPrompt, tt.wantPrompt)
			}
			if spec.Binary != tt.wantBinary {
				t.Errorf("Binary = %q, want %q", spec.Binary, tt.wantBinary)
			}
		})
	}
}

func TestResumeCommand(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		prompt   string
		fork     bool
		want     string
	}{
		{
			name:     "claude no prompt",
			provider: ProviderClaude,
			want:     "claude --resume 'sess-1'",
		},
		{
			name:     "claude with prompt",
			provider: ProviderClaude,
			prompt:   "pick up where we left off",
			want:     "claude --resume 'sess-1' 'pick up where we left off'",
		},
		{
			name:     "codex with prompt",
			provider: ProviderCodex,
			prompt:   "keep going",
			want:     "codex resume 'sess-1' 'keep going'",
		},
		{
			name:     "copilot drops prompt",
			provider: ProviderCopilot,
			prompt:   "keep going",
			want:     "copilot --resume='sess-1'",
		},
		{
			name:     "prompt with single quote is escaped",
			provider: ProviderClaude,
			prompt:   "it's done",
			want:     `claude --resume 'sess-1' 'it'\''s done'`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResumeCommand(tt.provider, "sess-1", tt.prompt, tt.fork, nil)
			if got != tt.want {
				t.Errorf("ResumeCommand = %q, want %q", got, tt.want)
			}
		})
	}
}
