package session

import (
	"fmt"
	"regexp"
	"strings"
)

// codexRolloutUUID matches the trailing UUID in a Codex rollout filename, e.g.
// "rollout-2026-06-04T14-40-56-019e93f0-3efe-7742-9598-bb06b36fb25a".
var codexRolloutUUID = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// codexResumeID returns the id that `codex resume` expects. ccrider stores Codex
// sessions under their rollout filename (e.g. "rollout-<timestamp>-<uuid>"), but
// codex resumes by the bare session UUID. If the stored id carries a trailing
// UUID, extract it; otherwise pass it through unchanged.
func codexResumeID(sessionID string) string {
	if m := codexRolloutUUID.FindString(sessionID); m != "" {
		return m
	}
	return sessionID
}

// Provider identifiers. These match the values stored in the sessions.provider
// column by the importer.
const (
	ProviderClaude  = "claude"
	ProviderCodex   = "codex"
	ProviderCopilot = "copilot"
)

// ResumeBinary returns the CLI executable used to resume a session for the
// given provider. Unknown/empty providers default to Claude.
func ResumeBinary(provider string) string {
	switch provider {
	case ProviderCodex:
		return "codex"
	case ProviderCopilot:
		return "copilot"
	default:
		return "claude"
	}
}

// ResumeSpec describes how to resume a session for a given provider.
//
// Prefix is the shell command up to (but excluding) any initial prompt
// argument. Callers append their own prompt handling so that prompt escaping
// (a shell concern) stays at the call site, e.g. a single-quoted literal or a
// "$(cat tmpfile)" command substitution.
type ResumeSpec struct {
	Binary string
	Prefix string
	// AcceptsPrompt reports whether an initial prompt may be appended to Prefix
	// as a positional argument. Copilot's interactive resume takes no prompt.
	AcceptsPrompt bool
}

// BuildResumeSpec returns provider-specific resume invocation details.
// claudeFlags are only applied for the Claude CLI.
func BuildResumeSpec(provider, sessionID string, fork bool, claudeFlags []string) ResumeSpec {
	// Session IDs come from the database and are normally UUID/rollout-shaped,
	// but shell-quote them anyway so an unexpected value can never break out of
	// the command (defense-in-depth, since this string is run by a shell).
	switch provider {
	case ProviderCodex:
		// codex resume <id> [prompt]; forking uses `codex fork <id> [prompt]`.
		verb := "resume"
		if fork {
			verb = "fork"
		}
		return ResumeSpec{
			Binary:        "codex",
			Prefix:        fmt.Sprintf("codex %s %s", verb, ShellQuote(codexResumeID(sessionID))),
			AcceptsPrompt: true,
		}
	case ProviderCopilot:
		// copilot --resume=<id>; interactive resume has no positional prompt.
		return ResumeSpec{
			Binary:        "copilot",
			Prefix:        fmt.Sprintf("copilot --resume=%s", ShellQuote(sessionID)),
			AcceptsPrompt: false,
		}
	default:
		flags := ""
		if len(claudeFlags) > 0 {
			flags = " " + strings.Join(claudeFlags, " ")
		}
		prefix := fmt.Sprintf("claude%s --resume %s", flags, ShellQuote(sessionID))
		if fork {
			prefix += " --fork-session"
		}
		return ResumeSpec{
			Binary:        "claude",
			Prefix:        prefix,
			AcceptsPrompt: true,
		}
	}
}

// ResumeCommand builds a one-line resume command for display or clipboard, with
// the prompt (if any) appended as a single-quoted argument. The prompt is
// dropped for providers that do not accept one.
func ResumeCommand(provider, sessionID, prompt string, fork bool, claudeFlags []string) string {
	spec := BuildResumeSpec(provider, sessionID, fork, claudeFlags)
	if prompt == "" || !spec.AcceptsPrompt {
		return spec.Prefix
	}
	return spec.Prefix + " " + ShellQuote(prompt)
}

// ShellQuote wraps s in single quotes, safely escaping any embedded single
// quotes, so it can be interpolated into a POSIX shell command as one argument.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
