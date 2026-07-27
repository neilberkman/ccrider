package session

import (
	"reflect"
	"strings"
	"testing"
)

func TestResumeBinary(t *testing.T) {
	cases := map[string]string{
		ProviderClaude:      "claude",
		ProviderCodex:       "codex",
		ProviderCopilot:     "copilot",
		ProviderOpenCode:    "opencode",
		ProviderPi:          "pi",
		ProviderAntigravity: "agy",
		ProviderAmp:         "amp",
		"":                  "claude",
		"unknown":           "claude",
	}
	for provider, want := range cases {
		if got := ResumeBinary(provider); got != want {
			t.Errorf("ResumeBinary(%q) = %q, want %q", provider, got, want)
		}
	}
}

func TestAntigravityResumeSpec(t *testing.T) {
	spec := BuildResumeSpec(ProviderAntigravity, "sess-1", false, nil)
	if spec.Binary != "agy" || spec.Prefix != "agy --conversation 'sess-1'" || spec.AcceptsPrompt {
		t.Fatalf("BuildResumeSpec(antigravity) = %#v", spec)
	}

	if SupportsFork(ProviderAntigravity) {
		t.Error("Antigravity should require its interactive /fork command")
	}
	if SupportsFork(ProviderCopilot) {
		t.Error("Copilot should not advertise unsupported direct forking")
	}
	if SupportsFork(ProviderAmp) {
		t.Error("Amp should not advertise unsupported direct forking")
	}
	for _, provider := range []string{ProviderClaude, ProviderCodex, ProviderOpenCode, ProviderPi} {
		if !SupportsFork(provider) {
			t.Errorf("SupportsFork(%q) = false, want true", provider)
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
		name       string
		provider   string
		fork       bool
		flags      []string
		wantPrefix string
		wantPrompt bool
		wantBinary string
	}{
		{
			name:       "claude default",
			provider:   ProviderClaude,
			wantPrefix: "claude --resume 'sess-1'",
			wantPrompt: true,
			wantBinary: "claude",
		},
		{
			name:       "claude with flags",
			provider:   ProviderClaude,
			flags:      []string{"--dangerously-skip-permissions"},
			wantPrefix: "claude --dangerously-skip-permissions --resume 'sess-1'",
			wantPrompt: true,
			wantBinary: "claude",
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
			name:       "claude fork with flags",
			provider:   ProviderClaude,
			fork:       true,
			flags:      []string{"--dangerously-skip-permissions"},
			wantPrefix: "claude --dangerously-skip-permissions --resume 'sess-1' --fork-session",
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
			// Codex global options must come BEFORE the subcommand
			// (codex [OPTIONS] <COMMAND>); after it they are rejected.
			name:       "codex flags placed before subcommand",
			provider:   ProviderCodex,
			flags:      []string{"--dangerously-bypass-approvals-and-sandbox"},
			wantPrefix: "codex --dangerously-bypass-approvals-and-sandbox resume 'sess-1'",
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
			name:       "codex fork keeps flags before subcommand",
			provider:   ProviderCodex,
			fork:       true,
			flags:      []string{"--dangerously-bypass-approvals-and-sandbox"},
			wantPrefix: "codex --dangerously-bypass-approvals-and-sandbox fork 'sess-1'",
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
			name:       "copilot flags placed before --resume",
			provider:   ProviderCopilot,
			flags:      []string{"--allow-all-tools"},
			wantPrefix: "copilot --allow-all-tools --resume='sess-1'",
			wantPrompt: false,
			wantBinary: "copilot",
		},
		{
			name:       "opencode resume",
			provider:   ProviderOpenCode,
			wantPrefix: "opencode --session 'sess-1'",
			wantPrompt: true,
			wantBinary: "opencode",
		},
		{
			name:       "opencode flags placed before --session",
			provider:   ProviderOpenCode,
			flags:      []string{"--model", "anthropic/claude-sonnet-4"},
			wantPrefix: "opencode --model anthropic/claude-sonnet-4 --session 'sess-1'",
			wantPrompt: true,
			wantBinary: "opencode",
		},
		{
			name:       "opencode fork",
			provider:   ProviderOpenCode,
			fork:       true,
			wantPrefix: "opencode --session 'sess-1' --fork",
			wantPrompt: true,
			wantBinary: "opencode",
		},
		{
			name:       "pi resume",
			provider:   ProviderPi,
			wantPrefix: "pi --session 'sess-1'",
			wantPrompt: true,
			wantBinary: "pi",
		},
		{
			name:       "pi flags placed before session",
			provider:   ProviderPi,
			flags:      []string{"--offline"},
			wantPrefix: "pi --offline --session 'sess-1'",
			wantPrompt: true,
			wantBinary: "pi",
		},
		{
			name:       "pi fork",
			provider:   ProviderPi,
			fork:       true,
			wantPrefix: "pi --fork 'sess-1'",
			wantPrompt: true,
			wantBinary: "pi",
		},
		{
			name:       "pi fork with flags",
			provider:   ProviderPi,
			fork:       true,
			flags:      []string{"--offline"},
			wantPrefix: "pi --offline --fork 'sess-1'",
			wantPrompt: true,
			wantBinary: "pi",
		},
		{
			name:       "multiple flags joined in order",
			provider:   ProviderCodex,
			flags:      []string{"--dangerously-bypass-approvals-and-sandbox", "--search"},
			wantPrefix: "codex --dangerously-bypass-approvals-and-sandbox --search resume 'sess-1'",
			wantPrompt: true,
			wantBinary: "codex",
		},
		{
			name:       "amp continue",
			provider:   ProviderAmp,
			fork:       true,
			flags:      []string{"--ignored"},
			wantPrefix: "amp threads continue 'sess-1'",
			wantPrompt: false,
			wantBinary: "amp",
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

		// UUID stripping and flag placement compose.
		spec = BuildResumeSpec(ProviderCodex, id, false, []string{"--dangerously-bypass-approvals-and-sandbox"})
		want = "codex --dangerously-bypass-approvals-and-sandbox resume '019e93f0-3efe-7742-9598-bb06b36fb25a'"
		if spec.Prefix != want {
			t.Errorf("Prefix with flags = %q, want %q", spec.Prefix, want)
		}
	})

	t.Run("pi timestamped filename stem extracts uuid", func(t *testing.T) {
		id := "2026-06-18T13-47-19-786Z_019edafc-796a-79ce-a42b-f1d986bd3e8c"
		spec := BuildResumeSpec(ProviderPi, id, false, nil)
		want := "pi --session '019edafc-796a-79ce-a42b-f1d986bd3e8c'"
		if spec.Prefix != want {
			t.Errorf("Prefix = %q, want %q", spec.Prefix, want)
		}

		spec = BuildResumeSpec(ProviderPi, id, true, []string{"--offline"})
		want = "pi --offline --fork '019edafc-796a-79ce-a42b-f1d986bd3e8c'"
		if spec.Prefix != want {
			t.Errorf("Fork prefix with flags = %q, want %q", spec.Prefix, want)
		}
	})

	// A session id containing shell metacharacters must be neutralized by
	// quoting so it cannot break out of the command.
	t.Run("malicious id is quoted", func(t *testing.T) {
		id := "x'; rm -rf /; '"
		for _, provider := range []string{ProviderClaude, ProviderCodex, ProviderCopilot, ProviderOpenCode, ProviderPi, ProviderAntigravity, ProviderAmp} {
			spec := BuildResumeSpec(provider, id, false, nil)
			if !strings.Contains(spec.Prefix, ShellQuote(id)) {
				t.Errorf("%s prefix = %q, want it to contain the quoted id %q", provider, spec.Prefix, ShellQuote(id))
			}
		}
	})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := BuildResumeSpec(tt.provider, "sess-1", tt.fork, tt.flags)
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

	t.Run("Amp uses an existing local project path", func(t *testing.T) {
		projectPath := t.TempDir()
		got := ResumeCommandIn(projectPath, ProviderAmp, "T-one", "ignored", false, nil)
		want := "cd " + ShellQuote(projectPath) + " && amp threads continue 'T-one'"
		if got != want {
			t.Errorf("ResumeCommandIn = %q, want %q", got, want)
		}
	})

	t.Run("Amp ignores an unavailable remote project path", func(t *testing.T) {
		projectPath := t.TempDir() + "/missing-orb-workspace"
		got := ResumeCommandIn(projectPath, ProviderAmp, "T-one", "ignored", false, nil)
		want := "amp threads continue 'T-one'"
		if got != want {
			t.Errorf("ResumeCommandIn = %q, want %q", got, want)
		}
	})
}

func TestResumeCommandIn(t *testing.T) {
	tests := []struct {
		name        string
		projectPath string
		provider    string
		sessionID   string
		prompt      string
		fork        bool
		want        string
	}{
		{
			name:        "claude has cd prefix",
			projectPath: "/Users/x/proj",
			provider:    ProviderClaude,
			sessionID:   "sess-1",
			want:        "cd '/Users/x/proj' && claude --resume 'sess-1'",
		},
		{
			name:        "codex rollout id has cd prefix and bare uuid",
			projectPath: "/Users/x/proj",
			provider:    ProviderCodex,
			sessionID:   "rollout-2026-06-04T14-40-56-019e93f0-3efe-7742-9598-bb06b36fb25a",
			want:        "cd '/Users/x/proj' && codex resume '019e93f0-3efe-7742-9598-bb06b36fb25a'",
		},
		{
			name:        "copilot has cd prefix",
			projectPath: "/Users/x/proj",
			provider:    ProviderCopilot,
			sessionID:   "sess-1",
			want:        "cd '/Users/x/proj' && copilot --resume='sess-1'",
		},
		{
			name:        "opencode has cd prefix",
			projectPath: "/Users/x/proj",
			provider:    ProviderOpenCode,
			sessionID:   "sess-1",
			want:        "cd '/Users/x/proj' && opencode --session 'sess-1'",
		},
		{
			name:        "pi has cd prefix",
			projectPath: "/Users/x/proj",
			provider:    ProviderPi,
			sessionID:   "sess-1",
			want:        "cd '/Users/x/proj' && pi --session 'sess-1'",
		},
		{
			name:        "prompt appended after cd prefix",
			projectPath: "/Users/x/proj",
			provider:    ProviderClaude,
			sessionID:   "sess-1",
			prompt:      "keep going",
			want:        "cd '/Users/x/proj' && claude --resume 'sess-1' 'keep going'",
		},
		{
			name:        "fork keeps cd prefix",
			projectPath: "/Users/x/proj",
			provider:    ProviderClaude,
			sessionID:   "sess-1",
			fork:        true,
			want:        "cd '/Users/x/proj' && claude --resume 'sess-1' --fork-session",
		},
		{
			name:        "project path with single quote is escaped",
			projectPath: "/Users/x/it's",
			provider:    ProviderClaude,
			sessionID:   "sess-1",
			want:        `cd '/Users/x/it'\''s' && claude --resume 'sess-1'`,
		},
		{
			name:      "empty project path falls back to bare command with comment",
			provider:  ProviderClaude,
			sessionID: "sess-1",
			want:      "claude --resume 'sess-1' # project path missing in DB for session sess-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResumeCommandIn(tt.projectPath, tt.provider, tt.sessionID, tt.prompt, tt.fork, nil)
			if got != tt.want {
				t.Errorf("ResumeCommandIn = %q, want %q", got, tt.want)
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
			name:     "opencode uses prompt flag",
			provider: ProviderOpenCode,
			prompt:   "keep going",
			want:     "opencode --session 'sess-1' --prompt 'keep going'",
		},
		{
			name:     "pi appends prompt positionally",
			provider: ProviderPi,
			prompt:   "keep going",
			want:     "pi --session 'sess-1' 'keep going'",
		},
		{
			name:     "pi fork uses fork flag",
			provider: ProviderPi,
			fork:     true,
			want:     "pi --fork 'sess-1'",
		},
		{
			name:     "prompt with single quote is escaped",
			provider: ProviderClaude,
			prompt:   "it's done",
			want:     `claude --resume 'sess-1' 'it'\''s done'`,
		},
		{
			name:     "amp drops prompt",
			provider: ProviderAmp,
			prompt:   "keep going",
			want:     "amp threads continue 'sess-1'",
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

func TestResolveWorkingDir(t *testing.T) {
	callerDir := t.TempDir()
	t.Chdir(callerDir)
	existingProject := t.TempDir()
	missingProject := t.TempDir() + "/orb-workspace"

	if got := ResolveWorkingDir(ProviderAmp, existingProject, "/ignored"); got != existingProject {
		t.Errorf("ResolveWorkingDir(Amp existing) = %q, want %q", got, existingProject)
	}
	if got := ResolveWorkingDir(ProviderAmp, missingProject, "/ignored"); got != callerDir {
		t.Errorf("ResolveWorkingDir(Amp missing) = %q, want caller cwd %q", got, callerDir)
	}
	if got := ResolveWorkingDir(ProviderClaude, missingProject, "/ignored"); got != missingProject {
		t.Errorf("ResolveWorkingDir(Claude) = %q, want required project %q", got, missingProject)
	}
}

func TestFileIdentityProviderNames(t *testing.T) {
	want := []string{ProviderClaude, ProviderCodex, ProviderPi}
	if got := FileIdentityProviderNames(); !reflect.DeepEqual(got, want) {
		t.Fatalf("FileIdentityProviderNames() = %v, want %v", got, want)
	}
}
