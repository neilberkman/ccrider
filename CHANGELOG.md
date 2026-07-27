# Changelog

## [Unreleased]

## [1.7.0] - 2026-07-27

### Added

- **Amp thread support** — ccrider indexes threads from your authenticated Amp account through the Amp CLI, covering active and archived threads, and participates in sync, search, list, export, TUI, and MCP workflows with `[amp]` provider tags and `--provider amp` filtering. Sync is incremental: a lightweight thread listing detects changes and only new or changed threads are exported. Both current and legacy Amp export schemas are supported
- **Amp resume support** — resumes with `amp threads continue <thread-id>`. Because Amp threads are addressed globally rather than by directory, ccrider resumes in the recorded project directory when it exists on this machine and falls back to the current directory when the thread came from an orb or another machine
- **`amp_enabled` config option** — Amp is cloud-backed, so importing it downloads thread text into the local database. The integration is off by default and requires `amp_enabled = true` in `~/.config/ccrider/config.toml`. MCP request syncs never contact Amp; they search Amp data already cached by an earlier CLI or TUI sync

### Changed

- **Sync respects cancellation** — Ctrl-C during a sync now stops promptly instead of running to completion, and remote imports carry bounded deadlines
- **Import failures are reported per session** — the importer returns structured per-session outcomes instead of printing warnings from the core, so each interface renders them in its own voice, and progress accounts for failed units rather than stalling

### Fixed

- **A failed session no longer breaks MCP queries** — an incomplete or timed-out background sync is logged and the request is served from cached data, instead of failing the tool call outright
- **One-time inode migration ignores providers that have no session files** — cloud-backed and database-backed providers no longer skew the check that decides whether a backfill is needed

## [1.6.2] - 2026-07-27

### Fixed

- **Sync no longer re-reads unchanged sessions** — a transcript whose content was unchanged but whose recorded timestamp had drifted (an in-place rewrite, a restore, a cloud-drive resync) was fully re-read and re-hashed on every sync, forever. The cheap timestamp/size check now re-arms once the hash proves the file unchanged. On a store of ~3700 Codex transcripts this cut a routine sync from 26 seconds to 29 milliseconds
- **Progress bar tracks the whole sync** — the bar only advanced on imports, so a routine sync that skips 98% of files sat at 0% for its entire run and looked hung. Skipped and unreadable files now advance it too
- **Completion line reports real imports** — `Completed: Imported N sessions` printed the total file count rather than the number actually imported, claiming every file was imported when almost none were

## [1.6.1] - 2026-07-16

### Changed

- **Sync output keeps stdout clean** — all sync progress and status messages now write to stderr, and when output is not a terminal the animated progress bar is replaced with plain one-line-per-10% updates, so cron jobs and CI logs no longer fill with redraw frames
- **Piped bare invocation prints help** — `ccrider | ...` with stdout redirected shows help text instead of failing with a TTY error
- Root help now links to the GitHub issues page

### Fixed

- **Errors print once and cleanly** — runtime failures no longer print the error twice or dump the full usage block after it
- **Unknown session IDs report a human-readable error** — `session not found: <id>` instead of leaking `sql: no rows in result set`

## [1.6.0] - 2026-07-10

### Added

- **Antigravity CLI session support** — indexes canonical Antigravity transcripts from `~/.gemini/antigravity-cli/brain/`, preserving workspace associations from its local history index and avoiding collisions from shared `transcript.jsonl` filenames. Antigravity sessions participate in sync, search, list, TUI, and MCP workflows with `[antigravity]` provider tags and `--provider antigravity` filtering
- **Antigravity resume support** — ccrider resumes Antigravity CLI sessions with `agy --conversation <id>`. Because Antigravity only branches interactively with `/fork`, the TUI omits its direct fork shortcut for those sessions

## [1.5.1] - 2026-07-04

### Changed

- **Provider-neutral session display** — TUI and CLI list output now tag Claude sessions with `[claude]` just like Codex, Copilot, OpenCode, and Pi. TUI help/title text, sync output, MCP/export help, and resume prompt docs now describe coding agents/providers instead of treating Claude Code as the baseline

## [1.5.0] - 2026-07-02

### Added

- **Pi session support** — indexes Pi sessions from `~/.pi/agent/sessions/` alongside Claude Code, Codex CLI, GitHub Copilot CLI, and OpenCode. Pi sessions participate in sync, search, list, TUI, MCP, and resume workflows with `[pi]` provider tags and `--provider pi` filtering
- **Pi resume support** — ccrider resumes Pi sessions with `pi --session <id>`, forks with `pi --fork <id>`, appends resume prompts positionally, and supports optional `pi_flags` in `config.toml`

### Changed

- **Provider registry** — provider metadata, resume binary lookup, resume specs, config flag lookup, source hints, and help-text provider lists now derive from a single core registry instead of scattered switch statements
- **Shared JSONL parser utilities** — Claude, Codex, Copilot, OpenCode, and Pi parsers now share the JSONL read loop, deterministic UUID helper, first-user summary fallback, and text item extraction helpers

### Fixed

- **TUI shortcut toolbar positioning** — list and detail views now size their scrollable areas so the keyboard shortcut toolbar stays anchored to the final terminal line. The session list also uses `lipgloss.JoinVertical` to avoid manual newline trimming that could leave extra gaps. Thanks @Evidlo

## [1.4.0] - 2026-06-28

### Added

- **OpenCode session support** — indexes OpenCode sessions from `~/.local/share/opencode/opencode*.db` alongside Claude Code, Codex CLI, and GitHub Copilot CLI. OpenCode sessions participate in normal sync, search, list, TUI, and MCP workflows, with `[opencode]` provider tags and `--provider opencode` filtering
- **OpenCode resume support** — ccrider can now resume OpenCode sessions with `opencode --session <id>`, including forked resumes via `--fork` and prompt prefill via `--prompt`. `opencode_flags` in `config.toml` works the same way as the existing per-provider resume flags
- **OpenCode SQLite parser** — new `pkg/opencodesessions` parser reads the current OpenCode DB schema directly, handles text/tool/subtask/file parts, and uses content hashes for incremental sync instead of shared DB mtimes

### Fixed

- **Optional provider failures no longer abort full sync** — OpenCode schema/read errors are reported as warnings and skipped, so a future upstream DB change cannot block Claude/Codex/Copilot imports
- **Resume prompt shell quoting is shared across provider launch paths** — temp-file prompt substitution is now quoted consistently for Claude and generic agent commands

## [1.3.2] - 2026-06-19

### Fixed

- **Session sort no longer counts non-message events as activity** — `updated_at` was computed as the max timestamp across *every* parsed transcript line, including `pr-link`, `file-history-snapshot`, `queue-operation`, and `summary` entries that carry a timestamp but aren't conversation. A PR-link stamped days after the last real message would float a stale session to the top of the last-active sort (this affected ~35% of a large session corpus). `created_at`/`updated_at` are now derived only from `user`/`assistant` turns. Run `ccrider sync --force` once to recompute existing rows
- **Help screen (`?`) is readable on any terminal theme** — the help text rendered through a single dim-gray style (`240`/`244`) that washed out on dark backgrounds. The body now uses the terminal's default foreground (guaranteed to contrast on light, dark, and low-contrast schemes), with bold adaptive accents on the title and section headings for hierarchy

## [1.3.1] - 2026-06-18

### Fixed

- **In-session search accepts `j` and `k` while typing** — fixed a TUI regression where search queries silently dropped those letters because they were intercepted as viewport scroll keys before reaching the search input. Arrow keys still scroll while search is active, and `j`/`k` still scroll after entering search navigation mode

## [1.3.0] - 2026-06-07

### Added

- **Per-provider resume flags: `codex_flags` and `copilot_flags`** — `config.toml` can now bake extra flags into Codex and Copilot resume commands the same way `claude_flags` already does for Claude (e.g. `codex_flags = ["--dangerously-bypass-approvals-and-sandbox"]`). Each provider's flags go where its CLI expects global options: `codex <flags> resume <id>` (before the subcommand — codex rejects them after), `copilot <flags> --resume=<id>`. Applies everywhere commands are built: TUI launch/copy/write/new-terminal/fallback, `debug-prompt`, and the MCP `resume_command` field

### Fixed

- **TUI resume launch for Codex/Copilot ignored configured flags** — the exec path passed no flags for non-Claude providers
- **`docs/CONFIGURATION.md` documented a `dangerously_skip_permissions` boolean that was never implemented** — replaced with the real `claude_flags` / `codex_flags` / `copilot_flags` keys

## [1.2.2] - 2026-06-07

### Added

- **`resume_command` in MCP session payloads** — `search_sessions`, `list_recent_sessions`, and `get_session_messages` now return a ready-to-run command (`cd '<project>' && claude --resume '<id>'`) so consuming agents never stitch a bare resume command from `project` + `session_id`, which fails outside the project directory. Codex commands use the bare rollout UUID, Copilot the `--resume=` form

### Fixed

- **Every emitted resume command includes the `cd <project>` prefix** — copy (`c`), write-to-file (`w`), and the terminal-fallback view could show commands that fail with "No conversation found" when run from another directory; the fallback view also incorrectly cd'd to the last working directory instead of the project path
- **Configured `claude_flags` apply to every emitted command** — TUI copy/write/fallback commands and MCP `resume_command` now include flags from `config.toml`, matching what resume actually runs; previously only the exec path applied them
- **`debug-prompt` shows the real resume prompt** — it rendered the template without the `same_directory`/`different_directory` variables, so conditional sections were silently dropped

### Changed

- **Resume command and prompt assembly consolidated in core** (`internal/core/session`) — command building, working-dir resolution, claude-flag application, and resume-prompt rendering now have a single implementation shared by CLI, TUI, and MCP. A session with no stored project path yields the bare command with a `# project path missing in DB` comment instead of erroring

## [1.2.1] - 2026-06-06

### Fixed

- **Resume command shell quoting**: session IDs and shell `cd` working directories are now quoted across resume and terminal-spawn paths
- **Copilot parser resilience**: malformed lines in `events.jsonl` are skipped instead of dropping the rest of the session transcript
- **Enumerated sync change detection**: skip hashes now use message UUIDs and text content instead of file mtime, avoiding mtime churn while still detecting same-length text edits

## [1.2.0] - 2026-06-06

### Added

- **GitHub Copilot CLI session support** — indexes Copilot CLI sessions from `~/.copilot/session-state/` (full `events.jsonl` transcripts) alongside Claude Code and Codex CLI in the same searchable database (thanks @dmd)
- **Provider-aware resume** — resuming now builds the right command per provider (`claude --resume`, `codex resume`, `copilot --resume=`); previously resuming a Codex session incorrectly launched `claude`
- **Copilot session parser** (`pkg/copilotsessions/`) — parses the Copilot CLI event log, using event IDs as stable message UUIDs
- **`[copilot]` tags** in TUI and CLI output, `--provider copilot` filtering on CLI and MCP tools

### Fixed

- **Stale terms in search index** — the FTS sync triggers used plain UPDATE/DELETE on external-content FTS5 tables, which leaves old terms behind; fixed to the documented delete+insert form, with a one-time migration that rebuilds both indexes
- **Resume prompt quoting** — a resume prompt containing a single quote no longer breaks the spawned terminal command
- **Re-imported message edits** — re-syncing a session whose message text changed in place now refreshes the stored text and search index instead of keeping the old version
- **TUI sync progress totals** — no longer counts subagent and edit-conflict files that the importer skips

## [1.1.9] - 2026-04-22

### Added
- **`view` command** — `ccrider view <session-id>` prints a session as markdown to stdout, using the same output as `export` without needing flags

## [1.1.8] - 2026-04-07

### Fixed
- **Skip corrupted JSONL lines** — the session importer now silently skips corrupted lines (e.g., null bytes from incomplete writes) instead of aborting the entire parsing process.
- **CI configuration and linting** — updated `.golangci.yml` for compatibility with newer linter versions and resolved several pre-existing staticcheck warnings.

## [1.1.7] - 2026-04-06

### Fixed
- **Goreleaser CI failure** — fixed `.golangci.yml` schema error (added `version: "2"` for newer golangci-lint compatibility).

## [1.1.6] - 2026-04-06

### Fixed
- **Goreleaser CI failure** — fixed "exit code 1" by tidying Go modules, removing accidentally tracked binary, and updating GitHub Actions to use official linter action.

## [1.1.5] - 2026-03-26

### Changed

- **Repo-aware session export** — pressing `e` in the TUI now opens an export dialog with a prefilled path instead of immediately writing to cwd. Default destination for repo-backed sessions is `<repo>/.ccrider/exports/session-<id>.md`. Nothing is written until Enter confirms. Closes #10
- **CLI export writes to stdout by default** — `ccrider export <id>` is now pipeable. Use `--repo` for repo-local export, `--output` for explicit path, `--force` to overwrite
- **Export markdown generation moved to core** — shared `internal/core/export/` package replaces duplicated raw SQL in interface layers

### Fixed

- **"Error: success: exported to..." display bug** — export success messages no longer route through `fmt.Errorf`; uses a dedicated status message field

### Added

- **Light theme support** — TUI colors adapt to terminal background using `lipgloss.AdaptiveColor`, fixing unreadable yellow-on-white in light terminals (thanks @seguri)
- **Export directory memory** — remembers last export directory per-repo and globally in config

## [1.1.4] - 2026-03-16

### Fixed

- **Session timestamps off by timezone offset** — "Updated: 7 hours ago" on sessions active minutes ago due to timezone being stripped during time formatting roundtrip (Format without timezone → Parse assumes UTC)
- **Importer uses max message timestamp** — use maximum timestamp across all messages instead of last array element, reducing unnecessary file_mtime fallback
- **"No sessions found" flash on startup** — briefly showed alarming empty-state message before async session load completed; now shows "Loading sessions..." during initial load

## [1.1.3] - 2026-03-09

### Fixed

- **Codex response_item parsing** — parse `response_item` events in addition to `event_msg`, fixing ~16% message loss and eliminating zero-message sessions where Codex CLI used `response_item` exclusively (thanks @APE-147)
- **Filter system boilerplate** — skip AGENTS.md instructions, environment_context, and system-reminder messages that Codex CLI emits as `role=user` response_items
- **Migration for existing users** — one-time migration wipes and re-imports Codex sessions automatically on upgrade, including derived data (summaries, issues, files)

## [1.1.2] - 2026-03-04

### Fixed

- **MCP tool annotations** — all tools now declare `readOnlyHint`, `destructiveHint: false`, `openWorldHint: false`, and `idempotentHint` so clients no longer label them as "destructive, open-world"

## [1.1.0] - 2026-02-28

### Added

- **Codex CLI session support** — indexes OpenAI Codex CLI sessions from `~/.codex/sessions/` alongside Claude Code sessions into a single searchable database
- **Provider filtering** — `--provider codex` or `--provider claude` on CLI list/search, plus `provider` parameter on MCP tools (`search_sessions`, `list_recent_sessions`)
- **Codex session parser** (`pkg/codexsessions/`) — parses Codex rollout JSONL format, maps `event_msg` payloads to the same schema used by Claude sessions, generates deterministic UUIDs via BLAKE3
- **`[codex]` tags** in TUI and CLI list output for non-Claude sessions

### Fixed

- Panic on sessions with IDs shorter than 12 characters
- UTF-8 corruption when truncating multi-byte summaries (bytes → runes)
- Silent zero timestamps from unparseable Codex timestamp fields

## [1.0.0] - 2026-02-28

### Changed

- **BLAKE3 hashing** replaces SHA256 for file change detection — faster and better sync convergence on cloud drives (inspired by @rcny's PR #5)
- **Filename-based session keying** — sessions are now keyed by JSONL filename instead of the parsed `sessionId` field. Fixes hash thrashing where resumed sessions (which reference their parent's UUID) caused multiple files to fight over one DB row
- **MCP response trimming** — deterministic token-based limits on all MCP handlers (was a guessed byte limit on one handler). Measures against actual serialized JSON, respects Claude Code's 25k token hard limit

### Fixed

- **Message relinking** — messages that were stuck under orphan session rows (from the old keying scheme) are now correctly reassigned during sync via `ON CONFLICT(uuid) DO UPDATE SET session_id`
- **MCP protocol corruption** — importer warnings were written to stdout, which IS the MCP JSON-RPC transport. Moved to stderr
- **Crash-safe migrations** — each column addition now checks for its own existence individually, so a crash between two ALTER TABLEs won't leave the schema half-migrated
- **Recovery prompt param name** — recovery mode told Claude to use `session_id` for search, but the actual MCP tool parameter is `current_session_id`
- **Negative limit panic** — MCP handler crashed on negative limit values
- **Trim edge cases** — trim loops could stop with items still over budget; `last_n` mode now correctly trims from front only

## [0.10.0] - 2026-02-01

### Performance

- **3.9x faster imports** - 19s → 2s for ~3000 sessions (74% improvement)
  - Multi-level change detection: mtime+size → hash → parse
  - Pre-load all session metadata in single query (eliminates N queries)
  - Skip 90%+ of files instantly via mtime+size check
  - Only hash/parse files that actually changed
- **Handle arbitrarily large JSONL lines** - tested with 105MB lines
  - Switched from bufio.Scanner (64MB limit) to bufio.Reader
  - No more "token too long" errors on sessions with huge base64 images or tool outputs

### Added

- **File-based change detection** - new DB columns: file_mtime, file_size, file_inode, file_device, file_hash
  - Content-based deduplication via SHA256 hashing
  - Handles filesystem quirks (clock skew, inode reuse)
  - Automatic one-time migration populates tracking data

### Fixed

- Support both RFC3339 and Go time formats when reading DB timestamps
- Silently skip files deleted between directory walk and import (race condition)
- Always skip subagent sessions (they use parent session IDs and would conflict)

### Changed

- Show failure count summary instead of verbose statistics
- Log warnings only for actual errors (parse failures, hash failures)
- Clean startup with no error spam

## [0.9.9] - 2026-01-12

### Fixed

- **Spinner panic on resume** - fixed "send on closed channel" crash when resuming sessions. The spinner's Stop() method is now idempotent, safe to call multiple times (fixes #2).

## [0.9.8] - 2026-01-12

### Added

- **Session recovery mode** - when a session file has been deleted by Claude Code but CCRider still has the conversation indexed, CCRider can start a new session with context from the old one. Prompts user to confirm, checks for CCRider MCP server, and falls back to asking for directory if original paths don't exist.

## [0.9.7] - 2025-01-11

### Fixed

- **project_path not updating on re-import** - sessions imported with wrong project_path (e.g., worktree path instead of main repo) were stuck forever because `ON CONFLICT` didn't update project_path. Now always updates from first message CWD.
- **Date filter comparison** - fixed timestamp format mismatch that caused date filters to fail

### Added

- **`sync --force` flag** - re-imports all sessions regardless of mtime, fixing any stale project_path values
- **CLI date filters** - CLI search now supports same filters as TUI: `after:`, `before:`, `date:`, `project:`

### Changed

- **Centralized filter parsing** - date/project filters moved to core, shared by TUI and CLI
- Supported date formats:
  - Go duration: `after:3h`, `after:24h`, `after:168h` (hours ago)
  - Natural language: `after:yesterday`, `after:tomorrow`, `before:today`
  - Relative: `after:3-days-ago`, `before:last-week`
  - ISO 8601: `after:2024-01-15`, `before:2024-01-15T10:30:00`

## [0.9.6] - 2025-01-08

### Added

- Anchor phrase retry increased for better reliability

## [0.9.5] - 2025-01-04

### Added

- **MCP: generate_session_anchor tool** - generates a unique diceware phrase Claude says aloud to "tag" its session, then searches with anchor_phrase to find earlier context that disappeared due to context compaction
- Anchor phrase search now retries up to 3 times with 500ms delays to handle Claude Code write buffering

### Removed

- **MCP: get_session_detail tool** - redundant with search_sessions + get_session_messages

## [0.9.4] - 2025-01-04

### Added

- **MCP: anchor_phrase for finding current session** - Claude can now search earlier in its own conversation by providing a unique phrase it just said/saw as an anchor. Defaults to last hour for best accuracy.
- **MCP: exact_match parameter** - auto-quotes the query for exact phrase matching (no more Claude failing to quote)

## [0.9.3] - 2025-01-04

### Fixed

- **Search race condition** - fast typing no longer shows stale results
  - Previously: typing "hello" quickly could show results for "hell" if that search completed last
  - Now: sequence numbers ensure only the most recent search results are displayed

## [0.9.2] - 2025-01-01

### Fixed

- **FTS5 search now handles all special characters** - queries with commas, hyphens, @, #, and other punctuation no longer cause syntax errors
  - Previously: `"4 tests, 0 failures"` → FTS5 syntax error
  - Now: properly escaped and searched
- Implemented proper FTS5 query escaping (same approach as sqlite-utils/datasette)
- Removed LIKE fallback for special characters - all searches now use FTS5 with proper escaping
- Preserved wildcard search functionality (`handle*` still works)

## [0.9.1] - 2024-12-30

### Fixed

- Phrase search now works in TUI (quotes were being stripped)
- Auto-balance quotes during live typing to prevent FTS5 errors
- Restored Elvis video to README hero position

## [0.9.0] - 2024-12-30

### Added

- **Anthropic API support** for session summarization as alternative to AWS Bedrock
  - Set `ANTHROPIC_API_KEY` to use direct API instead of Bedrock
- **Filter-only search** - search by date/project without requiring text query
  - `after:yesterday`, `before:2024-11-01`, `project:myapp` work standalone
- **SQLite busy timeout** - 5 second retry on database locks during concurrent access
- Message count and last working directory in CLI search output

### Fixed

- Date filter parsing: `2024-11-01` no longer misinterpreted as time "11:01"
- Hyphenated date filters now work: `3-days-ago`, `last-week`
- TUI search view overflow - header no longer disappears with many results
- Summary and project path truncation in TUI to prevent text spilling off screen
- Skip sessions with fewer than 5 messages during summarization

### Changed

- Improved summarization prompts for better problem-solution focus
- CLI search output format now matches TUI (relative time, message count, shorter paths)

## [0.2.6] - Previous release

See git history for earlier changes.
