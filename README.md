# ccrider

[![Go Report Card](https://goreportcard.com/badge/github.com/neilberkman/ccrider)](https://goreportcard.com/report/github.com/neilberkman/ccrider)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Release](https://img.shields.io/github/v/release/neilberkman/ccrider)](https://github.com/neilberkman/ccrider/releases)
[![Homebrew](https://img.shields.io/badge/homebrew-neilberkman%2Ftap-orange)](https://github.com/neilberkman/homebrew-tap)
[![Show HN](https://img.shields.io/badge/Show%20HN-black?logo=ycombinator)](https://news.ycombinator.com/item?id=46512501)

Search, browse, and resume your Claude Code and Codex CLI sessions, plus MCP server to remember past context.

When your coding agent forgets, tell it: _[see what you have done](#the-king)_.

## Why ccrider?

You've got months of coding agent sessions sitting in `~/.claude/projects/` and `~/.codex/sessions/`. Finding that conversation where you fixed the authentication bug? Good luck grepping through nested JSON files.

ccrider indexes Claude Code and Codex CLI sessions into a single searchable database, with a TUI browser, CLI search, and an MCP server so your agent can search past sessions too.

```bash
# Import sessions from Claude Code and Codex CLI
ccrider sync

# Launch the TUI - browse, search, resume
ccrider tui

# Or search from command line
ccrider search "authentication bug"
```

Stay in your terminal. Find any conversation. Resume where you left off. Codex sessions are tagged with `[codex]` in the TUI for easy identification.

**Installation:**

```bash
# Homebrew (recommended)
brew install neilberkman/tap/ccrider

# Or from source
git clone https://github.com/neilberkman/ccrider.git
cd ccrider
go build -o ccrider cmd/ccrider/main.go
sudo mv ccrider /usr/local/bin/

# Install MCP server for all your projects (optional)
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

Press **r** in the TUI or use the CLI:

```bash
ccrider resume <session-id>
```

Launches `claude --resume` in the right directory with the right session. Just works.

### 4. Incremental Sync

```bash
ccrider sync         # Import new sessions from all providers
ccrider sync --full  # Re-import everything
```

Automatically discovers Claude Code (`~/.claude/projects/`) and Codex CLI (`~/.codex/sessions/`) sessions. Detects ongoing sessions and imports new messages without re-processing everything.

---

## MCP Server

ccrider includes a built-in MCP (Model Context Protocol) server that gives your coding agent access to your session history.

Ask your agent to search past conversations while working on new problems:

- "Find sessions where I worked on authentication"
- "Show me my most recent Elixir sessions"
- "What was I working on last week in the billing project?"
- "Search my Codex sessions for database migrations"

### Setup

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

### Available Tools

- **search_sessions** - Full-text search across all session content with date/project/provider filters
- **list_recent_sessions** - Get recent sessions, optionally filtered by project or provider
- **get_session_messages** - Get messages from a session (supports tail mode, context around search matches)
- **generate_session_anchor** - Generate a unique phrase to tag your session for later retrieval

All tools support a `provider` parameter to filter by `claude` or `codex`. The MCP server provides read-only access to your session database. Your conversations stay local.

---

## Configuration

> **Note:** Claude Code auto-deletes session JSON files after 30 days by default. ccrider preserves all session content in its own database, but if you want the original files kept (for resume, etc.), add `"cleanupPeriodDays": 99999` to your `~/.claude/settings.json`.

ccrider looks for config in `~/.config/ccrider/`:

```toml
# config.toml - pass additional flags to claude --resume
claude_flags = ["--dangerously-skip-permissions"]
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
- **MCP** for Claude integration

### Why This Matters

Other coding agent session tools are broken:

- Incomplete schema support (can't parse all message types)
- Broken builds and abandoned dependencies
- No real search (just grep)
- Can't actually resume sessions
- Single-provider only

ccrider fixes this with:

- 100% schema coverage - parses all message types correctly
- Multi-provider - Claude Code and Codex CLI in one database
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
    importer/         # Session import/sync
    search/           # Full-text search
    session/          # Session launch logic
  interface/          # Thin UI wrappers
    cli/              # Command handlers
    tui/              # Terminal UI (bubbletea)
pkg/
  ccsessions/         # Claude Code session parser (public API)
  codexsessions/      # Codex CLI session parser (public API)
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
