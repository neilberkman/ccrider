# ccrider

**Claude Code session manager - search, browse, and resume your coding sessions**

`ccrider` is a fast, reliable tool for managing Claude Code sessions. Unlike existing broken tools, it provides complete schema support, robust full-text search, and seamless integration with Claude Code's native resume functionality.

## Why ccrider?

Existing tools for Claude Code sessions are broken:

- Incomplete schema support (can't parse all message types)
- Broken builds and dependencies
- No real search (just grep)
- Can't actually resume sessions easily

`ccrider` fixes all of this:

- ✅ **100% schema coverage** - Parses all message types correctly
- ✅ **SQLite FTS5 search** - Fast, powerful full-text search
- ✅ **Single binary** - No npm install, no pip, no dependencies
- ✅ **Native resume** - One keystroke to resume sessions in Claude Code
- ✅ **Incremental sync** - Detects new messages in ongoing sessions

## Features

- 🔍 **Full-text search** across all sessions with filters
- 📝 **TUI browser** built with Bubbletea
- 🔄 **Resume sessions** with `r` key - launches `claude --resume`
- 📊 **Session analytics** - token usage, message counts, timelines
- 🚀 **Fast imports** - parallel processing with incremental updates
- 📦 **Reusable libraries** - Parser and DB sync available as standalone packages

## Installation

### Binary Release (recommended)

```bash
# macOS (ARM)
curl -L https://github.com/you/ccrider/releases/latest/ccrider-darwin-arm64 -o ccrider
chmod +x ccrider
sudo mv ccrider /usr/local/bin/

# macOS (Intel)
curl -L https://github.com/you/ccrider/releases/latest/ccrider-darwin-amd64 -o ccrider
chmod +x ccrider
sudo mv ccrider /usr/local/bin/

# Linux
curl -L https://github.com/you/ccrider/releases/latest/ccrider-linux-amd64 -o ccrider
chmod +x ccrider
sudo mv ccrider /usr/local/bin/
```

### From Source

```bash
git clone https://github.com/you/ccrider.git
cd ccrider
go build -o ccrider cmd/ccrider/main.go
```

## Quick Start

```bash
# Import your sessions
ccrider sync

# Search across all sessions
ccrider search "authentication bug"

# Launch interactive TUI
ccrider tui

# Resume a session (from TUI or CLI)
ccrider resume <session-id>
```

## Documentation

- [Design Document](docs/plans/2025-11-08-ccrider-design.md)
- [Schema Documentation](research/schema.md)
- [Requirements](research/requirements.md)

## Architecture

Built with strict core/interface separation:

- **Core**: Pure business logic (parsing, DB, search)
- **Interface**: Thin wrappers (CLI, TUI)

Uses proven technologies:

- Go for performance and single-binary distribution
- SQLite with FTS5 for fast full-text search
- Bubbletea for polished terminal UI

## Development

See [CONTRIBUTING.md](CONTRIBUTING.md) for development setup and guidelines.

## License

MIT
