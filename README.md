# ccrider

The companion Claude Code needs. Search, browse, and resume your Claude Code sessions.

You've got months of Claude Code sessions sitting in `~/.claude/projects/`. Finding that conversation where you fixed the authentication bug? Good luck grepping through nested JSON files.

ccrider indexes everything into SQLite with full-text search. Browse sessions in a TUI, search across all messages instantly, and resume any session with one keystroke.

```bash
# Launch the TUI - browse, search, resume
# Syncs on startup.
ccrider

# Or sync & search in one command
ccrider search --sync "authentication bug"
```

**Installation:**

```bash
# macOS (Homebrew - recommended)
brew install neilberkman/ccrider/ccrider

# Linux/Windows (build from source)
git clone https://github.com/neilberkman/ccrider.git
cd ccrider
go build ./cmd/ccrider
```

_“Vibe code like ~a king~ The King!”_

https://github.com/user-attachments/assets/5b008290-076e-4323-a775-f27f704b1ff2

## What Makes It Good

### Smart Resume with Context

Press **r** in the TUI to resume any session. ccrider sends Claude an intelligent prompt that includes:

- How long the session has been inactive ("3 days ago" not timestamps)
- Where you were actually working (handles git worktrees)
- Reminder to check git status if stale

You were working in `/project/.worktrees/feature-branch`? ccrider remembers by parsing the session's messages for the last working directory, then tells Claude where to pick up.

Customize the resume prompt with your own template at `~/.config/ccrider/resume_prompt.txt`. See [RESUME_PROMPT.md](docs/RESUME_PROMPT.md).

### Terminal Auto-Detection

Hit **o** to open a session in a new terminal window. ccrider automatically detects and uses the right method for:

- Ghostty (native IPC)
- iTerm2 (AppleScript)
- Terminal.app (AppleScript)
- WezTerm (CLI spawn)
- Kitty (socket control)

Don't use these? Drop a custom command in `~/.config/ccrider/terminal_command.txt`.

### Project-Aware Browsing

Sessions matching your current directory are highlighted in the TUI. Working in `/projects/myapp`? See all your myapp sessions at the top. Press **p** to filter and show only those.

### Auto Sync with Progress

Launch the TUI - it syncs automatically on startup with a real-time progress bar: `Syncing: [████░░░] 67% (1234/1845)`. Press **s** anytime to sync again. Incremental sync detects ongoing sessions and imports new messages without re-processing everything.

### Search That Works

```bash
ccrider search "postgres migration"
ccrider search "ena-6530"  # Works with issue IDs
ccrider search "user@email.com"  # And email addresses
```

SQLite FTS5 with intelligent fallback - queries with special characters use exact matching, everything else uses full-text search with ranking. Results show snippets of the actual matching text, not random message chunks.

Filter by project or date:

```bash
ccrider search "authentication" --project ~/code/myapp --after 2024-01-01
```

---

## MCP Server

Built-in MCP server gives Claude access to your session history. Ask Claude to find that authentication fix from last month or pull up all your postgres migration sessions while working on a new problem.

```bash
# Install for all your projects
claude mcp add --scope user ccrider $(which ccrider) serve-mcp
```

See [CONFIGURATION.md](docs/CONFIGURATION.md) for Claude Desktop setup.

---

## Configuration

Optional config at `~/.config/ccrider/config.toml`:

```toml
# Pass flags to claude on resume (useful for --dangerously-skip-permissions)
claude_flags = ["--dangerously-skip-permissions"]

# Custom terminal command for 'o' key (auto-detects by default)
terminal_command = "wezterm cli spawn --cwd {cwd} -- {command}"
```

See [CONFIGURATION.md](docs/CONFIGURATION.md) and [RESUME_PROMPT.md](docs/RESUME_PROMPT.md) for details.

---

## How It Works

Single Go binary. SQLite database with FTS5 for search. Parses all Claude Code message types from `~/.claude/projects/`. Incremental sync detects ongoing sessions and imports only new messages.

Built with strict core/interface separation - pure business logic (parsing, database, search) in the core, thin wrappers (CLI, TUI, MCP server) in the interface layer.

---

## Development

```bash
go build -o ccrider cmd/ccrider/main.go
./ccrider sync
./ccrider tui
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

## License

MIT
