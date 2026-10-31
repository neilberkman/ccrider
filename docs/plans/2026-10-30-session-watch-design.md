# Session watch: cross-harness monitoring design

Status: proposal, 2026-10-30

## Problem

A user running several agent harnesses (Claude Code, Codex, Antigravity, OpenCode, Copilot, pi) wants one session to wait on another and act when something happens: the other session finishes a turn, reports a result, or exits. Harness-native messaging (for example Claude Code's local session messaging) only reaches sessions of the same harness. ccrider already reads every supported harness's transcripts and maps live processes to sessions, so it can offer a harness-neutral mechanism.

## Goals

- A session in any MCP-capable harness can wait on a session in any harness ccrider imports.
- Standard MCP only for the baseline. Nothing in the core path depends on one harness's extensions or on a particular terminal multiplexer.
- No missed events. A watcher that disconnects, times out, or restarts resumes from where it left off.
- Low idle cost: no full sync per poll, no LLM tokens spent while nothing happens.
- Strict core/interface separation as described in `AGENTS.md`.

## Non-goals (v1)

- Sending input into a live session (A instructing B). There is no harness-neutral way to inject a turn into a running session; this is a separate design.
- Remote providers (Amp). Watching requires local transcript access.
- Harness-specific push channels (Claude Code channels, Codex app-server). These can be added later as optional delivery paths on top of the same core.

## Constraints from MCP

To verify against the current spec and client implementations:

1. MCP has no message that starts a new model turn. Server-to-client notifications (`notifications/resources/updated`, `notifications/progress`, `notifications/message`) are delivered to the client application; whether the model sees them is up to the host. Claude Code's channels feature exists as a non-standard extension for this reason.
2. Progress notifications: the spec allows a client to reset its request timeout when it receives `notifications/progress` for an in-flight request, while still enforcing a maximum. Client behavior varies.
3. Tasks (experimental, spec 2025-11-25) let a tool call return a handle the client polls. Whether a host starts a model turn when a task completes is host-defined, and client adoption in coding agents is unknown.
4. Client tool-call timeouts differ. Codex's default MCP `tool_timeout_sec` is believed to be 60 seconds.
5. ccrider uses `mark3labs/mcp-go` v0.41.1. It supports sending progress notifications to a client session. It declares a resource `subscribe` capability flag but has no `resources/subscribe` handler, and it has no task support.

Consequence: the only delivery mechanism that works in every MCP client is a tool call that returns. The design makes that call as close to push as the client allows: it holds open while the client tolerates it and returns the moment an event fires, and it degrades to resumable long polling everywhere else.

## Events

A watch waits for the first of one or more conditions on a target session:

| Condition | Fires when | Needs target cooperation |
|---|---|---|
| `turn_end` | The target completes an agent turn | No |
| `message` | Any new message of a given role appears (default: assistant) | No |
| `phrase` | A given phrase appears in a new message | Yes: target must say it |
| `signal` | A signal addressed to the watch's channel is posted | Yes: target calls a ccrider tool |
| `exit` | The target's agent process is no longer live | No |

### `turn_end`

Detection is provider-specific and lives with each provider's parser in `pkg/*sessions`. Each provider exposes a pure function over parsed messages that reports whether the transcript ends at a completed turn. Candidate markers, to confirm per provider:

- Claude Code: assistant message whose `stop_reason` is `end_turn` with no unresolved `tool_use`; or a turn-duration system entry if present.
- Codex: `event_msg` with `task_complete` / turn-complete type.
- Others: explicit markers where available.

Fallback for providers without a marker: last message is assistant text, no unresolved tool call, and no transcript writes for a quiescence window (default 10 s, configurable). Events from the fallback are flagged `confidence: "quiescence"` so the agent knows it may be a long-running tool rather than a finished turn.

Parsers currently drop these markers, so `ParsedMessage` gains a provider-neutral `TurnEnd bool` (or a small enum) populated at parse time. This also benefits the TUI and `ccrider ps` (showing "working" vs "waiting for input").

### `phrase`

Phrases are matched against messages that appear after the watch's start position, and by default only in assistant text. This avoids two false positives:

- The user's instruction to the target ("say `deploy-done-4f2a` when finished") lands in the target's transcript as a user message.
- Tool output that echoes the phrase (a grep, a file read).

`generate_session_anchor` already produces unique phrases; the watch tool can mint one when the caller asks for a phrase without supplying it, returning it so the caller can pass it to the target.

### `signal`

A small local mailbox. A new table:

```sql
CREATE TABLE watch_signals (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  channel TEXT NOT NULL,          -- opaque name chosen by the watcher, e.g. "deploy-done-4f2a"
  from_session_id TEXT,           -- optional, filled when the sender knows it
  body TEXT NOT NULL,             -- free text or JSON payload, size-capped
  created_at INTEGER NOT NULL
);
CREATE INDEX watch_signals_channel ON watch_signals(channel, id);
```

The target posts with an MCP tool (`send_session_signal`) or the CLI (`ccrider signal`). Signals carry a payload such as a summary or a result, so the watcher doesn't have to read the transcript. They don't depend on when a harness flushes its transcript. Retention: rows older than 7 days are pruned on write.

A signal channel is a capability-free shared name. Any local process can post to it. This matches ccrider's existing trust model (single local user, local SQLite) and is documented as such.

### `exit`

Uses `internal/core/liveness`. The target is considered exited when no live process maps to it for two consecutive scans. The liveness cache is shared across concurrent watches.

## Core API (`internal/core/watch`)

```go
type Condition struct {
    Kind   ConditionKind // TurnEnd, Message, Phrase, Signal, Exit
    Role   string        // Message/Phrase: "assistant" (default), "user", "any"
    Phrase string        // Phrase
    Channel string       // Signal
}

type Request struct {
    SessionID  string      // target; resolved like other tools (full or bare UUID)
    Conditions []Condition
    Cursor     string      // opaque; empty starts at "now"
    MaxWait    time.Duration
    Heartbeat  func(elapsed time.Duration) // called periodically while waiting
}

type Result struct {
    Status  string // "event" | "waiting" | "gone"
    Event   *Event
    Cursor  string // always set; pass back to continue
}

type Event struct {
    Kind       ConditionKind
    SessionID  string
    Provider   string
    At         time.Time
    Message    *MessageRef // role, sequence, excerpt; nil for Exit/Signal
    Signal     *Signal
    Confidence string // "marker" | "quiescence" | "exact"
}

func (w *Watcher) Wait(ctx context.Context, req Request) (Result, error)
```

### Cursor

The cursor is opaque to clients and self-contained, so the server keeps no per-watch state and a restarted MCP server or CLI resumes correctly. It encodes:

- target session id and provider
- transcript position: last seen message sequence and UUID, plus file identity (inode/device, byte offset, hash of the last consumed line) for file-backed providers
- last seen signal id per channel
- watch start time
- for `turn_end`, whether a turn has started since the cursor was created (so an already-idle target doesn't fire immediately unless the caller asks for `fire_if_idle`)

If the file identity or last-line hash doesn't match on resume (rewrite, revert, rotation), the watcher falls back to locating the last seen message UUID by reparsing, and if that fails, treats everything after the stored sequence number as new. Sequence numbers, not timestamps, define "new", which avoids clock and granularity problems.

### Change detection

- File-backed providers (Claude Code, Codex, pi, Antigravity CLI): watch the transcript file and its directory with fsnotify (kqueue on macOS, inotify on Linux, ReadDirectoryChangesW on Windows), then read and parse only bytes after the stored offset. A stat-based poll at 2 s is the fallback when fsnotify fails or the file is on a filesystem without notifications.
- Enumerated providers (OpenCode, Copilot): poll the backing store's mtime (including the WAL file) at 2 s; on change, re-enumerate only the target session where the provider package supports it, otherwise re-enumerate and filter.
- Remote providers: `Wait` returns an error saying watching isn't supported for that provider.
- The watcher does not run `SyncAll`. It upserts the target session into the DB after reading new messages, so other tools see the same data.

### Session identity changes

- Claude Code `/clear` or an in-session resume moves the process to a new session id. The watch stays bound to the original id; `exit` does not fire, because the process is still live. The liveness registry records the move, so the watcher detects it and returns `Status: "gone"` with the new session id, letting the agent decide whether to rewatch.
- Codex sub-agents: the watch targets the session the caller names. `ResumeVia` is reported but not followed.

## Interfaces

### MCP

`wait_for_session`:

- Args: `session_id`, `until` (list of `turn_end|message|phrase|signal|exit`), `phrase`, `channel`, `role`, `cursor`, `max_wait_seconds`.
- `max_wait_seconds` default 45, maximum 600. The default stays under the 60 s timeout believed to be Codex's default.
- If the request carries a progress token, the handler sends `notifications/progress` every 15 s. In clients that reset their timeout on progress, callers can raise `max_wait_seconds` and get a single held call that returns on the event.
- Returns `{status, event, cursor}`. On `waiting`, the tool description tells the agent to call again with the cursor. No event can be lost between calls.
- Returns immediately with an error if the target isn't found after one targeted sync, or if the provider is unsupported.

`send_session_signal`:

- Args: `channel`, `body`, optional `from_session_id`.
- Returns the signal id.

The description of `wait_for_session` explains the cooperative pattern: mint a channel or phrase, tell the user to give it to the target session, then wait.

### CLI

```
ccrider wait <session> --until turn_end,exit [--phrase P] [--channel C] [--cursor X] [--timeout 2h] [--follow]
ccrider signal <channel> <body>
```

- `wait` prints one JSON event and exits 0; exits 2 on timeout, printing the cursor; exits 1 on error.
- `--follow` keeps running and prints one JSON event per line until interrupted or the target is gone. This suits harnesses that can run a background command and react to its output or exit, at zero token cost while waiting.

### Later (not v1)

- `ccrider://session/{id}` resources with `resources/subscribe`, once mcp-go supports subscriptions. Cheap to offer; useful as soon as a host acts on update notifications.
- MCP tasks for `wait_for_session`, once mcp-go and major clients support them.
- Optional harness-specific delivery (Claude Code channels) as a separate MCP server mode, using the same core `Watcher`.

## Resource limits

- One fsnotify watch per target file shared across concurrent waits in the same process.
- Concurrent waits per MCP server capped (default 16); excess calls return an error.
- Signal body capped at 16 KiB.
- Excerpts in events capped (default 2 KiB); the agent reads full messages with `get_session_messages`.

## Phasing

1. Core watcher with `message`, `phrase`, `exit`, cursor format, file-backed change detection. MCP `wait_for_session` with progress heartbeats. CLI `wait`.
2. `turn_end` markers in each parser, quiescence fallback. `signal` table, `send_session_signal`, `ccrider signal`. Enumerated providers.
3. Resource subscriptions and tasks, gated on library and client support.

## Open questions

1. Should `turn_end` fire if the target is already idle when the watch starts? Proposed: no by default, `fire_if_idle` to opt in.
2. Should phrase matching also consider tool calls the target makes (for example the target running `echo PHRASE`)? Proposed: no; that's what `signal` is for.
3. Is a shared, unauthenticated signal namespace acceptable, or should channels be minted by the watcher with a random secret suffix? Proposed: mint by default (anchor generator), allow explicit names.
4. Does the held-call mode block other tool calls in some clients (serialized MCP requests per server)? If so, the description should steer agents in those clients toward short waits or the CLI.
