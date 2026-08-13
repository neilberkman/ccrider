package session

import (
	"path/filepath"
	"regexp"
	"strings"
)

// This file gives each provider a liveness identity: how to recognize one of
// its interactive processes in a process table, and how to pull a session id
// out of the command line when the process was started as a resume. Interfaces
// never parse argv themselves — they go through MatchLiveProcess.

// LiveProcessMatch is the result of matching one process command line against
// the provider table.
type LiveProcessMatch struct {
	// Provider is the stable provider name (sessions.provider value).
	Provider string
	// SessionID is the session id recovered from argv, or "" when the
	// process gave no id (a fresh, non-resumed session).
	SessionID string
}

// uuidRe matches the UUID every provider embeds in its session ids. Argv
// tokens claiming to be session ids must contain one, so free-text arguments
// (e.g. a prompt word "resume") can never be mistaken for an id.
var uuidRe = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)

// interpreters are runtimes whose argv[0] hides the real script/binary name
// one position to the right (e.g. "node /opt/homebrew/bin/codex resume ...").
var interpreters = map[string]bool{
	"node": true, "bun": true, "deno": true,
	"python": true, "python3": true,
	"sh": true, "bash": true, "zsh": true,
}

// liveSpec describes how to recognize a provider's interactive session
// process. A nil entry in the table means the provider has no observable
// per-session process (e.g. IDE-embedded providers).
type liveSpec struct {
	// exclude lists first-argument subcommands that are not interactive
	// sessions (servers, config commands) and must never be listed live.
	exclude []string
	// sessionID extracts the session id from the arguments following the
	// binary, returning "" when the process carries none.
	sessionID func(args []string) string
}

// liveSpecs is keyed by provider name. It lives next to the providers table:
// adding a provider without deciding its liveness behavior means it simply
// never appears in live listings, which is the safe default.
var liveSpecs = map[string]liveSpec{
	ProviderClaude: {
		exclude:   []string{"mcp", "config", "doctor", "update", "install", "setup-token", "migrate-installer"},
		sessionID: func(args []string) string { return flagUUIDValue(args, "--resume", "-r") },
	},
	ProviderCodex: {
		exclude:   []string{"app-server", "mcp", "mcp-server", "login", "logout", "completion", "apply"},
		sessionID: func(args []string) string { return subcommandUUIDValue(args, "resume", "fork") },
	},
	ProviderCopilot: {
		sessionID: func(args []string) string { return flagUUIDValue(args, "--resume") },
	},
	ProviderOpenCode: {
		exclude:   []string{"serve"},
		sessionID: func(args []string) string { return flagValue(args, "--session") },
	},
	ProviderPi: {
		sessionID: func(args []string) string {
			if id := flagUUIDValue(args, "--session"); id != "" {
				return id
			}
			return flagUUIDValue(args, "--fork")
		},
	},
	ProviderAntigravity: {
		sessionID: func(args []string) string { return flagUUIDValue(args, "--conversation") },
	},
	ProviderAmp: {
		sessionID: func(args []string) string {
			for i := 0; i+2 < len(args); i++ {
				if args[i] == "threads" && args[i+1] == "continue" && !strings.HasPrefix(args[i+2], "-") {
					return args[i+2]
				}
			}
			return ""
		},
	},
}

// MatchLiveProcess reports whether argv belongs to a provider's interactive
// session process, and if so which provider and (when recoverable) which
// session id. The binary is identified by the basename of argv[0], hopping
// over one interpreter (node, python, ...) when present — kernel process
// names are unreliable (Claude Code's is its version number).
func MatchLiveProcess(argv []string) (LiveProcessMatch, bool) {
	if len(argv) == 0 {
		return LiveProcessMatch{}, false
	}

	binary := filepath.Base(argv[0])
	args := argv[1:]
	if interpreters[binary] && len(args) > 0 {
		binary = filepath.Base(args[0])
		args = args[1:]
	}

	for _, p := range providers {
		if p.Binary != binary {
			continue
		}
		spec, ok := liveSpecs[p.Name]
		if !ok {
			return LiveProcessMatch{}, false
		}
		if len(args) > 0 {
			for _, sub := range spec.exclude {
				if args[0] == sub {
					return LiveProcessMatch{}, false
				}
			}
		}
		match := LiveProcessMatch{Provider: p.Name}
		if spec.sessionID != nil {
			match.SessionID = spec.sessionID(args)
		}
		return match, true
	}
	return LiveProcessMatch{}, false
}

// flagValue returns the value of the first present flag from names, handling
// both "--flag value" and "--flag=value" forms. Values starting with "-" are
// not values.
func flagValue(args []string, names ...string) string {
	for i, arg := range args {
		for _, name := range names {
			if v, ok := strings.CutPrefix(arg, name+"="); ok {
				return v
			}
			if arg == name && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				return args[i+1]
			}
		}
	}
	return ""
}

// flagUUIDValue is flagValue restricted to values containing a UUID, so bare
// flags and unrelated values are never mistaken for session ids.
func flagUUIDValue(args []string, names ...string) string {
	if v := flagValue(args, names...); uuidRe.MatchString(v) {
		return v
	}
	return ""
}

// subcommandUUIDValue returns the UUID-bearing token following the first
// occurrence of any of the given subcommand words.
func subcommandUUIDValue(args []string, subcommands ...string) string {
	for i, arg := range args {
		for _, sub := range subcommands {
			if arg == sub && i+1 < len(args) && uuidRe.MatchString(args[i+1]) {
				return args[i+1]
			}
		}
	}
	return ""
}
