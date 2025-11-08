# Claude Session Parser - Requirements & Design Spec

## Project Overview

Tool to parse, index, search, and interact with Claude Code session history stored in `~/.claude/`.

## Core Requirements

### 1. Data Import & Storage

**Source Data:**

- Location: `~/.claude/projects/[project-path]/[sessionId].jsonl`
- Format: JSONL with 5 entry types (summary, user, assistant, system, file-history-snapshot)
- Session structure: One session = one file, `sessionId` matches filename
- Agent sessions: `agent-[shortId].jsonl` (subagent transcripts)

**Database Requirements:**

- SQLite with full schema preservation
- Store ALL fields from JSONL (no data loss)
- Support incremental updates (detect new messages in existing sessions)
- Track: sessions, messages, tools used, file changes, agents
- Metadata: `cwd`, `gitBranch`, `timestamp`, `parentUuid`, `version`, etc.

**Import Strategy:**

- File hash tracking to detect already-imported sessions
- Incremental sync: append new messages to existing sessions
- Parallel import support (multiple sessions concurrently)
- WAL mode for concurrent reads during import
- Batch inserts for performance

### 2. Search & Filtering

**Full-Text Search:**

- SQLite FTS5 with dual tokenizers (like shannon):
  - Porter stemming for natural language
  - Unicode61 for code/symbols
- Search across: user messages, assistant messages, tool outputs
- Boolean operators: AND, OR, NOT
- Phrase search with quotes
- Wildcard support

**Filtering:**

- By date range (session created/updated, message timestamps)
- By directory/project path
- By git branch
- By model used
- By sender (user/assistant)
- By tool usage (sessions that used specific tools)
- By session duration, message count

**Query Performance:**

- Proper indexing on: sessionId, cwd, timestamp, gitBranch
- FTS5 for text search
- Conversation-level results (not individual messages)

### 3. Background Sync (Hybrid Approach)

**Manual Mode (Default):**

- `sync` command imports new/updated sessions
- Auto-check staleness before searches (check file mtime vs DB)
- Fast incremental updates

**Daemon Mode (Opt-in):**

- `sync --daemon` or `sync --watch`
- File watcher (fsnotify) on `~/.claude/projects/**/*.jsonl`
- Real-time updates as messages arrive
- Graceful handling of in-progress sessions
- Can run as systemd/launchd service or background process

**Implementation:**

- Detect file changes by comparing mtime + size
- Incremental parse: only read new lines from existing sessions
- Transaction batching for efficiency

### 4. Session Resume Integration

**Claude Code Native Support:**

```bash
claude --resume <sessionId>           # Resume specific session
claude --resume <sessionId> --fork-session  # Fork from session
claude --continue                      # Continue most recent
```

**TUI Integration:**
When viewing a session in TUI, provide actions:

- **`r` - Resume Session**: Exec `cd <cwd> && claude --resume <sessionId>`
- **`f` - Fork Session**: Same with `--fork-session` flag
- **`c` - Copy Command**: Copy resume command to clipboard
- **`o` - Open in Terminal**: Launch in new terminal window (platform-specific)

**Execution Strategy:**

- Primary: Direct exec (replace TUI process with claude)
- Fallback: Spawn in new terminal (macOS: `open -a Terminal`, Linux: `gnome-terminal`, etc.)
- Last resort: Print command for manual copy/paste

**Context Restoration:**

- Use session's original `cwd` from metadata
- Preserve git context if branch info available
- Show session summary before resume (last few messages)

### 5. TUI Features (Bubbletea/Charm)

**Browse Mode:**

- List sessions (paginated, sorted by updated_at)
- Show: session title, date, message count, project path, model
- Keyboard navigation: j/k or arrows
- Filter/search sessions

**Search Mode:**

- Enter search query
- Show matching sessions with context snippets
- Highlight matches
- Navigate results

**Session View:**

- Display full conversation thread
- Follow `parentUuid` threading
- Show: timestamps, sender, tool calls, outputs
- Syntax highlighting for code blocks
- Navigate messages: j/k, gg/G (vim-style)
- In-conversation search: `/` to find text

**Session Actions:**

- `r` - Resume in Claude Code
- `f` - Fork session
- `c` - Copy resume command
- `e` - Export (JSON, Markdown, CSV)
- `v` - View raw JSONL
- `a` - View agent/subagent sessions
- `o` - Open in new terminal

**Navigation:**

- ESC acts as back button
- `q` quits entirely
- `/` for search within view
- Tab between panes (if multi-pane)

### 6. CLI Commands

```bash
# Import/Sync
claude-sessions sync [path]           # Import/update sessions
claude-sessions sync --daemon         # Run as background daemon
claude-sessions sync --watch          # Watch for changes (foreground)

# Search
claude-sessions search "query"        # Full-text search
claude-sessions search "query" --after 2025-01-01
claude-sessions search "query" --dir ~/xuku/invoice
claude-sessions search "query" --branch main

# List
claude-sessions list                  # List all sessions
claude-sessions list --project ~/xuku/invoice
claude-sessions list --format json

# View
claude-sessions view <sessionId>      # View session transcript
claude-sessions view <sessionId> --format markdown

# Export
claude-sessions export <sessionId>    # Export as markdown
claude-sessions export <sessionId> --format json

# Resume
claude-sessions resume <sessionId>    # Launch claude --resume
claude-sessions resume <sessionId> --fork

# TUI
claude-sessions tui                   # Launch interactive TUI
claude-sessions tui "search query"    # Launch TUI with search

# Stats
claude-sessions stats                 # Database statistics
```

## Technical Architecture

### Core vs Interface Pattern

**Principle: Core = Business Logic, Interface = Protocol-Specific Wrapper**

Based on Saša Jurić's "Towards Maintainable Elixir: The Core and the Interface" pattern, adapted for Go.

**Core Responsibilities (Business Logic):**

- Parse JSONL session files
- Validate session integrity (parentUuid threading, data consistency)
- Database operations (CRUD, transactions)
- Search query execution (FTS5)
- Import orchestration (incremental detection, batch processing)
- Business rules enforcement (e.g., session must have valid structure)
- All logic that runs REGARDLESS of how the system is accessed

**Interface Responsibilities (Protocol-Specific):**

- CLI: Flag parsing, argument normalization, exit codes, table formatting
- TUI: Keyboard handling, screen rendering, UI state management
- Input normalization: Converting weak inputs (strings, maps) to strongly-typed core models
- Output formatting: Converting core results to user-friendly display
- Error presentation: HTTP codes, user messages (core returns typed errors)

**Key Rules:**

1. **No business logic in interface** - Interface only normalizes input and formats output
2. **Core has precise signatures** - No `map[string]interface{}`, no generic errors, strong types only
3. **Interface depends on core, never reverse** - Core imports nothing from interface layer
4. **Protocol-specific code stays in interface** - CLI flag parsing, TUI key handling, etc.
5. **Universal code stays in core** - Anything that must run regardless of access method

**Example: Search Flow**

```go
// INTERFACE: CLI normalizes weak input
func searchCommand() *cobra.Command {
    return &cobra.Command{
        Run: func(cmd *cobra.Command, args []string) {
            // Input normalization (interface concern)
            query, err := parseSearchFlags(cmd)
            if err != nil {
                fmt.Fprintf(os.Stderr, "Error: %v\n", err)
                os.Exit(1)
            }

            // Call core with strongly-typed input
            repo := session.NewRepository(dbPath)
            results, err := repo.Search(query)

            // Output formatting (interface concern)
            printSearchResults(results)
        },
    }
}

// CORE: Strongly-typed, precise signature
func (r *Repository) Search(q SearchQuery) ([]Session, error) {
    // Business validation
    if len(strings.TrimSpace(q.Text)) == 0 {
        return nil, ErrEmptyQuery
    }
    // Execute search with business rules
    return r.executeSearch(q)
}
```

**Directory Structure:**

```
internal/
  core/                          # Pure business logic - NO UI dependencies
    session/
      repository.go              # Session CRUD operations
      importer.go               # Import orchestration and incremental sync
      search.go                 # Search query execution
    parser/
      jsonl.go                  # JSONL parsing logic
      validator.go              # Session structure validation
    models/
      types.go                  # Strongly-typed domain models
      errors.go                 # Typed error definitions
    db/
      schema.go                 # Database schema
      migrations.go             # Schema migrations

  interface/                     # Thin wrappers - Protocol-specific only
    cli/
      root.go                   # Cobra CLI setup
      sync.go                   # sync command - normalizes args → calls core
      search.go                 # search command - normalizes flags → calls core
      list.go                   # list command
      view.go                   # view command
      resume.go                 # resume command
    tui/
      app.go                    # Bubbletea app setup
      browse.go                 # Browse mode - UI state + core calls
      search.go                 # Search mode - UI state + core calls
      session.go                # Session view - UI state + core calls
      keys.go                   # Keyboard bindings
      styles.go                 # Lipgloss styling
```

**Testing Benefits:**

- Core tests have ZERO UI dependencies (no cobra, bubbletea imports)
- Interface tests can mock core with simple interfaces
- Business logic testable without any UI concerns
- Clear contracts make integration testing straightforward

### Database Schema (Preliminary)

**Tables:**

- `sessions` - Session metadata (sessionId, cwd, gitBranch, created_at, updated_at, etc.)
- `messages` - Individual messages (uuid, sessionId, parentUuid, type, sender, content, timestamp)
- `tool_uses` - Tool invocations (messageId, toolName, input, output, duration)
- `agents` - Subagent sessions (agentId, parentSessionId, description)
- `file_changes` - File history snapshots (sessionId, messageId, filePath, backupFile, version)
- `import_log` - Import tracking (filePath, fileHash, importedAt, status)

**Indexes:**

- sessionId, cwd, gitBranch, timestamp fields
- FTS5 virtual tables for message content
- Foreign keys with CASCADE for cleanup

### Technology Stack (Inspired by Shannon)

**Language:** Go

- Fast, single binary
- Excellent SQLite support (modernc.org/sqlite)
- Great CLI/TUI libraries

**Database:** SQLite

- Single file, embedded
- WAL mode for concurrent access
- FTS5 for full-text search

**TUI:** Bubbletea + Charm Libraries

- bubbletea: Event-driven TUI framework
- bubbles: Pre-built components (list, viewport, etc.)
- lipgloss: Styling/layout
- glamour: Markdown rendering

**File Watching:** fsnotify

- Cross-platform file system notifications
- Efficient event-based monitoring

**Concurrency:**

- Goroutines for parallel import
- Channel-based coordination
- Single SQLite writer, multiple readers

### Parallel Import Strategy

**Challenge:** SQLite = single writer

**Solution:**

1. Parse sessions in parallel (goroutines)
2. Channel to collect parsed data
3. Single writer goroutine batches inserts
4. Transaction per session or batch of N sessions
5. Progress reporting via channels

**Pseudo-code:**

```go
// Worker pool pattern
for _, sessionFile := range files {
    go func(file string) {
        parsed := parseSession(file)
        parsedChan <- parsed
    }(sessionFile)
}

// Single writer
go func() {
    tx := db.Begin()
    for parsed := range parsedChan {
        insertSession(tx, parsed)
        if count % batchSize == 0 {
            tx.Commit()
            tx = db.Begin()
        }
    }
    tx.Commit()
}()
```

### Incremental Sync Algorithm

**For each session file:**

1. Compute file hash (SHA256)
2. Check if hash exists in `import_log`
3. If new: full import
4. If exists: compare file size/mtime
5. If changed:
   - Read only new lines (seek to last known position)
   - Parse new entries
   - Append to existing session
   - Update hash/mtime

**Optimization:**

- Store last line count in DB
- Seek to byte offset of last message
- Read remaining lines only

## Success Criteria

1. **Import Performance:**
   - 1000 sessions in < 10 seconds
   - Incremental sync < 1 second for small updates
   - No data loss from original JSONL

2. **Search Performance:**
   - Sub-second search across 10K messages
   - Responsive UI during search

3. **Resume Reliability:**
   - 100% success rate launching claude with correct session
   - Correct cwd restoration

4. **Sync Daemon:**
   - < 1% CPU idle
   - Detect new messages within 1 second

5. **TUI Usability:**
   - Vim-like navigation
   - No UI lag on large sessions
   - Clean markdown rendering

## Open Questions

1. **Agent/Subagent Linking:**
   - How to display agent sessions in relation to parent?
   - Nested view or separate list?

2. **Branch Support:**
   - Claude Code supports conversation branching (`isSidechain: true`)
   - How to visualize/navigate branches in TUI?

3. **Tool Output Storage:**
   - Store full stdout/stderr inline or separate table?
   - Binary data handling (images)?

4. **Config File:**
   - Where: `~/.config/claude-sessions/config.yaml`?
   - What to configure: sync interval, TUI theme, default filters?

5. **Platform-Specific Resume:**
   - Detect terminal emulator automatically?
   - User preference for terminal app?
   - How to handle SSH sessions?

## Phase 1: Importer (MVP)

**Deliverables:**

- Parse JSONL session files
- SQLite schema with all fields
- Import command with progress
- Incremental sync support
- Basic stats command

**Out of Scope (Phase 1):**

- TUI (comes in Phase 2)
- Daemon mode (manual sync only)
- Advanced filtering

## Phase 2: TUI & Search

**Deliverables:**

- Bubbletea TUI
- Browse sessions
- Full-text search
- Session view with threading
- Resume integration
- Export commands

**Out of Scope (Phase 2):**

- Daemon mode
- Branch visualization
- Agent session navigation

## Phase 3: Advanced Features

**Potential Features:**

- Sync daemon with fsnotify
- Branch visualization
- Agent session trees
- Advanced analytics (token usage, model stats)
- Session comparisons
- Conversation export to share
