# Changelog

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
