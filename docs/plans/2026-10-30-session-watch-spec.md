# Session watch: specification

Status: spec, revision 4, 2026-10-30. Implemented on branch `feat/session-watch`. Supersedes `2026-10-30-session-watch-design.md`. Incorporates review rounds 1 to 3.

## Purpose

Let one agent session, in any MCP-capable harness, wait for something to happen in another session and act on it. ccrider already reads every supported harness's transcripts, so it can provide this without harness-specific messaging.

## Decisions since revision 2

- Claude Code `turn_end` stays in v1, implemented as a state machine with explicit outcomes and fixture tests (below).
- A Codex thread is watched as a logical thread: when its active rollout file changes (revert, replacement), the watch reports a gap and continues in the new rollout.
- mcp-go is upgraded from v0.41.1 to v1.5.0 (commit f09a496). v1.5.0 cancels in-flight handlers when stdio input ends and honors `notifications/cancelled`, so per-request cancellation and disconnect cleanup come from the library.
- Transcript conditions and signal conditions can't be mixed in one watch in v1.
- Passive watching in v1 covers Claude Code and Codex only. pi and Copilot move to later, since there's no local data to validate them against.

## Changes in revision 4

- Codex active rollout is chosen by observed write activity across every candidate rollout, not by `session_meta` timestamp. A superseded rollout can become active again.
- Codex `task_complete` with a non-null `error` is `failed`, not `completed`.
- The Claude decoder state carries everything an outcome depends on, and `unknown` is a real decoder state.
- The reader checks the consumed boundary again after reading, and event ids carry a generation that changes after every gap.
- Claude `turn_end` requires an interactive session. Headless SDK sessions don't write `turn_duration`.
- Signal reads and pruning are specified as atomic, and every variable-length field in a result has a limit.

## Guarantees

- For an append-only source, every event after an armed cursor is delivered at least once. Every event has a stable `event_id`; callers deduplicate on it.
- ccrider detects these source changes and reports each as a `gap` with a reason: file replaced, file truncated, last consumed record changed, Codex active rollout changed.
- Changes those checks can't see are outside the guarantee: an equal-length edit inside the already consumed region, records written and removed between two polls, and records on a Codex rollout that isn't the active one when two rollouts of the same thread are written within one 5 s locator interval. ccrider doesn't hash the full consumed prefix, because the files involved reach hundreds of megabytes.
- A gap means continuity was lost. It doesn't restore what was lost.
- Signals are delivered at least once while retained. A watcher that falls behind retention gets `cursor_expired`.
- Standard MCP gives no portable way to wake an idle agent. Waiting through the tool means re-calling it after each `waiting` result. Zero-token waiting depends on host features covered under delivery paths.

## v1 scope

| | Claude Code | Codex | OpenCode, Antigravity, Amp, pi, Copilot |
|---|---|---|---|
| `message`, `phrase` | yes | yes | no |
| `turn_end` | yes | yes | no |
| `signal` | yes | yes | yes |

A condition the target's provider doesn't support fails with `unsupported_condition`. ccrider never substitutes a heuristic.

Supported transcript formats:

- A Claude Code session's entrypoint must appear in its first 4 MiB, and a Codex rollout's `session_meta` must fit in its first 1 MiB. Real sessions put both on the first lines.

- Claude Code transcripts from interactive sessions, whose records carry `entrypoint: "cli"`. Evidence below is from Claude Code 2.1.295. In 150 recent transcripts, all 31 interactive ones contain `turn_duration` and none of the 117 headless ones (`entrypoint: "sdk-cli"`) do. `turn_end` on a headless session fails with `unsupported_format`; `message` and `phrase` still work. A transcript with no record carrying `entrypoint` yet also fails `turn_end` with `unsupported_format`, and the caller re-arms once it has started.
- Codex rollouts whose records carry `ordinal`. Older rollouts without ordinals fail with `unsupported_format`.

Out of v1: `exit` and session reassignment, quiet-period heuristics, pi, Copilot, OpenCode, Antigravity passive watching, MCP Tasks, `subscriptions/listen`, fsnotify, Claude Code channels.

## Events

Each source record decodes into zero or more events, in source order:

| Kind | Fields |
|---|---|
| `message` | visible assistant text, excerpt |
| `turn_ended` | `turn_id`, `outcome`, `reason`, `duration_ms`, excerpt |
| `signal` | channel, body, `from_session_id`, signal id |

`outcome` is one of `completed`, `refused`, `failed`, `interrupted`. An event also carries `partial: true` when the decoder started in the `unknown` state and the outcome or `turn_id` may be incomplete.

Turn starts are tracked internally for state and correlation but aren't exposed as a condition in v1.

Conditions select events:

- `message`: any `message` event.
- `phrase`: a `message` event whose visible text contains the phrase. Matching is literal and case-sensitive, on the full text before excerpt truncation. Text blocks within one record are joined with a newline. Empty phrases are rejected.
- `turn_end`: any `turn_ended` event.
- `signal`: any `signal` event on the given channel.

An event that satisfies several conditions appears once, with a `matched` list naming them.

### Visible assistant text

Claude Code: `text` blocks in `assistant` records. Excluded:

- `tool_use` and `thinking` blocks, tool results
- sidechain records
- compact summaries
- records with `isApiErrorMessage`, which the host writes, not the model

Codex: `response_item` records of type `message`, role `assistant`, `output_text` blocks. Excluded:

- `response_item` `agent_message`, which are messages between threads in multi-agent runs
- `event_msg` records of every type; supported rollouts carry assistant output in `response_item`
- reasoning, tool calls and outputs, `compacted`, developer and user messages
- records before `subagent_history_start_ordinal` in a child rollout, which are inherited context

### Claude Code turn decoder

State is `idle`, `running` or `unknown`. A running or unknown turn also carries:

- the start record's uuid, empty when unknown
- `refused`: a `model_refusal_no_fallback` has been seen in this turn
- `last_assistant_error`: whether the most recent assistant record in this turn has `isApiErrorMessage`

All of this is stored in the cursor, so decoding resumed from a cursor gives the same outcome as decoding without a break. Review round 3 reproduced the failure this prevents: an API error, then `scheduled_task_fire`, then `turn_duration`, decoded as `failed` continuously but `completed` after a cursor boundary between the last two.

`unknown` behaves like `running` with an empty start uuid, except that the end event carries `partial: true`, and a `turn_duration` or interrupt notice in `unknown` always emits, because the decoder can't tell it from a duplicate.

Transitions, applied to non-sidechain records in order:

| Record | When idle | When running |
|---|---|---|
| `user` prompt (text, not meta, not tool result) | start turn | none; prompt is queued |
| `assistant` record, including API errors | start turn; set `last_assistant_error` from the record | set `last_assistant_error` from the record |
| `system` `model_refusal_no_fallback` | none | mark refused |
| `system` `turn_duration` | none; duplicate boundary, dropped | end turn |
| `user` interrupt notice | none | end turn, `interrupted` |

A prompt is a non-sidechain `user` record with text that isn't `isMeta`, isn't a compact summary, carries no `tool_result`, and doesn't start with `<command-name>`, `<local-command-stdout>` or `<local-command-caveat>`. Those three mark slash commands and their output, which don't run the agent by themselves; a slash command that does run the agent produces an assistant record, which starts the turn. Task notifications (`<task-notification>`) are prompts.

The interrupt notice is a user record whose entire text is `[Request interrupted by user]` or `[Request interrupted by user for tool use]`. A user message that merely contains that text doesn't count.

The outcome when `turn_duration` ends a turn:

- `refused` if the turn saw `model_refusal_no_fallback`.
- `failed` if `last_assistant_error` is set.
- `completed` otherwise.

Stop-hook continuations stay in one turn: Claude writes `stop_hook_summary` and then one `turn_duration` after the last continuation.

After an interrupt in the tool-use form, Claude sometimes keeps working on prompts the user queued during the turn. The next assistant record starts a new turn under the "idle" column, so each loop gets its own end event.

`turn_id` is the uuid of the record that started the turn. `event_id` uses the uuid of the record that ended it.

Evidence from 150 recent local Claude Code transcripts:

| Sequence | Count |
|---|---|
| assistant, then `turn_duration`, then a new prompt | 2,653 |
| `stop_hook_summary`, then `turn_duration` | about 300 |
| API error, then `turn_duration` | 76 |
| `model_refusal_no_fallback`, then API error, then `turn_duration` | 22 |
| plain interrupt, then a new prompt | about 170 |
| tool-use interrupt, then a new prompt | 29 |
| tool-use interrupt, then more assistant output | 6 |
| `turn_duration`, then assistant output without a new prompt | about 100 |

The last row is turns started by task notifications and scheduled fires, which the "assistant record when idle" rule covers.

### Codex turn decoder

| Record | Effect |
|---|---|
| `event_msg` `task_started` | start turn with its `turn_id` |
| `event_msg` `task_complete`, `error` null | end turn, `completed`; excerpt from `last_agent_message` |
| `event_msg` `task_complete`, `error` set | end turn, `failed`; `reason` is the error code, else the error message, at most 200 bytes |

The error code is `error.codex_error_info`. Codex writes it as a string for variants without data (`"server_overloaded"`) and as a single-key object for variants with data (`{"http_connection_failed": {...}}`); the code is the variant name either way.
| `event_msg` `turn_aborted` | end turn, `interrupted`; `reason` from the record |

Review round 3 found 27 `task_complete` records with terminal errors (server overload, usage limits, policy, connection failures) in 134 local rollouts. Codex's own history database records those turns as failed.

An end record whose `turn_id` doesn't match the running turn still ends it and is reported with its own `turn_id`. `item_completed` isn't a turn boundary.

A `task_started` while a turn is running replaces it without an event. The earlier turn has no end record, and the decoder doesn't invent one. Codex's history database also leaves such turns in progress.

In a child rollout, records before `subagent_history_start_ordinal` are inherited context and are skipped for both lifecycle and messages.

`event_id` = rollout id + generation + ordinal. The rollout id comes from the file name. The generation is described under Continuity.

### Target state at arm time

Arming reports `target_state`: `running`, `idle`, or `unknown`. It's computed by scanning backward from the end of the source in 1 MiB chunks to the last turn boundary and replaying the decoder forward from there. The scan stops after 64 MiB; beyond that the state is `unknown`. The record that fixes the state is hashed from the same read that decoded it. After decoding forward, the scan checks that record and the final one again, and rescans, up to three times, if either changed or the path now names a different file. Codex rollouts can exceed 700 MB, so a forward scan from the start isn't an option.

A turn already running at arm time produces `turn_end` when it finishes, because its end record lands after the cursor. An idle target produces nothing until a later turn ends.

## Source identity

A watch target is a logical session:

- Claude Code: a session id, one transcript file.
- Codex: a thread id. A thread can have several rollout files: every file whose `session_meta.id` equals the thread id is a candidate.

The active Codex rollout is the candidate whose last record with a valid `timestamp` is latest. The search walks back record by record, reading each whole, so compacted records of several megabytes and corrupt lines after them don't hide a rollout's activity. It is bounded by the call's deadline, not by size. A corrupt last line doesn't make an active rollout look idle; the watch reports it as a corrupt record when it reads it. When a rollout's activity can't be established at all, that isn't treated as old: the current rollout stays current, and another such rollout is chosen only if no candidate's activity is known. Neither `session_meta` timestamps nor Codex's own `threads.rollout_path` decide it. Review round 3 found a thread whose newer replacement rollout had gone quiet while Codex, resumed on the original rollout, kept appending to it. Codex held the original open for writing and it grew by 450 KB during the review.

Resolution, at arm time and on every resume:

1. Resolve the caller's id to a canonical id with a DB query under the call's context: an exact match first, then a bare-UUID suffix match, which scans session ids.
2. Locate the source on disk with a provider locator, bounded by the call's deadline. Claude: glob each config directory's `projects/*/<id>.jsonl`. Codex: glob `sessions/*/*/*/rollout-*-<thread id>*.jsonl` and read each candidate's first record.
3. If the DB has no row, step 2 runs directly on the caller's id when it's a full UUID. There's no sync at arm time.

During a wait, the Codex locator reruns every 5 s and reads the last complete record of each candidate. If another candidate's last record is newer than the watched rollout's, the wait returns `gap` with reason `rollout_changed`, the new rollout id, and a cursor at the new rollout's current end, after reading the new rollout's records back to its last turn boundary to compute decoder state. A switch back to an earlier rollout is handled the same way. Ties keep the current rollout.

Activity between two locator runs on a rollout that isn't active can be missed: if two rollouts both receive records within one 5 s window, events on the one not watched are lost without a gap. This sits outside the guarantee, alongside the other limits listed there.

## Cursor

Opaque to callers: base64url of versioned JSON. It carries everything needed to continue, so the CLI and a restarted server can resume from the cursor alone.

| Field | Meaning |
|---|---|
| `v` | format version; unknown versions fail with `cursor_invalid` |
| `dv` | decoder version; a different decoder version fails with `cursor_invalid` and the caller re-arms |
| `until`, `phrase`, `channel` | the normalized conditions themselves |
| `sid`, `prov` | canonical target id and provider; empty for signal watches |
| `src` | Claude: session id. Codex: rollout id |
| `dev`, `ino` | file identity |
| `off` | byte offset just past the last consumed record |
| `tail` | SHA-256 of the last consumed record's bytes |
| `gen` | generation, starting at 0 and increased by every gap on this source |
| `ds` | decoder state: idle, running or unknown; turn start uuid or `turn_id`; `refused`; `last_assistant_error` |
| `sig` | last consumed signal id, for signal watches |

If a caller supplies conditions together with a cursor, they must equal the cursor's, or the call fails with `cursor_mismatch`.

Decoding also checks each kind's invariants and fails with `cursor_invalid` otherwise. A signal cursor needs a valid channel and a nonnegative `sig`, and carries no transcript fields. A transcript cursor carries no signal fields.

### Arming

A call without a cursor arms the watch. It returns immediately with `status: "armed"`, a cursor at the current end of the source or signal stream, and `target_state`. It never waits.

Arming mints a phrase or channel when asked, using the anchor generator. The intended flow:

1. Arm, getting a cursor and, if asked, a phrase or channel.
2. Tell the target session what to say or where to signal.
3. Wait with the cursor, repeating with each returned cursor.

### Reading

Each poll:

1. Open the file. Compare its identity with the cursor before anything else: device and inode on Unix, volume serial number and file index from the open handle on Windows. A mismatch returns `gap`, reason `replaced`.
2. Stat the open file. A size below `off` returns `gap`, reason `truncated`.
3. Read the record ending at `off` and compare its hash with `tail`. A mismatch returns `gap`, reason `rewritten`.
4. Note the size as this read's frontier. Read complete, newline-terminated records from `off` up to the frontier. An unterminated last line is left for a later poll.
5. Check the context between records and at least every 1 MiB.
6. After reading and before returning, repeat steps 1 to 3 on the same open file. If anything changed, discard the batch and return `gap` with the matching reason. This catches a rewrite that lands between the first check and the read; review round 3 reproduced one that otherwise skipped records without a gap.

Size and mtime are compared with the previous poll only to skip unchanged files. They never replace steps 1 to 3.

A malformed complete record returns `gap`, reason `corrupt`, with a cursor past it. The caller decides whether to read the transcript. Decoders parse strictly only the record types they act on, so a malformed field in an ignored record type isn't reported. Strict means JSON `null` is rejected where a string or list is required: message content, block types, text in text blocks, Codex payload types and roles, and Codex turn ids. encoding/json would otherwise accept `null` as empty. Because the skipped record could have started or ended a turn, the decoder state after it is `unknown` until a record that fixes the state on its own, and the next turn end carries `partial: true`. The backward scan treats records it can't decode the same way. Strict decoding rejected none of 1.5 million records in all 1,838 local Claude Code transcripts, headless and subagent ones included, nor any record in 1,447 Codex rollouts.

A `gap` result carries a fresh cursor and a fresh `target_state`. The fresh cursor's `gen` is one higher. Event ids built from positions or ordinals include `gen`, so records written after a truncation that reuse old ordinals or offsets get new ids, and a caller deduplicating by id doesn't drop them. Claude record uuids are unique per record and identify events without `gen`.

`gen` is per cursor. Two watchers of the same session can assign different ids to the same event after a gap. Deduplication is per watcher, so this doesn't matter.

The cursor's `off`, `tail`, `gen` and `ds` always describe the same boundary: the end of the last record whose events were returned, or the end of the last record read when nothing matched. The decoder state is snapshotted after each record, so a batch that stops before a record restores the snapshot from before it.

### Batching

- A record is consumed whole: all its events are returned together, or none are.
- Every variable-length field has a limit: channel and phrase at most 200 characters, `reason` at most 200 bytes, excerpt 2 KiB, `from_session_id` 200 bytes, and signal body 16 KiB measured after JSON encoding, so characters that expand when escaped count at their encoded size. Results are encoded without HTML escaping, and event sizes are measured the same way. With those limits a single record's events fit in any byte budget an interface uses, so the "first record always fits" rule can't produce an oversized response.
- The batch stops at the first record that would take it past 20 events or 256 KiB of serialized events. The MCP interface passes a smaller byte budget, ccrider's server-wide response cap of about 36 KB less room for the other fields. If the first record alone exceeds either limit, it's still returned. Excerpts are capped at 2 KiB and signal bodies at 16 KiB, so a single record's events stay small.
- The returned cursor points just past the last returned record.
- When nothing matched, the call returns `waiting` with a cursor past all records read, so non-matching history isn't reread.

A cursor reused against an unchanged source returns the same events with the same ids, in the same order. A call whose deadline cuts the read short returns the events collected so far, or, if none matched, a cursor past the records it read, so short waits still work through a large backlog. A replay can therefore return a prefix of an earlier batch, never different events. Against a source that has grown since, it returns the same events first, possibly followed by more.

## Signals

Tables, created by a migration in `internal/core/db`:

```sql
CREATE TABLE watch_signals (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  channel TEXT NOT NULL,
  idempotency_key TEXT,
  from_session_id TEXT,
  body TEXT NOT NULL,
  body_sha256 TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  UNIQUE (channel, idempotency_key)
);
CREATE INDEX watch_signals_channel ON watch_signals(channel, id);

CREATE TABLE watch_signal_channels (
  channel TEXT PRIMARY KEY,
  pruned_through_id INTEGER NOT NULL
);
```

- Signals are retained for 7 days. Pruning runs on insert. In one write transaction it raises each affected channel's `pruned_through_id` to the highest id it's about to delete there, then deletes the rows.
- A wait reads the channel's `pruned_through_id` and the signals after its cursor in one read transaction, so both come from the same snapshot. If `pruned_through_id` exceeds the cursor's `sig`, the wait returns `cursor_expired` with a fresh cursor, whatever signals it read.
- Arming sets `sig` to the highest signal id ever assigned, from SQLite's AUTOINCREMENT sequence. That is never below any channel's `pruned_through_id`, so a fully pruned channel can't make a new watch look expired.
- An idempotency key reused with the same body returns the existing signal with `duplicate: true`. Reused with a different body, the send fails with `idempotency_conflict`. Keys last as long as their signal is retained.
- Body is capped at 16 KiB of JSON-encoded size and must be valid UTF-8. An explicit empty body is allowed. A missing or invalid body fails with `invalid_body`.
- Channels are broadcast streams. Every watcher reads with its own cursor.
- `from_session_id` is reported as a claim. ccrider doesn't verify it.
- Minted channel names are random to avoid collisions. They aren't secrets. Any local process can post to any channel, consistent with ccrider's single-user local trust model.

Signal watches don't need a target session. They work with any harness that has ccrider's MCP server or can run the CLI.

## Waiting

- Poll interval 1 s.
- No DB transaction is held across a poll. Signal reads are single indexed queries under the call's context.
- The wait reads the source directly and doesn't import into the DB.
- The wait ends at the earlier of its deadline and cancellation of its context.

## Core / interface split

Core:

- `pkg/ccsessions`: a reader returning complete lines with byte offsets, leaving an unterminated tail unread, checking a context between chunks.
- `pkg/sessionevents`: event types and the decoder interface.
- `pkg/ccsessions` and `pkg/codexsessions`: turn decoders and visible-text extraction, next to the existing parsers. The existing parsers are unchanged.
- `internal/core/db`: signal tables and queries; canonical id resolution with context.
- `internal/core/watch`: provider locators, arm, wait, cursor encoding and validation, continuity checks, batching, backward state scan, support matrix. Event and byte limits are parameters, with defaults.

Interfaces:

- MCP: argument parsing, deadline from `max_wait_seconds`, progress notifications, admission control, JSON shape.
- CLI: flags, exit codes, output, `--follow` looping.

## MCP interface

### `wait_for_session`

| Argument | Notes |
|---|---|
| `session_id` | required for transcript conditions; not allowed for signal watches |
| `until` | `message`, `phrase`, `turn_end`; or `signal` alone |
| `phrase` | literal phrase; or `mint_phrase: true` at arm time |
| `channel` | channel name; or `mint_channel: true` at arm time |
| `cursor` | omit to arm |
| `max_wait_seconds` | default 20, maximum 600; covers resolution, reading and waiting |

```json
{
  "status": "armed | event | waiting | gap | cursor_expired | busy",
  "events": [{ "event_id": "...", "kind": "turn_ended", "matched": ["turn_end"],
               "session_id": "...", "provider": "codex", "at": "...",
               "turn_id": "...", "outcome": "completed", "reason": null,
               "excerpt": "...", "signal": null }],
  "cursor": "...",
  "phrase": "...",
  "channel": "...",
  "target_state": "running | idle | unknown",
  "gap_reason": null,
  "rollout_id": null
}
```

Errors: `not_found`, `unsupported_condition`, `unsupported_format`, `cursor_invalid`, `cursor_mismatch`.

- The deadline starts when the call arrives and covers arming as well as waiting.
- The default of 20 s stays under the shortest known client tool timeout, Copilot CLI's 30 s. The tool description tells agents that longer waits need a client configured for them.
- When the request has a progress token, the handler sends progress notifications every 10 s while waiting, with strictly increasing `progress`, and stops when the call returns. Arming doesn't wait and sends none. Nothing depends on a client resetting its timeout because of them. Codex's current source logs progress notifications without resetting its timeout.
- The stdio server runs with 16 workers. At most 8 waits run at once. A wait beyond that returns `busy` at once, with the caller's cursor unchanged.
- Client cancellation and disconnection end the wait through mcp-go v1.5.0's request context, with one exception. When all workers are busy and the request queue of 100 is full, mcp-go runs the next tool call on its read loop. While that call runs, the server doesn't read cancellations or notice disconnection. A wait in that position still ends at its deadline. Reaching this needs 116 concurrent requests.

### `send_session_signal`

Arguments: `channel` and `body`, both required; `idempotency_key`; `from_session_id`. Returns `{signal_id, duplicate}`. Errors: `idempotency_conflict`, `invalid_body`.

## CLI interface

```
ccrider wait arm <session> --until turn_end[,message,phrase] [--phrase P | --mint-phrase]
ccrider wait arm --signal [--channel C | --mint-channel]
ccrider wait <cursor> [--timeout 2h]
ccrider wait <cursor> --follow
ccrider signal <channel> <body> [--key K] [--from SESSION]
```

- `wait <cursor>` prints one JSON result. Exit codes: 0 events, 2 timeout, 3 gap or expiry, 1 error. The printed cursor continues the watch.
- `--follow` prints one JSON result per line and continues from each returned cursor. It stops on a gap or expiry unless `--through-gaps` is given.
- The CLI has no worker limit and its own timeout. It suits harnesses that can run a background command and resume when it exits or prints.

## Delivery paths

| Path | Who | Cost while waiting |
|---|---|---|
| `wait_for_session` re-called with cursor | any MCP client | one model step per call |
| `ccrider wait` as a background command | harnesses with a background shell that wakes the agent on exit or output | none |
| MCP Tasks, later | clients implementing the Tasks extension | none where the host resumes the agent on completion |

### Client compatibility, as of 2026-10-30

| Client | Tool timeout | Progress | MCP Tasks |
|---|---|---|---|
| Claude Code | about 28 h wall clock; idle limit 30 min on stdio | refreshes idle activity, not the wall-clock limit | beta from 2.1.304, with surface and setting restrictions; documented to resume an idle agent when a task completes |
| Codex | 60 s, `tool_timeout_sec`; elicitation pauses it | logged, doesn't reset | unverified |
| Copilot CLI | 30 s | unverified | unverified |
| Cursor, VS Code | unverified | unverified | unverified |

Sources: [Claude Code MCP docs](https://code.claude.com/docs/en/mcp#time-limits-on-mcp-tool-calls), [Codex MCP configuration](https://learn.chatgpt.com/docs/extend/mcp?surface=cli), [Codex timeout implementation](https://raw.githubusercontent.com/openai/codex/main/codex-rs/rmcp-client/src/rmcp_client.rs), [Copilot CLI reference](https://docs.github.com/en/copilot/reference/copilot-cli-reference/cli-command-reference#mcp-server-configuration), [Cursor MCP docs](https://cursor.com/docs/mcp#protocol-and-extension-support).

mcp-go v1.5.0 supports task augmentation on tools, so a Tasks-based `wait_for_session` for Claude Code is a library-supported phase-2 item.

## MCP standard notes

- Current revision 2026-07-28. It replaces `resources/subscribe` with `subscriptions/listen`, moves Tasks into the `io.modelcontextprotocol/tasks` extension, and deprecates sampling. Sources: [versioning](https://modelcontextprotocol.io/docs/2026-07-28/learn/versioning), [changelog](https://modelcontextprotocol.io/specification/2026-07-28/changelog), [Tasks](https://modelcontextprotocol.io/extensions/tasks/overview).
- MCP doesn't guarantee a portable way to resume an existing host conversation. Server notifications reach the client application, which decides what the model sees. Tasks define asynchronous protocol behavior; the host decides how to act on completion.

## Deferred

### `exit` and reassignment

Prerequisites in `internal/core/liveness`:

- Expose every process-to-session association, not only the most recent declared session per process.
- Track process identity as PID plus start time.
- Distinguish `process_exited`, `session_detached`, `session_reassigned` with the new id, and `unknown`. Cwd-only matches are `unknown`. Missing evidence is never reported as exit.
- Require two independent scans, not two reads of the 5 s cache.
- Antigravity IDE and desktop have no process mapping and stay unsupported.

### Other

- Tasks-based `wait_for_session`.
- Mixed transcript and signal watches, with fair allocation across sources.
- pi and Copilot, once real transcripts are available to validate decoders.
- OpenCode with row revision tracking; Antigravity once a turn marker is established.
- fsnotify as a wakeup hint, with polling kept as reconciliation.
- `turn_started` as a public condition, once it can be tied to a specific message.

## Acceptance tests

Fixtures are records with the shape of real transcript records and the content replaced, written inline in the tests.

Claude Code decoder, one fixture each:

1. Normal turn ending in `turn_duration`.
2. Stop-hook continuation: one `turn_ended`.
3. Refusal: `refused`.
4. API error: `failed`.
5. Plain interrupt: `interrupted`.
6. Tool-use interrupt followed by work on queued prompts: `interrupted`, then a second turn that completes.
7. Turn started by a task notification without a prompt.
8. `turn_duration` with no activity since the last end: no event.
9. User message containing the interrupt text inside other text: not an interrupt.
9a. API error, `scheduled_task_fire`, `turn_duration`, with a cursor boundary before `turn_duration`: `failed`, same as without the boundary.
9b. Arm with the last boundary beyond the backward-scan limit: the next end event has `partial: true`.
9c. Headless (`sdk-cli`) transcript: `turn_end` fails with `unsupported_format`; `message` works.

Codex decoder:

10. `task_complete` with excerpt; `task_complete` with a terminal error gives `failed` with a reason; `turn_aborted` with reason; `task_started` while running gives no event.
11. Child rollout: records before `subagent_history_start_ordinal` produce nothing.
12. Rollout replacement: `gap` with `rollout_changed`, then events from the new rollout with different ids for reused ordinals. Resuming the original rollout afterwards: `gap` with `rollout_changed` back to it.
13. Rollout without ordinals: `unsupported_format`.

Reading and cursors:

14. Arm, then a turn already running finishes: event delivered.
15. Arm while idle: nothing until a new turn ends.
16. Two turns ending between polls: both delivered.
17. Unterminated last line: not consumed; consumed after completion.
18. Replacement, truncation, truncate-and-regrow past the offset, rewrite of the last consumed record: each returns its `gap` reason.
18a. An in-place rewrite of the consumed boundary between the first check and the read: `gap`, `rewritten`, batch discarded.
18b. Truncation followed by new records reusing ordinals: the new records get ids with the next generation.
19. Malformed complete record: `gap` with `corrupt`.
20. Batch limit reached partway: the record that would exceed it is left whole for the next call; nothing lost or repeated.
21. One record satisfying `message` and `phrase`: one event, both in `matched`.
22. Response over 256 KiB of events: split at a record boundary.
23. Cursor reused against an unchanged source: identical events and ids.
24. Cursor with different conditions supplied: `cursor_mismatch`.
25. Backward state scan on a file larger than the scan limit: `unknown`.
26. Phrase in a user message, tool output, tool input, thinking, compact summary, sidechain, API error record, or Codex inter-agent message: no match.

Signals:

27. Same idempotency key and body: one row, `duplicate`. Same key, different body: `idempotency_conflict`.
28. Cursor behind `pruned_through_id`: `cursor_expired`. Pruning committed between a reader's watermark check and its signal fetch can't happen, because both run in one read transaction; a test interleaves a prune with a read to confirm. Arming on a fully pruned channel isn't expired.
29. Two watchers on one channel each receive every signal.

MCP:

30. Nine concurrent waits: the ninth returns `busy`; a search during eight waits starts without waiting for a worker. Search itself syncs before querying, which takes as long as it takes.
31. Client cancels a wait: the handler returns and its ticker stops.
32. Stdio closes during a wait: handlers return before the server exits.
33. `max_wait_seconds` 20 returns within 20 s, including resolution.
34. Progress notifications have strictly increasing values and stop after the result.

## Open questions

None. Resolved in revision 4:

1. A turn that ends because its process died has no end record, in Claude or Codex. Reporting that is left to the deferred `exit` condition. `target_state` is the last observed transcript state, not evidence that a process is alive.
2. Codex child threads are watched by their own id. A parent watch doesn't report child turn ends.
