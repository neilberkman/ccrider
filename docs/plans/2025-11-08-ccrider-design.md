# ccrider - Claude Code Session Manager

**Design Document**
Date: 2025-11-08
Status: Draft

## Executive Summary

`ccrider` is a terminal-based tool for managing, searching, and resuming Claude Code sessions. Unlike existing tools which suffer from incomplete schema support and poor user experience, `ccrider` provides a robust SQLite-backed search engine with a polished TUI and native integration with Claude Code's resume functionality.

**Name Origin:** "CC Rider" - Claude Code + the metaphor of "riding" through sessions, navigating conversation history.

## Problem Statement

Claude Code stores session data in JSONL files across `~/.claude/projects/`, but:

1. **Existing tools are broken** - Can't parse all message types (file-history-snapshot, etc.)
2. **No real search** - Just grep or broken implementations
3. **No session management** - Can't easily resume old sessions
4. **Overcomplicated** - Web UIs with broken builds and dependencies
5. **No incremental updates** - Must re-parse entire sessions

Users need a reliable way to:

- Search across all historical sessions (full-text)
- Filter by project, date, model, branch
- Resume sessions with one keystroke
- Track session evolution over time

## Solution Overview

`ccrider` is a Go monolith with strict core/interface separation, providing:

- **SQLite backend** with FTS5 for fast full-text search
- **Incremental sync** that detects new messages in ongoing sessions
- **Bubbletea TUI** for browsing, searching, and resuming
- **Native resume integration** via `claude --resume <sessionId>`
- **Reusable libraries** for JSONL parsing and SQLite sync

## Architecture

### Core vs Interface Pattern

Based on Saša Jurić's "Towards Maintainable Elixir: The Core and the Interface" adapted for Go.

**Core Layer (Business Logic):**

```
internal/core/
  session/       # Session repository, CRUD operations
  parser/        # JSONL parsing logic
  importer/      # Import orchestration, incremental sync
  search/        # FTS5 query execution
  models/        # Strongly-typed domain models
  db/            # Database schema and migrations
```

**Interface Layer (Protocol-Specific):**

```
internal/interface/
  cli/           # Cobra commands - thin wrappers
  tui/           # Bubbletea UI - keyboard + rendering only
```

**Key Principle:** Core has ZERO dependencies on interface. Interface imports core, never reverse.

### Reusable Packages

**Package: `pkg/ccsessions`** - JSONL Parser

- Pure parsing library, no DB or UI
- Converts JSONL → strongly-typed Go structs
- Supports all 5 message types from schema
- Validates parentUuid threading
- Usable standalone: `import "github.com/you/ccrider/pkg/ccsessions"`

**Package: `pkg/ccsessionsdb`** - SQLite Sync

- Takes parsed sessions → SQLite
- FTS5 setup with dual tokenizers
- Incremental update detection
- Import orchestration
- Usable standalone for custom tools

**Example Usage:**

```go
// Just parse sessions
import "github.com/you/ccrider/pkg/ccsessions"
session, err := ccsessions.ParseFile("/path/to/session.jsonl")

// Parse + sync to DB
import "github.com/you/ccrider/pkg/ccsessionsdb"
db := ccsessionsdb.New("sessions.db")
db.Import(session)
```

## Data Model

### Database Schema

**sessions table:**

```sql
CREATE TABLE sessions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT UNIQUE NOT NULL,      -- UUID from filename
    project_path TEXT NOT NULL,            -- Normalized project path
    summary TEXT,                          -- From summary entry
    leaf_uuid TEXT,                        -- Last message UUID
    cwd TEXT,                              -- Working directory
    git_branch TEXT,                       -- Git branch
    created_at DATETIME,
    updated_at DATETIME,
    message_count INTEGER DEFAULT 0,
    version TEXT,                          -- Claude Code version
    imported_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    last_synced_at DATETIME,
    file_hash TEXT,                        -- SHA256 for change detection
    file_size INTEGER,
    file_mtime DATETIME
);

CREATE INDEX idx_sessions_session_id ON sessions(session_id);
CREATE INDEX idx_sessions_project_path ON sessions(project_path);
CREATE INDEX idx_sessions_updated_at ON sessions(updated_at);
CREATE INDEX idx_sessions_git_branch ON sessions(git_branch);
```

**messages table:**

```sql
CREATE TABLE messages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    uuid TEXT UNIQUE NOT NULL,
    session_id INTEGER NOT NULL,
    parent_uuid TEXT,                      -- Links messages
    type TEXT NOT NULL,                    -- user, assistant, system, etc.
    sender TEXT,                           -- human, assistant (for user/assistant types)
    content TEXT,                          -- Full message content (JSON)
    text_content TEXT,                     -- Extracted text for FTS
    timestamp DATETIME NOT NULL,
    sequence INTEGER,                      -- Order within session
    is_sidechain BOOLEAN,
    cwd TEXT,
    git_branch TEXT,
    version TEXT,
    FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
);

CREATE INDEX idx_messages_uuid ON messages(uuid);
CREATE INDEX idx_messages_session_id ON messages(session_id);
CREATE INDEX idx_messages_parent_uuid ON messages(parent_uuid);
CREATE INDEX idx_messages_timestamp ON messages(timestamp);
```

**tool_uses table:**

```sql
CREATE TABLE tool_uses (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    message_id INTEGER NOT NULL,
    tool_name TEXT NOT NULL,
    tool_id TEXT,                          -- toolu_xxx identifier
    input JSON,                            -- Tool input params
    output JSON,                           -- Tool result
    created_at DATETIME,
    FOREIGN KEY (message_id) REFERENCES messages(id) ON DELETE CASCADE
);

CREATE INDEX idx_tool_uses_message_id ON tool_uses(message_id);
CREATE INDEX idx_tool_uses_tool_name ON tool_uses(tool_name);
```

**file_snapshots table:**

```sql
CREATE TABLE file_snapshots (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id INTEGER NOT NULL,
    message_id INTEGER,
    file_path TEXT NOT NULL,
    backup_filename TEXT,
    version INTEGER,
    backup_time DATETIME,
    FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE,
    FOREIGN KEY (message_id) REFERENCES messages(id) ON DELETE CASCADE
);
```

**import_log table:**

```sql
CREATE TABLE import_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    file_path TEXT NOT NULL,
    file_hash TEXT NOT NULL,
    imported_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    sessions_imported INTEGER,
    messages_imported INTEGER,
    status TEXT CHECK(status IN ('success', 'partial', 'failed')),
    error_message TEXT
);

CREATE INDEX idx_import_log_file_hash ON import_log(file_hash);
```

**FTS5 Tables:**

```sql
-- Natural language search with stemming
CREATE VIRTUAL TABLE messages_fts USING fts5(
    text_content,
    content=messages,
    content_rowid=id,
    tokenize='porter unicode61'
);

-- Code search without stemming (preserves symbols, camelCase)
CREATE VIRTUAL TABLE messages_fts_code USING fts5(
    text_content,
    content=messages,
    content_rowid=id,
    tokenize='unicode61'
);

-- Triggers to keep FTS in sync
CREATE TRIGGER messages_ai AFTER INSERT ON messages BEGIN
    INSERT INTO messages_fts(rowid, text_content) VALUES (new.id, new.text_content);
    INSERT INTO messages_fts_code(rowid, text_content) VALUES (new.id, new.text_content);
END;

CREATE TRIGGER messages_ad AFTER DELETE ON messages BEGIN
    DELETE FROM messages_fts WHERE rowid = old.id;
    DELETE FROM messages_fts_code WHERE rowid = old.id;
END;

CREATE TRIGGER messages_au AFTER UPDATE ON messages BEGIN
    UPDATE messages_fts SET text_content = new.text_content WHERE rowid = new.id;
    UPDATE messages_fts_code SET text_content = new.text_content WHERE rowid = new.id;
END;
```

### Message Type Handling

**Based on research/schema.md**, we support 5 entry types:

1. **summary** - First line, session metadata
2. **user** - User messages with optional toolUseResult
3. **assistant** - Claude responses with content blocks (text, tool_use)
4. **system** - System messages (commands, status)
5. **file-history-snapshot** - File backup tracking

Each type is parsed into appropriate tables with full data preservation.

## Import & Sync Strategy

### Initial Import

```
1. Discover all .jsonl files in ~/.claude/projects/
2. For each file:
   a. Compute SHA256 hash
   b. Check if already imported (import_log)
   c. If new: parse + insert
3. Use goroutine pool for parallel parsing
4. Single writer goroutine for DB inserts
5. Batch transactions (100 messages per tx)
```

### Incremental Sync

```
1. For each known session file:
   a. Check mtime + file size vs DB
   b. If changed:
      - Compute new hash
      - Seek to last known byte offset
      - Read only new lines
      - Parse new messages
      - Append to existing session
      - Update hash/mtime
2. Detect new session files (not in import_log)
3. Full import for new files
```

**Optimization:** Store `last_byte_offset` in import_log to enable efficient incremental reads.

### Parallel Import

```go
// Worker pool pattern
const numWorkers = 8

parsedCh := make(chan ParsedSession, 100)
errCh := make(chan error, 1)

// Parser workers
for i := 0; i < numWorkers; i++ {
    go func() {
        for file := range fileCh {
            session, err := parser.ParseFile(file)
            if err != nil {
                errCh <- err
                return
            }
            parsedCh <- session
        }
    }()
}

// Single DB writer
go func() {
    tx := db.Begin()
    count := 0
    for session := range parsedCh {
        db.InsertSession(tx, session)
        count++
        if count%100 == 0 {
            tx.Commit()
            tx = db.Begin()
        }
    }
    tx.Commit()
}()
```

## Search Implementation

### Query Types

**Full-Text Search:**

```go
type SearchQuery struct {
    Text       string      // FTS query
    Sender     string      // "human", "assistant", or empty
    AfterDate  time.Time   // Filter by timestamp
    BeforeDate time.Time
    ProjectDir string      // Filter by project path
    GitBranch  string      // Filter by branch
    ToolUsed   string      // Sessions that used specific tool
    Limit      int         // Result limit
    Offset     int         // Pagination
}
```

**FTS Query Construction:**

```sql
-- Natural language query with stemming
SELECT DISTINCT s.* FROM sessions s
JOIN messages m ON m.session_id = s.id
JOIN messages_fts fts ON fts.rowid = m.id
WHERE messages_fts MATCH ?
  AND s.project_path LIKE ?
  AND s.git_branch = ?
  AND m.timestamp > ?
ORDER BY s.updated_at DESC
LIMIT ? OFFSET ?

-- Code query without stemming
-- (Auto-detect if query contains code patterns: camelCase, snake_case, symbols)
SELECT ... FROM messages_fts_code WHERE ...
```

**Auto-Detection of Code Queries:**

```go
func isCodeQuery(text string) bool {
    // Has camelCase
    if regexp.MustCompile(`[a-z][A-Z]`).MatchString(text) {
        return true
    }
    // Has symbols
    if regexp.MustCompile(`[_\-\./:{}()\[\]]`).MatchString(text) {
        return true
    }
    return false
}
```

## TUI Design

### Modes

**1. Browse Mode**

```
┌─ Sessions ──────────────────────────────────────┐
│ [1] Fix authentication bug                      │
│     ~/xuku/invoice • main • 2h ago • 45 msgs    │
│                                                  │
│ [2] Add user registration                       │
│     ~/xuku/invoice • feat/auth • 1d ago • 23... │
│                                                  │
│ > [3] Elixir migration help                     │
│     ~/xuku/clippy • main • 3d ago • 12 msgs     │
│                                                  │
│ [4] Shannon TUI improvements                    │
│     ~/xuku/shannon • main • 1w ago • 67 msgs    │
└──────────────────────────────────────────────────┘
Keys: ↑↓/jk=navigate  /=search  r=resume  q=quit
```

**2. Search Mode**

```
┌─ Search: "authentication bug" ──────────────────┐
│                                                  │
│ [1] Fix authentication bug (3 matches)          │
│     ...discussed the auth flow and found the... │
│     ~/xuku/invoice • 2h ago                     │
│                                                  │
│ [2] User login issue (1 match)                  │
│     ...authentication token expires too fast... │
│     ~/xuku/api • 2d ago                         │
└──────────────────────────────────────────────────┘
Keys: ↑↓=navigate  Enter=view  Esc=back  q=quit
```

**3. Session View**

```
┌─ Fix authentication bug ─────────────────────────┐
│ Session: a5e26ba8... • ~/xuku/invoice • main    │
│ Created: 2h ago • 45 messages                    │
├──────────────────────────────────────────────────┤
│                                                  │
│ [human] 2h ago                                   │
│ yo help with this auth bug...                    │
│                                                  │
│ [assistant] 2h ago                               │
│ I'll help debug the authentication issue.       │
│                                                  │
│ [tool: Bash] git log --oneline -10              │
│ Output: 14d827d Fix unused variable...          │
│                                                  │
│ [human] 2h ago                                   │
│ perfect thanks                                   │
└──────────────────────────────────────────────────┘
Keys: ↑↓/jk=scroll  gg/G=top/bottom  /=find
      r=resume  f=fork  c=copy  Esc=back  q=quit
```

### Key Bindings

**Global:**

- `q` - Quit
- `/` - Search
- `Esc` - Back/Cancel

**Browse Mode:**

- `j/k` or `↑↓` - Navigate sessions
- `Enter` - View session
- `r` - Resume selected session
- `f` - Fork selected session
- `c` - Copy resume command
- `d` - Filter by date
- `p` - Filter by project
- `s` - Sort options

**Session View:**

- `j/k` or `↑↓` - Scroll messages
- `gg` - Go to top
- `G` - Go to bottom
- `/` - Find in session
- `n/N` - Next/prev find match
- `r` - Resume this session
- `f` - Fork this session
- `c` - Copy resume command
- `e` - Export session

## Resume Integration

### Implementation

```go
// internal/core/session/resume.go
type ResumeCommand struct {
    SessionID string
    CWD       string
    Fork      bool
}

func (r *Repository) BuildResumeCommand(sessionID string, fork bool) (*ResumeCommand, error) {
    session, err := r.GetSession(sessionID)
    if err != nil {
        return nil, err
    }

    return &ResumeCommand{
        SessionID: session.SessionID,
        CWD:       session.CWD,
        Fork:      fork,
    }, nil
}

// internal/interface/tui/session.go
func (m sessionViewModel) resumeSession(fork bool) tea.Cmd {
    return func() tea.Msg {
        cmd, err := m.repo.BuildResumeCommand(m.session.SessionID, fork)
        if err != nil {
            return errorMsg{err}
        }

        // Strategy 1: Direct exec (replace TUI process)
        bashCmd := fmt.Sprintf("cd %s && claude --resume %s",
            shellQuote(cmd.CWD),
            cmd.SessionID)
        if fork {
            bashCmd += " --fork-session"
        }

        // Replace current process
        return tea.ExecProcess(exec.Command("bash", "-c", bashCmd), nil)
    }
}
```

### Execution Strategies

**Primary: Direct Exec**

```go
// Replace TUI with claude process
tea.ExecProcess(exec.Command("bash", "-c", "cd ... && claude --resume ..."), nil)
```

**Fallback: New Terminal**

```go
// macOS
exec.Command("open", "-a", "Terminal", "-n", "--args", "bash", "-c", cmd)

// Linux (detect terminal)
exec.Command("gnome-terminal", "--", "bash", "-c", cmd)
exec.Command("kitty", "bash", "-c", cmd)
```

**Last Resort: Copy to Clipboard**

```go
import "github.com/atotto/clipboard"
clipboard.WriteAll(cmd)
fmt.Println("Resume command copied to clipboard!")
```

## CLI Commands

```bash
# Sync
ccrider sync                    # Incremental sync (default)
ccrider sync --full            # Full re-import
ccrider sync --watch           # Watch mode (foreground)
ccrider sync --daemon          # Daemon mode (future)

# Search
ccrider search "query"         # Full-text search
ccrider search "query" --after 2025-01-01
ccrider search "query" --dir ~/xuku/invoice
ccrider search "query" --branch main --sender human

# List
ccrider list                   # List all sessions
ccrider list --project ~/xuku/invoice
ccrider list --limit 20
ccrider list --format json

# View
ccrider view <sessionId>       # View session
ccrider view <sessionId> --format markdown

# Resume
ccrider resume <sessionId>     # Launch claude --resume
ccrider resume <sessionId> --fork

# TUI
ccrider tui                    # Launch TUI
ccrider tui "query"           # Launch with search

# Stats
ccrider stats                  # Database stats
ccrider stats --sessions       # Session stats
ccrider stats --usage          # Token usage stats
```

## Testing Strategy

### Core Tests (Pure Business Logic)

```go
// internal/core/parser/jsonl_test.go
func TestParseSessionFile(t *testing.T) {
    // NO UI dependencies
    // Test all 5 message types
    // Test parentUuid threading
    // Test malformed JSON
}

// internal/core/session/repository_test.go
func TestIncrementalSync(t *testing.T) {
    // Create temp DB
    // Import session
    // Append messages to file
    // Sync again
    // Verify only new messages imported
}
```

### Interface Tests (Mocked Core)

```go
// internal/interface/tui/browse_test.go
type mockRepository struct {
    sessions []core.Session
}

func TestBrowseNavigation(t *testing.T) {
    mock := &mockRepository{sessions: testSessions}
    model := newBrowseModel(mock)

    // Test keyboard nav
    model.Update(tea.KeyMsg{Type: tea.KeyDown})
    // Assert cursor moved
}
```

## Implementation Phases

### Phase 1: Core & CLI (MVP)

**Deliverables:**

- [ ] JSONL parser (`pkg/ccsessions`)
- [ ] SQLite schema + migrations
- [ ] Import orchestration with incremental sync
- [ ] Basic CLI: `sync`, `list`, `search`, `stats`
- [ ] Full test coverage for core

**Out of Scope:**

- TUI (Phase 2)
- Daemon mode
- Advanced filtering

**Success Criteria:**

- Import 1000 sessions in < 10 seconds
- Incremental sync detects new messages correctly
- Search returns results in < 100ms

### Phase 2: TUI & Resume

**Deliverables:**

- [ ] Bubbletea TUI with browse/search/session views
- [ ] Resume integration (`r`, `f` keys)
- [ ] In-session search (`/`)
- [ ] Export functionality

**Out of Scope:**

- Daemon mode
- Advanced analytics

**Success Criteria:**

- Resume works 100% of time with correct cwd
- TUI responsive on sessions with 1000+ messages
- Search highlights work correctly

### Phase 3: Polish & Advanced

**Deliverables:**

- [ ] Daemon mode with fsnotify
- [ ] Token usage analytics
- [ ] Session comparison
- [ ] Advanced filters (tool usage, model, etc.)

## Open Questions

1. **Project Name:** `ccrider` vs alternatives?
2. **Agent sessions:** How to display subagent sessions in relation to parent?
3. **Branch visualization:** How to show conversation branches in TUI?
4. **Config:** Where to store config? `~/.config/ccrider/config.yaml`?
5. **Platform-specific resume:** Auto-detect terminal emulator or config setting?

## References

- Schema documentation: `research/schema.md`
- Requirements: `research/requirements.md`
- Shannon (inspiration): `~/xuku/shannon`
- Core vs Interface pattern: Saša Jurić's article
- Claude Code resume flags: `claude --help`

## Success Metrics

**Technical:**

- 100% schema coverage (all 5 message types)
- Sub-second search on 10K messages
- Zero data loss from original JSONL
- Incremental sync < 1s for typical updates

**User Experience:**

- One-key resume (`r`) that works every time
- Search results in < 100ms
- TUI never lags
- Better than grep for finding old sessions

**Adoption:**

- Reusable parser used by other projects
- Community contributions to improve search
- Replaces broken existing tools
