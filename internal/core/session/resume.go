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
	ProviderClaude   = "claude"
	ProviderCodex    = "codex"
	ProviderCopilot  = "copilot"
	ProviderOpenCode = "opencode"
)

// ResumeBinary returns the CLI executable used to resume a session for the
// given provider. Unknown/empty providers default to Claude.
func ResumeBinary(provider string) string {
	switch provider {
	case ProviderCodex:
		return "codex"
	case ProviderCopilot:
		return "copilot"
	case ProviderOpenCode:
		return "opencode"
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
	Binary     string
	Prefix     string
	PromptFlag string
	// AcceptsPrompt reports whether an initial prompt may be appended to Prefix
	// using PromptFlag or, when PromptFlag is empty, as a positional argument.
	// Copilot's interactive resume takes no prompt.
	AcceptsPrompt bool
}

// joinFlags renders configured flags as a command fragment with a leading
// space, or "" when there are none.
func joinFlags(flags []string) string {
	if len(flags) == 0 {
		return ""
	}
	return " " + strings.Join(flags, " ")
}

// BuildResumeSpec returns provider-specific resume invocation details.
// flags are the user-configured extra flags for this provider's CLI
// (claude_flags / codex_flags / copilot_flags / opencode_flags), injected at
// the position each CLI expects its global options.
func BuildResumeSpec(provider, sessionID string, fork bool, flags []string) ResumeSpec {
	// Session IDs come from the database and are normally UUID/rollout-shaped,
	// but shell-quote them anyway so an unexpected value can never break out of
	// the command (defense-in-depth, since this string is run by a shell).
	switch provider {
	case ProviderCodex:
		// codex [OPTIONS] resume <id> [prompt]; forking uses `codex fork`.
		// Global options go BEFORE the subcommand (codex rejects them after).
		verb := "resume"
		if fork {
			verb = "fork"
		}
		return ResumeSpec{
			Binary:        "codex",
			Prefix:        fmt.Sprintf("codex%s %s %s", joinFlags(flags), verb, ShellQuote(codexResumeID(sessionID))),
			AcceptsPrompt: true,
		}
	case ProviderCopilot:
		// copilot [flags] --resume=<id>; interactive resume has no positional prompt.
		return ResumeSpec{
			Binary:        "copilot",
			Prefix:        fmt.Sprintf("copilot%s --resume=%s", joinFlags(flags), ShellQuote(sessionID)),
			AcceptsPrompt: false,
		}
	case ProviderOpenCode:
		// opencode [flags] --session <id> [--fork] [--prompt <prompt>].
		prefix := fmt.Sprintf("opencode%s --session %s", joinFlags(flags), ShellQuote(sessionID))
		if fork {
			prefix += " --fork"
		}
		return ResumeSpec{
			Binary:        "opencode",
			Prefix:        prefix,
			PromptFlag:    "--prompt",
			AcceptsPrompt: true,
		}
	default:
		prefix := fmt.Sprintf("claude%s --resume %s", joinFlags(flags), ShellQuote(sessionID))
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

// AppendPromptArg appends an already shell-safe prompt argument to a resume
// command according to the provider's CLI contract.
func AppendPromptArg(cmd string, spec ResumeSpec, promptArg string) string {
	if promptArg == "" || !spec.AcceptsPrompt {
		return cmd
	}
	if spec.PromptFlag != "" {
		return cmd + " " + spec.PromptFlag + " " + promptArg
	}
	return cmd + " " + promptArg
}

// ResumeCommand builds a one-line resume command for display or clipboard, with
// the prompt (if any) appended in the form required by the provider. The prompt
// is dropped for providers that do not accept one.
func ResumeCommand(provider, sessionID, prompt string, fork bool, flags []string) string {
	spec := BuildResumeSpec(provider, sessionID, fork, flags)
	if prompt == "" || !spec.AcceptsPrompt {
		return spec.Prefix
	}
	return AppendPromptArg(spec.Prefix, spec, ShellQuote(prompt))
}

// ResumeCommandIn builds the full one-line resume command for display or
// clipboard, prefixed with `cd <projectPath> && ` so it works from any cwd.
// Every provider scopes session storage by the directory the session was
// created in (Claude under ~/.claude/projects/<encoded-cwd>/, Codex by cwd in
// session_meta), so a resume command without the cd prefix fails outside the
// project directory.
//
// If projectPath is empty (missing in the DB), the bare command is returned
// with a trailing comment noting the gap rather than erroring.
func ResumeCommandIn(projectPath, provider, sessionID, prompt string, fork bool, flags []string) string {
	cmd := ResumeCommand(provider, sessionID, prompt, fork, flags)
	if projectPath == "" {
		return fmt.Sprintf("%s # project path missing in DB for session %s", cmd, sessionID)
	}
	return fmt.Sprintf("cd %s && %s", ShellQuote(projectPath), cmd)
}

// ShellQuote wraps s in single quotes, safely escaping any embedded single
// quotes, so it can be interpolated into a POSIX shell command as one argument.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
