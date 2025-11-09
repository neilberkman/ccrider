# CCRider MCP Server Design

## Overview

An MCP (Model Context Protocol) server that allows Claude Code to search and retrieve information from other Claude Code sessions. This enables Claude to reference past conversations, solutions, and context from the user's session history.

## Use Cases

1. **Find similar problems**: "Have I solved something like this before?"
2. **Retrieve past solutions**: "How did I implement X last time?"
3. **Context discovery**: "What conversations mention database migrations?"
4. **Session resume**: "Show me the sessions about the authentication system"

## Proposed Tools

### `search_sessions`

Search across all sessions for a query string.

**Arguments:**

- `query` (required): Search term to match against message content
- `limit` (optional): Max number of sessions to return (default: 10)
- `project` (optional): Filter by project path

**Returns:**

```json
{
  "sessions": [
    {
      "session_id": "abc123...",
      "summary": "Fix authentication bug",
      "project": "/Users/neil/xuku/myapp",
      "updated_at": "2025-01-08T10:30:00Z",
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

Get recent sessions, optionally filtered by project.

**Arguments:**

- `limit` (optional): Max sessions to return (default: 20)
- `project` (optional): Filter by project path
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
      "message_count": 42
    }
  ]
}
```

## Implementation Notes

### Architecture (Based on Clippy Pattern)

Following clippy's library-first approach:

1. **Core Logic** (`internal/core/`): Already exists with db, importer, parser
2. **MCP Server** (`cmd/ccrider/mcp/server.go`): New package for MCP protocol
3. **Main Entry** (`cmd/ccrider/main.go`): Add `serve-mcp` subcommand

### Technology Stack

- **MCP Framework**: `github.com/mark3labs/mcp-go` (same as clippy)
- **Database**: Existing SQLite database at `~/.config/ccrider/sessions.db`
- **Server Protocol**: stdio-based MCP server (standard for Claude Desktop)

### Database Queries

Leverage existing TUI queries:

- Search: Already implemented in `internal/interface/tui/messages.go:performSearch()`
- Session detail: Already implemented in `loadSessionDetail()`
- Recent sessions: Already implemented in `loadSessions()`

Can factor these into reusable core functions.

### Configuration

MCP servers are configured in Claude Desktop's config file:

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

### Phase 1: Basic MCP Server

1. Add `github.com/mark3labs/mcp-go` dependency
2. Create `cmd/ccrider/mcp/server.go` (following clippy pattern)
3. Implement `search_sessions` tool
4. Add `serve-mcp` subcommand to main.go
5. Test with Claude Desktop

### Phase 2: Full Toolset

1. Implement `get_session_detail` tool
2. Implement `list_recent_sessions` tool
3. Add project filtering support
4. Optimize queries for MCP usage

### Phase 3: Advanced Features

1. Semantic search (if feasible)
2. Session tagging/categorization
3. Cross-project pattern detection
4. Export capabilities

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
        Description: "Search Claude Code sessions",
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

1. Claude Code can search past sessions via MCP
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
