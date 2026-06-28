package session

import (
	"github.com/neilberkman/ccrider/internal/core/config"
	"github.com/neilberkman/ccrider/internal/core/db"
)

// ProviderFlags selects the configured extra-flags slice for the given
// provider's CLI (claude_flags / codex_flags / copilot_flags /
// opencode_flags). A nil cfg yields no flags. Unknown/empty providers map to
// Claude, matching BuildResumeSpec.
func ProviderFlags(cfg *config.Config, provider string) []string {
	if cfg == nil {
		return nil
	}
	switch provider {
	case ProviderCodex:
		return cfg.CodexFlags
	case ProviderCopilot:
		return cfg.CopilotFlags
	case ProviderOpenCode:
		return cfg.OpenCodeFlags
	default:
		return cfg.ClaudeFlags
	}
}

// ConfiguredFlags loads the config and returns the extra flags the user
// configured for the given provider's CLI (e.g.
// --dangerously-skip-permissions for Claude). Config errors yield no flags
// rather than an error: a resume command without optional flags is still
// correct.
func ConfiguredFlags(provider string) []string {
	cfg, err := config.Load()
	if err != nil {
		return nil
	}
	return ProviderFlags(cfg, provider)
}

// DisplayResumeCommand builds the one-line resume command an interface shows
// or copies for a session. It resolves the working directory and applies the
// provider's configured flags itself, so every interface (CLI, TUI, MCP)
// emits the identical command and none can forget a step.
func DisplayResumeCommand(provider, sessionID, projectPath, lastCwd string, fork bool) string {
	workDir := ResolveWorkingDir(projectPath, lastCwd)
	return ResumeCommandIn(workDir, provider, sessionID, "", fork, ConfiguredFlags(provider))
}

// LaunchInfoSource looks up the stored launch info for a session id.
// *db.DB satisfies this.
type LaunchInfoSource interface {
	GetSessionLaunchInfo(sessionID string) (*db.Session, string, error)
}

// DisplayResumeCommandFor builds the display resume command for a session
// looked up by id. When launch info cannot be loaded, it falls back to the
// bare command with the missing-project comment rather than erroring, so
// callers always have something to show.
func DisplayResumeCommandFor(src LaunchInfoSource, sessionID string) string {
	info, lastCwd, err := src.GetSessionLaunchInfo(sessionID)
	if err != nil {
		return DisplayResumeCommand("", sessionID, "", "", false)
	}
	return DisplayResumeCommand(info.Provider, sessionID, info.ProjectPath, lastCwd, false)
}
