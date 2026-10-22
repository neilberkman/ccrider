# CCRider MCP Server Design

## Overview

An MCP (Model Context Protocol) server that allows coding agents to search and retrieve information from imported session history. This enables agents to reference past conversations, solutions, and context from the user's local database.

## Use Cases

1. **Find similar problems**: "Have I solved something like this before?"
2. **Retrieve past solutions**: "How did I implement X last time?"
3. **Context discovery**: "What conversations mention database migrations?"
4. **Session resume**: "Show me the sessions about the authentication system"

## Proposed Tools

### `search_sessions`

Search across all sessions for a query string. Supports current session awareness for compact cycles and date filtering.

**Arguments:**

- `query` (required): Search term to match against message content
- `limit` (optional): Max number of sessions to return (default: 10)
- `project` (optional): Filter by project path
- `provider` (optional): Filter by provider (`claude`, `codex`, `copilot`, `opencode`, `pi`, `antigravity`, `antigravity-ide`, `antigravity-desktop`, or `amp`)
- `current_session_id` (optional): Current session ID - if provided, searches ONLY within this session (useful for finding earlier parts of current conversation)
- `exclude_current` (optional): If true, excludes current session from results (searches only other sessions). Requires current_session_id to be set.
- `after_date` (optional): Only sessions updated after this date (ISO 8601 format, e.g. '2025-01-01' or '2025-01-08T10:00:00Z')
- `before_date` (optional): Only sessions updated before this date (ISO 8601 format)
- `anchor_phrase` (optional): Find sessions containing this phrase, then search within those sessions for `query`. Anchor lookup defaults to matches from the last hour unless `after_date` or `before_date` is supplied.
- `exact_match` (optional): Treat `query` as a phrase rather than an FTS expression. Useful for searching for a known tag, including hyphenated tags, across older conversations.

**Returns:**

```json
{
  "sessions": [
    {
      "session_id": "abc123...",
      "summary": "Fix authentication bug",
      "project": "/Users/neil/xuku/myapp",
      "updated_at": "2025-01-08T10:30:00Z",
      "resume_command": "cd '/Users/neil/xuku/myapp' && claude --resume 'abc123...'",
      "match_count": 3,
      "matches": [
        {
          "message_type": "user",
          "snippet": "The authentication token is expiring too quickly...",
          "sequence": 5
        }
      ]
    }
  ]
}
```

### `generate_session_anchor`

Generate and persist an anchor for context recovery or later retrieval. This tool writes to the local database and is annotated accordingly.

**Arguments:**

- `label` (optional): A descriptive label of up to 120 characters, supplied by the calling agent. The generated phrase uses the label's words with a random suffix, e.g. `add-rest-endpoint-a12bc34de56f`. Omitting the label generates four random words.
- `session_id` (optional): A known session ID to attach immediately. Full IDs and unambiguous bare UUIDs are accepted; unknown or ambiguous IDs are rejected.

The response preserves `anchor` and `instruction`, and adds `label` and `created_at`. It also includes `session_id` when explicitly attached. With a known session ID, the bookmark works immediately without putting its phrase in the transcript. Otherwise:

1. Call `generate_session_anchor`.
2. Include the returned `anchor` in an assistant response and ask the user to reply so the response can be written to the transcript.
3. After the reply, call `search_sessions` with `anchor_phrase` set to that phrase and `query` set to the topic to retrieve.

The search syncs local transcripts before querying. A phrase already visible in the conversation can also identify the session without generating a new one. The user does not need to record the generated phrase for this context-recovery workflow.

For later retrieval, use `list_session_anchors`. For a known tag in an older conversation, ordinary `search_sessions` with the tag as `query` and `exact_match: true` also avoids the default one-hour anchor-lookup window. The `anchor_phrase` filter still searches transcript text; use the saved session ID when an anchor was attached without writing its phrase into the conversation.

### `list_session_anchors`

List saved anchors, newest first, with labels, creation times, and matching sessions. Local transcripts are synced before lookup, and matching has no date cutoff.

**Arguments:**

- `session_id` (optional): Only anchors associated with this session. Full IDs and unambiguous bare UUIDs are accepted.
- `project` (optional): Filter associated sessions by project path substring.
- `provider` (optional): Filter associated sessions by provider.
- `limit` (optional): Anchors per page, default 20, capped at 100.
- `offset` (optional): Anchors to skip, default 0. Continue with the previous response's `next_offset`.

**Returns:**

```json
{
  "anchors": [
    {
      "anchor": "add-rest-endpoint-a12bc34de56f",
      "label": "Add REST endpoint",
      "created_at": "2026-10-22T21:00:00Z",
      "session_id": "abc123",
      "session_count": 1,
      "sessions": [
        {
          "session_id": "abc123",
          "summary": "Add a REST endpoint",
          "project": "/repo",
          "updated_at": "2026-10-22 21:00:00",
          "provider": "claude",
          "resume_command": "cd '/repo' && claude --resume 'abc123'",
          "message_count": 42
        }
      ]
    }
  ],
  "total": 1
}
```

`session_id` at anchor level is present only for an explicit binding. Otherwise, `sessions` contains all eligible literal transcript matches; copied phrases can match several conversations. Repeated occurrences within one session produce one entry. An unfiltered list includes anchors with no matches yet (`sessions: []`); session/project/provider filters omit those anchors. This lets an agent discover a newly generated bookmark before its response has been indexed.

Responses include `next_offset` when more anchors remain. Session summaries are abbreviated for display; large session lists can be shortened with `sessions_truncated: true`, while `session_count` records the full matching count. Use a listed session ID with `get_session_messages` for conversation content.

The registry starts with anchors generated after persistence was introduced. Older generated phrases and manually written tags are not automatically registered; they remain searchable as ordinary text. Anchor records survive restarts and message reimports in the same database.

### `get_session_detail`

Retrieve full conversation for a specific session.

**Arguments:**

- `session_id` (required): Session UUID to retrieve

**Returns:**

```json
{
  "session": {
    "session_id": "abc123...",
    "summary": "Fix authentication bug",
    "project": "/Users/neil/xuku/myapp",
    "created_at": "2025-01-08T09:00:00Z",
    "updated_at": "2025-01-08T10:30:00Z",
    "message_count": 42
  },
  "messages": [
    {
      "type": "user",
      "content": "I'm seeing authentication tokens expire too quickly...",
      "timestamp": "2025-01-08T09:01:00Z",
      "sequence": 1
    }
  ]
}
```

### `list_recent_sessions`

Get recent sessions, optionally filtered by project or provider.

**Arguments:**

- `limit` (optional): Max sessions to return (default: 20)
- `project` (optional): Filter by project path
- `provider` (optional): Filter by provider (`claude`, `codex`, `copilot`, `opencode`, `pi`, `antigravity`, `antigravity-ide`, `antigravity-desktop`, or `amp`)
- `since` (optional): Only sessions updated since this timestamp

**Returns:**

```json
{
  "sessions": [
    {
      "session_id": "abc123...",
      "summary": "Fix authentication bug",
      "project": "/Users/neil/xuku/myapp",
      "updated_at": "2025-01-08T10:30:00Z",
      "resume_command": "cd '/Users/neil/xuku/myapp' && claude --resume 'abc123...'",
      "message_count": 42
    }
  ]
}
```

## Implementation Notes

### Architecture (Based on Clippy Pattern)

Following clippy's library-first approach with core/interface separation:

1. **Core Logic** (`internal/core/`): Database queries and business logic
2. **MCP Interface** (`cmd/ccrider/mcp/server.go`): MCP protocol implementation
3. **CLI Entry** (`internal/interface/cli/mcp.go`): serve-mcp subcommand

### Database Sync Strategy

All MCP tools automatically sync the database before executing queries to ensure up-to-date results. The sync is:

- **Silent**: No progress output to avoid polluting MCP responses
- **Incremental**: Only imports new or changed sessions (hash-based deduplication)
- **Centralized**: Single `syncDatabase()` function called by all tool handlers
- **Local-only**: MCP request syncs refresh local sources with a short deadline and never contact Amp or other cloud sources. Previously imported Amp sessions remain searchable from the local cache.

### Technology Stack

- **MCP Framework**: `github.com/mark3labs/mcp-go` (same as clippy)
- **Database**: Existing SQLite database at `~/.config/ccrider/sessions.db`
- **Server Protocol**: stdio-based MCP server for MCP-capable clients

### Database Queries

Leverage existing TUI queries:

- Search: Already implemented in `internal/interface/tui/messages.go:performSearch()`
- Session detail: Already implemented in `loadSessionDetail()`
- Recent sessions: Already implemented in `loadSessions()`

Can factor these into reusable core functions.

### Configuration

Configure an MCP-capable client to run `ccrider serve-mcp`. Claude Desktop uses this config form:

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

## Development Plan

### Phase 1: Basic MCP Server ✅

1. ✅ Add `github.com/mark3labs/mcp-go` dependency
2. ✅ Create `cmd/ccrider/mcp/server.go` (following clippy pattern)
3. ✅ Implement `search_sessions` tool with enhanced parameters
4. ✅ Add `serve-mcp` subcommand via `internal/interface/cli/mcp.go`
5. ✅ Add automatic database sync to all tools

### Phase 2: Full Toolset ✅

1. ✅ Implement `get_session_detail` tool
2. ✅ Implement `list_recent_sessions` tool
3. ✅ Add project filtering support
4. ✅ Add current session awareness (compact cycles support)
5. ✅ Add date filtering (after_date, before_date)
6. ✅ Optimize queries for MCP usage

### Phase 3: Advanced Features (Future)

1. Semantic search (if feasible)
2. Session tagging/categorization
3. Cross-project pattern detection
4. Export capabilities

## Current Status

**Implemented Features:**

- ✅ Three MCP tools: search_sessions, get_session_detail, list_recent_sessions
- ✅ Current session awareness (search within current session or exclude it)
- ✅ Date filtering (ISO 8601 format)
- ✅ Project filtering
- ✅ Automatic database sync before each query
- ✅ Silent incremental sync (hash-based deduplication)
- ✅ Full conversation retrieval
- ✅ Session grouping (up to 3 match snippets per session)

**Configuration Example:**

Generic MCP client config form:

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

## Reference Implementation

See `~/xuku/clippy/cmd/clippy/mcp/server.go` for:

- MCP server setup pattern
- Tool registration
- Argument parsing
- Result formatting
- Error handling

Key pattern from clippy:

```go
// StartServer starts the MCP server
func StartServer() error {
    s := server.NewMCPServer(
        "ccrider",
        "1.0.0",
        server.WithToolCapabilities(true),
    )

    // Register tools
    s.AddTool(mcp.Tool{
        Name: "search_sessions",
        Description: "Search coding agent sessions",
        InputSchema: mcp.ToolInputSchema{
            Type: "object",
            Properties: map[string]interface{}{
                "query": map[string]interface{}{
                    "type": "string",
                    "description": "Search query",
                },
            },
            Required: []string{"query"},
        },
    }, searchSessionsHandler)

    return s.Serve()
}
```

## Success Criteria

1. MCP clients can search past sessions via MCP
2. Results are relevant and well-formatted
3. Performance is acceptable (<500ms for typical searches)
4. Integration is seamless for users
5. Documentation enables easy setup

## Future Considerations

- Multi-user support (if needed)
- Cloud sync integration
- Real-time session updates
- Session analytics/insights
- Integration with other MCP servers
