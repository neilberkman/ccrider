package session

import (
	"strings"

	"github.com/neilberkman/ccrider/internal/core/config"
	"github.com/neilberkman/ccrider/internal/core/db"
)

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
	return ResumeCommandIn(projectPath, provider, sessionID, "", fork, ConfiguredFlags(provider))
}

// LaunchInfoSource looks up the stored launch info for a session id.
// *db.DB satisfies this.
type LaunchInfoSource interface {
	GetSessionLaunchInfo(sessionID string) (*db.Session, string, error)
}

// DisplayResumeCommandFor builds the display resume command for a session
// looked up by id. When launch info cannot be loaded, it falls back to the
// bare command with the missing-project comment rather than erroring, so
// callers always have something to show. The command uses the canonical
// session id from the lookup, not the caller's input, so a session looked
// up by bare UUID still resumes under its full provider id, or under the
// session recorded to resume in its place (see ResumeTarget).
func DisplayResumeCommandFor(src LaunchInfoSource, sessionID string) string {
	info, lastCwd, err := src.GetSessionLaunchInfo(sessionID)
	if err != nil {
		return DisplayResumeCommand("", sessionID, "", "", false)
	}
	return DisplayResumeCommand(info.Provider, ResumeTarget(info.SessionID, info.ResumeVia, false), info.ProjectPath, lastCwd, false)
}

// DisplaySummary turns a stored session summary into a one-line title capped
// at maxRunes runes, so every interface (CLI, TUI, MCP) shows the same title.
// A summary that falls back to the first prompt can span many lines or be a
// whole pasted document, so it drops one leading wrapper tag (such as
// <pasted_content>) and its closing tag, collapses all whitespace to single spaces and marks a
// cut with an ellipsis.
func DisplaySummary(summary string, maxRunes int) string {
	summary = strings.TrimSpace(summary)
	if strings.HasPrefix(summary, "<") {
		if end := strings.Index(summary, ">"); end >= 0 {
			tag := strings.Fields(summary[1:end])
			summary = summary[end+1:]
			if len(tag) > 0 {
				summary = strings.TrimSuffix(strings.TrimSpace(summary), "</"+tag[0]+">")
			}
		}
	}
	summary = strings.Join(strings.Fields(summary), " ")
	runes := []rune(summary)
	if len(runes) <= maxRunes {
		return summary
	}
	return string(runes[:maxRunes]) + "…"
}
