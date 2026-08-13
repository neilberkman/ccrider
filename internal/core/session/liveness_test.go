package session

import "testing"

func TestMatchLiveProcess(t *testing.T) {
	cases := []struct {
		name     string
		argv     []string
		provider string
		session  string
		ok       bool
	}{
		{
			name:     "claude resumed",
			argv:     []string{"/Users/neil/.local/bin/claude", "--dangerously-skip-permissions", "--resume", "8650b828-b3a9-4db4-81c0-64d0fee6dd70"},
			provider: ProviderClaude, session: "8650b828-b3a9-4db4-81c0-64d0fee6dd70", ok: true,
		},
		{
			name:     "claude resumed with settings json before resume flag",
			argv:     []string{"/Users/neil/.local/bin/claude", "--settings", `{"hooks":{}}`, "--resume", "fd2f49dd-e673-4b57-b023-808c44eff582", "--dangerously-skip-permissions"},
			provider: ProviderClaude, session: "fd2f49dd-e673-4b57-b023-808c44eff582", ok: true,
		},
		{
			name:     "claude fresh session",
			argv:     []string{"/Users/neil/.local/bin/claude", "--dangerously-skip-permissions"},
			provider: ProviderClaude, session: "", ok: true,
		},
		{
			name: "claude mcp server excluded",
			argv: []string{"claude", "mcp", "serve"},
			ok:   false,
		},
		{
			name:     "codex resume native binary",
			argv:     []string{"/opt/homebrew/.../bin/codex", "--dangerously-bypass-approvals-and-sandbox", "resume", "019fcb64-509d-7ef0-bd45-918cec3ac1a2"},
			provider: ProviderCodex, session: "019fcb64-509d-7ef0-bd45-918cec3ac1a2", ok: true,
		},
		{
			name:     "codex under node interpreter",
			argv:     []string{"node", "/opt/homebrew/bin/codex", "resume", "rollout-2026-07-24T08-08-11-019f94ab-7288-7043-b7ef-5e5b852ed3a5"},
			provider: ProviderCodex, session: "rollout-2026-07-24T08-08-11-019f94ab-7288-7043-b7ef-5e5b852ed3a5", ok: true,
		},
		{
			name: "codex app-server excluded",
			argv: []string{"/Applications/ChatGPT.app/Contents/Resources/codex", "app-server", "--listen", "stdio://"},
			ok:   false,
		},
		{
			name:     "codex prompt word resume is not an id",
			argv:     []string{"codex", "exec", "resume", "the", "refactor"},
			provider: ProviderCodex, session: "", ok: true,
		},
		{
			name:     "amp thread continue",
			argv:     []string{"amp", "threads", "continue", "T-abc-123"},
			provider: ProviderAmp, session: "T-abc-123", ok: true,
		},
		{
			name:     "copilot resume equals form",
			argv:     []string{"copilot", "--resume=0ccfddc4-00e7-443a-bb82-58ede5936619"},
			provider: ProviderCopilot, session: "0ccfddc4-00e7-443a-bb82-58ede5936619", ok: true,
		},
		{
			name: "unrelated node mcp server",
			argv: []string{"node", "/Users/neil/.npm/_npx/x/node_modules/.bin/sentry-mcp"},
			ok:   false,
		},
		{
			name: "empty argv",
			argv: nil,
			ok:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			match, ok := MatchLiveProcess(tc.argv)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if !ok {
				return
			}
			if match.Provider != tc.provider {
				t.Errorf("provider = %q, want %q", match.Provider, tc.provider)
			}
			if match.SessionID != tc.session {
				t.Errorf("session = %q, want %q", match.SessionID, tc.session)
			}
		})
	}
}
