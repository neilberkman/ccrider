package session

import (
	"fmt"
	"regexp"
	"strings"
)

// trailingUUID matches a UUID at the end of a stored session id stem, e.g.
// Codex "rollout-<timestamp>-<uuid>" or Pi "<timestamp>_<uuid>" filenames.
var trailingUUID = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// stripToTrailingUUID returns the bare UUID some provider CLIs expect. ccrider
// stores file-based sessions by filename stem in the DB; for providers whose
// CLIs resolve the metadata UUID, this strip is load-bearing.
func stripToTrailingUUID(sessionID string) string {
	if m := trailingUUID.FindString(sessionID); m != "" {
		return m
	}
	return sessionID
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
// clipboard. Providers that scope sessions to a project get a
// `cd <projectPath> && ` prefix. Globally-addressed cloud sessions use the
// recorded project path only when it exists on this machine.
//
// If projectPath is empty (missing in the DB), the bare command is returned
// with a trailing comment noting the gap rather than erroring.
func ResumeCommandIn(projectPath, provider, sessionID, prompt string, fork bool, flags []string) string {
	cmd := ResumeCommand(provider, sessionID, prompt, fork, flags)
	workDir := recordedWorkingDir(provider, projectPath)
	if workDir == "" && lookupProvider(provider).WorkingDir == workingDirExistingPreferred {
		return cmd
	}
	if projectPath == "" {
		return fmt.Sprintf("%s # project path missing in DB for session %s", cmd, sessionID)
	}
	return fmt.Sprintf("cd %s && %s", ShellQuote(workDir), cmd)
}

// ShellQuote wraps s in single quotes, safely escaping any embedded single
// quotes, so it can be interpolated into a POSIX shell command as one argument.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
