package session

import (
	"fmt"

	"github.com/neilberkman/ccrider/internal/core/config"
)

// Provider identifiers. These match the values stored in the sessions.provider
// column by the importer.
const (
	ProviderClaude      = "claude"
	ProviderCodex       = "codex"
	ProviderCopilot     = "copilot"
	ProviderOpenCode    = "opencode"
	ProviderPi          = "pi"
	ProviderAntigravity = "antigravity"
	ProviderAmp         = "amp"
)

type workingDirPolicy int

const (
	workingDirRequired workingDirPolicy = iota
	workingDirExistingPreferred
)

// providerInfo is the single description of everything ccrider knows about one
// coding agent's CLI: identity, resume invocation, config flags, and the
// display strings interfaces derive their provider lists from.
//
// Adding a provider means adding one entry to the providers table below —
// resume routing, validation messages, config flag selection, and every
// interface's provider list all derive from the table, so a new provider
// cannot be half-added.
type providerInfo struct {
	// Name is the stable id stored in sessions.provider.
	Name string
	// Binary is the CLI executable used to resume a session.
	Binary string
	// Product is the human-friendly product name, e.g. "Codex CLI".
	Product string
	// InstallHint is optional install guidance shown when Binary is missing.
	InstallHint string
	// SourceHint documents where sessions are imported from, for help text.
	SourceHint string
	// ConfigFlags selects this provider's extra-flags slice from the config
	// (claude_flags / codex_flags / ...).
	ConfigFlags func(cfg *config.Config) []string
	// BuildSpec builds the provider-specific resume invocation. flags are the
	// user-configured extra flags, injected at the position the CLI expects
	// its global options.
	BuildSpec func(sessionID string, fork bool, flags []string) ResumeSpec
	// SupportsFork reports whether ccrider can launch a fork directly.
	SupportsFork bool
	// WorkingDir controls whether the recorded project path is required for
	// resume. Cloud-backed providers can identify a session globally and only
	// prefer the recorded directory when it exists on this machine.
	WorkingDir workingDirPolicy
	// UsesFileIdentity reports whether incremental imports track source files
	// by inode and device and therefore participate in the one-time migration.
	UsesFileIdentity bool
}

// providers lists every supported provider in display order. The first entry
// (Claude) is also the fallback for unknown/empty provider values, matching
// the historical behavior of the switch statements this table replaced.
var providers = []providerInfo{
	{
		Name:        ProviderClaude,
		Binary:      "claude",
		Product:     "Claude Code",
		InstallHint: "npm install -g @anthropic-ai/claude-code",
		SourceHint:  "~/.claude/projects/",
		ConfigFlags: func(cfg *config.Config) []string { return cfg.ClaudeFlags },
		BuildSpec: func(sessionID string, fork bool, flags []string) ResumeSpec {
			prefix := fmt.Sprintf("claude%s --resume %s", joinFlags(flags), ShellQuote(sessionID))
			if fork {
				prefix += " --fork-session"
			}
			return ResumeSpec{
				Binary:        "claude",
				Prefix:        prefix,
				AcceptsPrompt: true,
			}
		},
		SupportsFork:     true,
		UsesFileIdentity: true,
	},
	{
		Name:        ProviderCodex,
		Binary:      "codex",
		Product:     "Codex CLI",
		SourceHint:  "~/.codex/sessions/",
		ConfigFlags: func(cfg *config.Config) []string { return cfg.CodexFlags },
		BuildSpec: func(sessionID string, fork bool, flags []string) ResumeSpec {
			// codex [OPTIONS] resume <id> [prompt]; forking uses `codex fork`.
			// Global options go BEFORE the subcommand (codex rejects them after).
			verb := "resume"
			if fork {
				verb = "fork"
			}
			return ResumeSpec{
				Binary:        "codex",
				Prefix:        fmt.Sprintf("codex%s %s %s", joinFlags(flags), verb, ShellQuote(stripToTrailingUUID(sessionID))),
				AcceptsPrompt: true,
			}
		},
		SupportsFork:     true,
		UsesFileIdentity: true,
	},
	{
		Name:        ProviderCopilot,
		Binary:      "copilot",
		Product:     "GitHub Copilot CLI",
		SourceHint:  "~/.copilot/session-state/",
		ConfigFlags: func(cfg *config.Config) []string { return cfg.CopilotFlags },
		BuildSpec: func(sessionID string, fork bool, flags []string) ResumeSpec {
			// copilot [flags] --resume=<id>; interactive resume has no positional prompt.
			return ResumeSpec{
				Binary:        "copilot",
				Prefix:        fmt.Sprintf("copilot%s --resume=%s", joinFlags(flags), ShellQuote(sessionID)),
				AcceptsPrompt: false,
			}
		},
	},
	{
		Name:        ProviderOpenCode,
		Binary:      "opencode",
		Product:     "OpenCode",
		SourceHint:  "~/.local/share/opencode/opencode*.db",
		ConfigFlags: func(cfg *config.Config) []string { return cfg.OpenCodeFlags },
		BuildSpec: func(sessionID string, fork bool, flags []string) ResumeSpec {
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
		},
		SupportsFork: true,
	},
	{
		Name:        ProviderPi,
		Binary:      "pi",
		Product:     "Pi",
		SourceHint:  "~/.pi/agent/sessions/",
		ConfigFlags: func(cfg *config.Config) []string { return cfg.PiFlags },
		BuildSpec: func(sessionID string, fork bool, flags []string) ResumeSpec {
			// pi [flags] --session <id> [prompt]; forking uses --fork <id>.
			id := ShellQuote(stripToTrailingUUID(sessionID))
			if fork {
				return ResumeSpec{
					Binary:        "pi",
					Prefix:        fmt.Sprintf("pi%s --fork %s", joinFlags(flags), id),
					AcceptsPrompt: true,
				}
			}
			return ResumeSpec{
				Binary:        "pi",
				Prefix:        fmt.Sprintf("pi%s --session %s", joinFlags(flags), id),
				AcceptsPrompt: true,
			}
		},
		SupportsFork:     true,
		UsesFileIdentity: true,
	},
	{
		Name:        ProviderAntigravity,
		Binary:      "agy",
		Product:     "Antigravity CLI",
		SourceHint:  "~/.gemini/antigravity-cli/brain/",
		ConfigFlags: func(*config.Config) []string { return nil },
		BuildSpec: func(sessionID string, fork bool, flags []string) ResumeSpec {
			// Antigravity branches from its interactive /fork command. Its CLI
			// has no direct fork flag, so ccrider only resumes the selected UUID.
			return ResumeSpec{
				Binary:        "agy",
				Prefix:        fmt.Sprintf("agy%s --conversation %s", joinFlags(flags), ShellQuote(sessionID)),
				AcceptsPrompt: false,
			}
		},
	},
	{
		Name:        ProviderAmp,
		Binary:      "amp",
		Product:     "Amp",
		InstallHint: "https://ampcode.com/manual",
		SourceHint:  "authenticated account via amp threads",
		ConfigFlags: func(*config.Config) []string { return nil },
		BuildSpec: func(sessionID string, fork bool, flags []string) ResumeSpec {
			return ResumeSpec{
				Binary:        "amp",
				Prefix:        fmt.Sprintf("amp threads continue %s", ShellQuote(sessionID)),
				AcceptsPrompt: false,
			}
		},
		WorkingDir: workingDirExistingPreferred,
	},
}

// lookupProvider returns the table entry for the given provider name.
// Unknown/empty providers fall back to Claude, matching the default branches
// of the switch statements the table replaced.
func lookupProvider(name string) providerInfo {
	for _, p := range providers {
		if p.Name == name {
			return p
		}
	}
	return providers[0]
}

// ResumeBinary returns the CLI executable used to resume a session for the
// given provider. Unknown/empty providers default to Claude.
func ResumeBinary(provider string) string {
	return lookupProvider(provider).Binary
}

// BuildResumeSpec returns provider-specific resume invocation details.
// flags are the user-configured extra flags for this provider's CLI
// (claude_flags / codex_flags / copilot_flags / opencode_flags / pi_flags), injected at
// the position each CLI expects its global options.
//
// Session IDs come from the database and are normally UUID/rollout-shaped,
// but every builder shell-quotes them anyway so an unexpected value can never
// break out of the command (defense-in-depth, since this string is run by a
// shell).
func BuildResumeSpec(provider, sessionID string, fork bool, flags []string) ResumeSpec {
	return lookupProvider(provider).BuildSpec(sessionID, fork, flags)
}

// ProviderFlags selects the configured extra-flags slice for the given
// provider's CLI. A nil cfg yields no flags. Unknown/empty providers map to
// Claude, matching BuildResumeSpec.
func ProviderFlags(cfg *config.Config, provider string) []string {
	if cfg == nil {
		return nil
	}
	return lookupProvider(provider).ConfigFlags(cfg)
}

// SupportsFork reports whether ccrider can start a forked session directly.
func SupportsFork(provider string) bool {
	return lookupProvider(provider).SupportsFork
}

// providerDisplayName returns a human-friendly name for the provider's CLI,
// including install guidance where useful.
func providerDisplayName(provider string) string {
	p := lookupProvider(provider)
	if p.InstallHint != "" {
		return fmt.Sprintf("%s (%s)", p.Product, p.InstallHint)
	}
	return p.Product
}

// ProviderNames returns the stable provider ids in display order, for
// interfaces that enumerate providers in help text or API descriptions.
func ProviderNames() []string {
	names := make([]string, len(providers))
	for i, p := range providers {
		names[i] = p.Name
	}
	return names
}

// FileIdentityProviderNames returns providers whose local session files need
// inode/device backfill during the one-time incremental-sync migration.
func FileIdentityProviderNames() []string {
	var names []string
	for _, p := range providers {
		if p.UsesFileIdentity {
			names = append(names, p.Name)
		}
	}
	return names
}

// ProviderProducts returns the human-friendly product names in display order.
func ProviderProducts() []string {
	products := make([]string, len(providers))
	for i, p := range providers {
		products[i] = p.Product
	}
	return products
}

// ProviderSources returns "Product (source location)" strings in display
// order, for help text that documents where sessions are imported from.
func ProviderSources() []string {
	sources := make([]string, len(providers))
	for i, p := range providers {
		sources[i] = fmt.Sprintf("%s (%s)", p.Product, p.SourceHint)
	}
	return sources
}
