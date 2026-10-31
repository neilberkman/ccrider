# ccrider

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Release](https://img.shields.io/github/v/release/neilberkman/ccrider)](https://github.com/neilberkman/ccrider/releases)
[![Homebrew](https://img.shields.io/badge/homebrew-neilberkman%2Ftap-orange)](https://github.com/neilberkman/homebrew-tap)
[![Show HN](https://img.shields.io/badge/Show%20HN-black?logo=ycombinator)](https://news.ycombinator.com/item?id=46512501)

Search, browse, and resume your Claude Code, Codex CLI, GitHub Copilot CLI, OpenCode, Pi, Antigravity, and Amp sessions, plus MCP server to remember past context.

When your coding agent forgets, tell it: _[see what you have done](#the-king)_.

## Why ccrider?

You've got months of coding agent sessions sitting in `~/.claude/projects/`, `~/.codex/sessions/`, `~/.copilot/`, `~/.local/share/opencode/`, `~/.pi/agent/sessions/`, `~/.gemini/antigravity*/brain/`, and your Amp account. Finding that conversation where you fixed the authentication bug? Good luck grepping through nested JSON files and separate tools.

ccrider indexes Claude Code, Codex CLI, GitHub Copilot CLI, OpenCode, Pi, Antigravity (CLI, IDE, and desktop app), and Amp sessions into a single searchable database, with a TUI browser, CLI search, and an MCP server so your agent can search past sessions too.

```bash
# Import sessions from every detected local provider (and Amp when explicitly enabled)
ccrider sync

# Launch the TUI - browse, search, resume
ccrider tui

# Or search from command line
ccrider search "authentication bug"
```

Stay in your terminal. Find any conversation. Resume where you left off. Sessions are tagged with their provider in the TUI for easy identification.

**Installation:**

```bash
# Homebrew (recommended)
brew install neilberkman/tap/ccrider

# Installed 1.11.1 or earlier with Homebrew? ccrider is now a cask, not a formula:
brew uninstall --formula ccrider
brew trust --cask neilberkman/tap/ccrider
brew install --cask neilberkman/tap/ccrider

# Or from source
git clone https://github.com/neilberkman/ccrider.git
cd ccrider
go build -o ccrider cmd/ccrider/main.go
sudo mv ccrider /usr/local/bin/

# Optional: install the MCP server in Claude Code
claude mcp add --scope user ccrider $(which ccrider) serve-mcp
```

<a id="the-king"></a>
_"Vibe code like ~a king~ The King!"_

https://github.com/user-attachments/assets/5b008290-076e-4323-a775-f27f704b1ff2

## Core Features

### 1. Interactive TUI Browser

```bash
ccrider tui
```

Browse your sessions with a polished terminal UI:

- **Arrow keys** to navigate
- **Enter** to view full conversation
- **o** to open session in new terminal tab (auto-detects Ghostty, iTerm, Terminal.app)
- **/** to search across all messages
- **p** to toggle project filter (show only current directory)
- **?** for help

Sessions matching your current directory are highlighted in light green - instantly see which sessions are relevant to your current work.

### 2. Full-Text Search

```bash
ccrider search "postgres migration"
ccrider search "error handling" --project ~/code/myapp
ccrider search "authentication" --after 2024-01-01
```

Powered by SQLite FTS5 - search message content, filter by project or date, get results instantly.

### 3. Resume Sessions

Press **r** in the TUI to launch the provider's resume command in the right directory with the right session. Use **c** to copy that command or **o** to open it in a new terminal. Pi resumes with `pi --session <id>` and forks with `pi --fork <id>`; Antigravity CLI sessions resume with `agy --conversation <id>` and fork interactively with `/fork`; Antigravity IDE and desktop app conversations are searchable and browsable but have no terminal resume command, so reopen them in the app; Amp resumes with `amp threads continue <id>`.

### 4. Incremental Sync

```bash
ccrider sync         # Import new sessions from all providers
ccrider sync --force # Re-import everything
```

Automatically discovers Claude Code (`~/.claude/projects/`), Codex CLI (`~/.codex/sessions/`), GitHub Copilot CLI (`~/.copilot/session-state/`), OpenCode (`~/.local/share/opencode/opencode*.db`), Pi (`~/.pi/agent/sessions/`), Antigravity CLI (`~/.gemini/antigravity-cli/brain/`), Antigravity IDE (`~/.gemini/antigravity-ide/brain/`), and the Antigravity desktop app (`~/.gemini/antigravity/brain/`) sessions. Antigravity imports each canonical `transcript.jsonl`, excluding its diagnostic transcript. To import Amp, set `amp_enabled = true` in `~/.config/ccrider/config.toml` and install/authenticate the `amp` CLI. ccrider then lists archived and active threads and exports only new or changed threads.

Amp threads are cloud-backed. The integration is disabled by default because enabling it downloads searchable text into ccrider's local SQLite database. MCP requests never contact Amp; they search cached Amp data from an earlier CLI/TUI sync.

---

## MCP Server

ccrider includes a built-in MCP (Model Context Protocol) server that gives your coding agent access to your session history.

Ask your agent to search past conversations while working on new problems:

- "Find sessions where I worked on authentication"
- "Show me my most recent Elixir sessions"
- "What was I working on last week in the billing project?"
- "Search my Codex sessions for database migrations"

### Setup

Configure your MCP-capable client to run `ccrider serve-mcp`. Examples:

**Claude Code:**

```bash
# Install for all your projects (recommended)
claude mcp add --scope user ccrider $(which ccrider) serve-mcp

# Or for current project only
claude mcp add ccrider $(which ccrider) serve-mcp
```

**Claude Desktop:**

Add to your config (`~/Library/Application Support/Claude/claude_desktop_config.json`):

```json
{
  "mcpServers": {
    "ccrider": {
      "command": "ccrider",
      "args": ["serve-mcp"]
    }
  }
}
```

**Other MCP clients:**

Use command `ccrider` with args `["serve-mcp"]`.

### Available Tools

- **search_sessions** - Full-text search across all session content with date/project/provider filters
- **list_recent_sessions** - Get recent sessions, optionally filtered by project or provider
- **get_session_messages** - Get messages from a session (supports tail mode, context around search matches)
- **generate_session_anchor** - Save a unique session anchor, optionally with a descriptive label and a known session ID
- **list_session_anchors** - List saved anchors and their matching sessions, with project/provider/session filters and pagination
- **wait_for_session** - Wait for another session to finish a turn, write a message or phrase, or send a signal
- **send_session_signal** - Post a signal to a channel, waking sessions that wait on it

`search_sessions`, `list_recent_sessions`, and `list_session_anchors` support a `provider` parameter to filter by `claude`, `codex`, `copilot`, `opencode`, `pi`, `antigravity`, `antigravity-ide`, `antigravity-desktop`, or `amp`. Search and retrieval tools read your local session database; anchor generation saves a bookmark there. Local providers are read from disk; Amp threads are fetched from your authenticated Amp account during sync and then stored locally.

### Saving and finding session anchors

Ask your agent to bookmark a conversation with a descriptive label. For example, `generate_session_anchor` with `{"label": "Add REST endpoint"}` saves a tag such as `add-rest-endpoint-a12bc34de56f`. The agent supplies the label from the conversation; no separate LLM service is required. Omitting `label` keeps the original four-random-word format.

If the agent knows the session ID, it can also supply `session_id` to attach the bookmark immediately. Otherwise, it includes the returned phrase in its response and waits for a user reply so the phrase can be written to the transcript. `list_session_anchors` syncs local transcripts and finds conversations containing the phrase, without a date cutoff.

Call `list_session_anchors` to retrieve saved labels, phrases, timestamps, and matching sessions with resume commands. Use a returned `session_id` with `get_session_messages` to read the conversation. The list defaults to 20 anchors per page (maximum 100); pass its `next_offset` as `offset` to continue. Optional `project`, `provider`, and `session_id` filters select associated sessions.

An anchor appears in the unfiltered list as soon as it is generated, even if its `sessions` list is still empty. Session filters omit unmatched anchors. A phrase copied into multiple conversations can match more than one session; an explicit `session_id` binding takes precedence. Large session lists can be shortened with `sessions_truncated: true` and the full `session_count` retained.

The registry includes anchors generated after this feature was added. Previously generated phrases and manually written tags remain searchable as ordinary conversation text.

### Waiting on another session

One session can wait for another, even when they run in different harnesses. There are two kinds of watch.

**Transcript watches** follow a Claude Code or Codex session's transcript, with no cooperation needed from it. Call `wait_for_session` with a `session_id` and `until` set to any of:

- `turn_end`: the session finishes a turn. The event's `outcome` is `completed`, `failed`, `refused` or `interrupted`. Codex events include the turn's final message as `excerpt`.
- `message`: the session writes any new assistant message.
- `phrase`: the session writes a message containing a phrase. Pass `mint_phrase: true` to get a unique phrase to hand to the other session.

`turn_end` needs an interactive Claude Code session; headless `claude -p` sessions don't record turn ends. A turn already running when the watch is armed reports its end. A Codex thread is followed when it moves to another rollout file.

**Signal watches** work with any harness. Arm with `{"until": ["signal"], "mint_channel": true}`, give the channel to the other session, and have it call `send_session_signal` with a short result when it's done. Signals are kept for 7 days. Resending with the same `idempotency_key` and body doesn't post twice.

Arming returns immediately with a cursor. Each wait returns as soon as something matches, or after `max_wait_seconds` (default 20) with status `waiting` and a cursor to continue from. Nothing is lost between calls, and every event has an `event_id` for deduplication. If a transcript is rewritten, truncated or replaced, the wait returns status `gap` with a fresh cursor, since events in between may be missing.

The same works from a shell, which suits agents that can run a background command and resume when it exits:

```bash
ccrider wait arm <session> --until turn_end   # prints a cursor
ccrider wait <cursor> --timeout 2h            # exits 0 on an event, 2 on timeout, 3 on a gap
ccrider wait arm --signal --mint-channel      # signal watch: prints a channel and cursor
ccrider signal <channel> "tests pass"         # from the other session
```

The design and its limits are in `docs/plans/2026-10-30-session-watch-spec.md`.

### Recovering earlier context

`generate_session_anchor` helps an agent identify its current conversation after context compaction. The agent calls the tool, includes the returned phrase in its response, and asks you to reply so that response can be written to the transcript. It then calls `search_sessions` with the phrase as `anchor_phrase` and the topic it needs as `query`. You do not need to keep a separate record of the phrase for this workflow.

A unique phrase already visible in the conversation can also be used as `anchor_phrase` without calling the generator. The `anchor_phrase` search filter matches transcript text and, by default, only considers matches from the last hour; explicit `after_date` or `before_date` filters override that window. For a saved bookmark, use `list_session_anchors` to obtain the session ID directly, including when the bookmark was attached without writing its phrase into the transcript.

To find an older conversation using a tag you know, including a descriptive tag you wrote yourself, search for it directly:

```json
{
  "query": "how-to-add-a-rest-endpoint-in-this-repo-abc123",
  "exact_match": true
}
```

This ordinary `search_sessions` query has no default date restriction. Use a returned `session_id` with `get_session_messages` to read the conversation.

---

## Configuration

> **Note:** Claude Code auto-deletes session JSON files after 30 days by default. ccrider preserves all session content in its own database, but if you want the original files kept (for resume, etc.), add `"cleanupPeriodDays": 99999` to your `~/.claude/settings.json`.

ccrider looks for config in `~/.config/ccrider/`:

```toml
# config.toml - pass additional flags to each provider's resume command
claude_flags = ["--dangerously-skip-permissions"]
# codex_flags = ["--dangerously-bypass-approvals-and-sandbox"]
# copilot_flags = []
# opencode_flags = []
# pi_flags = []
```

```txt
# terminal_command.txt - custom command for 'o' key
# Available placeholders: {cwd}, {command}
wezterm cli spawn --cwd {cwd} -- {command}
```

```txt
# resume_prompt.txt - customize the prompt sent when resuming sessions
```

See [CONFIGURATION.md](docs/CONFIGURATION.md) for full details.

---

## Architecture

Built with strict core/interface separation following [Saša Jurić's principles](https://www.theerlangelist.com/article/phoenix_is_modular):

- **Core** (`pkg/`, `internal/core/`): Pure business logic - parsing, database, search, multi-provider import
- **Interface** (`internal/interface/`, `cmd/`): Thin wrappers - CLI, TUI, MCP server

Uses proven technologies:

- **Go** for performance and single-binary distribution
- **SQLite with FTS5** for fast full-text search
- **Bubbletea** for polished terminal UI
- **MCP** for coding agent integration

### Why This Matters

Other coding agent session tools are broken:

- Incomplete schema support (can't parse all message types)
- Broken builds and abandoned dependencies
- No real search (just grep)
- Can't actually resume sessions
- Single-provider only

ccrider fixes this with:

- 100% schema coverage - parses all message types correctly
- Multi-provider - Claude Code, Codex CLI, GitHub Copilot CLI, OpenCode, Pi, Antigravity, and Amp in one database
- SQLite FTS5 search - fast, powerful full-text search
- Single binary - no npm, no pip, no dependencies
- Native resume - one keystroke to resume sessions
- Incremental sync - detects new messages in ongoing sessions

---

## Development

See [CONTRIBUTING.md](CONTRIBUTING.md) for development setup and guidelines.

### Project Structure

```
cmd/ccrider/          # CLI entry point + MCP server
internal/
  core/               # Business logic (no UI concerns)
    db/               # Database operations
    importer/         # Session import/sync and Amp CLI client
    search/           # Full-text search
    session/          # Session launch logic
  interface/          # Thin UI wrappers
    cli/              # Command handlers
    tui/              # Terminal UI (bubbletea)
pkg/
  ccsessions/         # Claude Code session parser (public API)
  codexsessions/      # Codex CLI session parser (public API)
  copilotsessions/    # GitHub Copilot CLI session parser (public API)
  opencodesessions/   # OpenCode session parser (public API)
  pisessions/         # Pi session parser (public API)
  antigravitysessions/ # Antigravity session parser (public API)
  ampsessions/        # Pure Amp export parser (public API)
```

### Quick Build

```bash
go build -o ccrider cmd/ccrider/main.go
./ccrider sync
./ccrider tui
```

---

## Documentation

- [Configuration Guide](docs/CONFIGURATION.md)
- [Resume Prompts](docs/RESUME_PROMPT.md)
- [Design Document](docs/plans/2025-11-08-ccrider-design.md)
- [Schema Documentation](research/schema.md)
- [Requirements](research/requirements.md)

## License

MIT
