# ccrider

Search, browse, and resume your Claude Code sessions. Fast.

**macOS only** - built specifically for Claude Code's session format and macOS clipboard system.

## Why ccrider?

You've got months of Claude Code sessions sitting in `~/.claude/projects/`. Finding that conversation where you fixed the authentication bug? Good luck grepping through nested JSON files.

ccrider solves this:

```bash
# Import your sessions once
ccrider sync

# Launch the TUI - browse, search, resume
ccrider tui

# Or search from command line
ccrider search "authentication bug"
```

Stay in your terminal. Find any conversation. Resume where you left off.

**Installation:**

```bash
# From source (releases coming soon)
git clone https://github.com/you/ccrider.git
cd ccrider
go build -o ccrider cmd/ccrider/main.go
sudo mv ccrider /usr/local/bin/
```

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
ccrider sync       # Import all new sessions
ccrider sync --full  # Re-import everything
```

Detects ongoing sessions and imports new messages without re-processing everything.

[![](https://img.youtube.com/vi/6W-sNKa80QA/0.jpg)](https://youtu.be/6W-sNKa80QA?si=mz55F2_xipjZrFBq&t=22)

---

## MCP Server

ccrider includes a built-in MCP (Model Context Protocol) server that gives Claude access to your session history.

Ask Claude to search your past conversations while working on new problems:

- "Find sessions where I worked on authentication"
- "Show me my most recent Elixir sessions"
- "What was I working on last week in the billing project?"
- "Search my sessions for postgres migration issues"

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

- **get_session_detail** - Retrieve full conversation for a specific session
- **list_recent_sessions** - Get recent sessions, optionally filtered by project
- **search_sessions** - Full-text search across all session content with date/project filters

The MCP server provides read-only access to your session database. Your conversations stay local.

---

## Configuration

ccrider looks for config at `~/.config/ccrider/config.toml`:

```toml
# Skip permission prompts (use with caution)
dangerously_skip_permissions = true

# Custom terminal command for 'o' key
# Available placeholders: {cwd}, {command}
terminal_command = "wezterm cli spawn --cwd {cwd} -- {command}"
```

See [CONFIGURATION.md](docs/CONFIGURATION.md) for full details.

---

## Architecture

Built with strict core/interface separation following [Saša Jurić's principles](https://www.theerlangelist.com/article/phoenix_is_modular):

- **Core** (`pkg/`, `internal/core/`): Pure business logic - parsing, database, search
- **Interface** (`internal/interface/`, `cmd/`): Thin wrappers - CLI, TUI, MCP server

Uses proven technologies:

- **Go** for performance and single-binary distribution
- **SQLite with FTS5** for fast full-text search
- **Bubbletea** for polished terminal UI
- **MCP** for Claude integration

### Why This Matters

Other Claude Code session tools are broken:

- Incomplete schema support (can't parse all message types)
- Broken builds and abandoned dependencies
- No real search (just grep)
- Can't actually resume sessions

ccrider fixes this with:

- ✅ 100% schema coverage - parses all message types correctly
- ✅ SQLite FTS5 search - fast, powerful full-text search
- ✅ Single binary - no npm, no pip, no dependencies
- ✅ Native resume - one keystroke to resume sessions
- ✅ Incremental sync - detects new messages in ongoing sessions

---

## Development

See [CONTRIBUTING.md](CONTRIBUTING.md) for development setup and guidelines.

### Project Structure

```
cmd/ccrider/          # CLI entry point
internal/
  core/               # Business logic
    db/               # Database operations
    parser/           # JSON parsing
    sync/             # Session synchronization
  interface/          # UI/interface code
    cli/              # Command handlers
    tui/              # Terminal UI
    mcp/              # MCP server
pkg/                  # Public libraries (none yet)
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
