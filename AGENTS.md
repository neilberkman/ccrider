# Core/Interface Separation Philosophy

This codebase follows strict **Core/Interface separation** based on Saša Jurić's ["Towards Maintainable Elixir: The Core and the Interface"](https://www.theerlangelist.com/article/core_and_interface).

## Principles

### CORE Layer (`internal/core/`)

The core contains **business logic** that applies REGARDLESS of which interface uses it:

1. **Data Retrieval**: All database queries, FTS searches, external API calls
2. **Business Rules**: Filtering out warmup sessions, summary fallback logic, validation
3. **Data Transformation**: Converting raw database rows into domain types
4. **Domain Types**: Structs that represent business concepts (`Session`, `SessionDetail`, `SearchResult`, `Stats`)
5. **Pure Business Operations**: Logic that would be identical whether you have CLI, TUI, MCP, or web API

**Test: "Would this be different for CLI vs TUI vs MCP?"**

- **NO** → Core
- **YES** → Interface

Examples:

- ✅ CORE: "Get all sessions, excluding those with no meaningful content"
- ✅ CORE: "Search messages using FTS5"
- ✅ CORE: "Calculate last working directory from message history"
- ❌ NOT CORE: "Show 20 sessions" (that's pagination - interface concern)
- ❌ NOT CORE: "Format timestamp as 'Jan 2, 2006'" (that's presentation - interface concern)

### INTERFACE Layer (`internal/interface/`, `cmd/ccrider/mcp/`)

Interfaces translate between protocols and core:

1. **Protocol Translation**: HTTP, CLI flags, MCP JSON, TUI key bindings
2. **Input Normalization**: Parse `project:foo after:yesterday` into filter structs
3. **Output Formatting**: Convert `time.Time` to "Jan 2" or RFC3339 or "2 hours ago"
4. **Pagination/Limits**: Decide to show 20 vs 50 vs 10 results
5. **Presentation**: Colors, tables, progress bars, JSON structure
6. **Grouping**: "Show 3 matches per session" (core returns ALL matches)

Examples:

- ✅ INTERFACE: Strip quotes from user's search query
- ✅ INTERFACE: Apply limit of 50 sessions to display
- ✅ INTERFACE: Group search results by session for display
- ✅ INTERFACE: Format date as "2 days ago" for terminal
- ✅ INTERFACE: Parse `after:2024-01-01` from search string

## Rules

### ❌ NEVER in Interface Layers:

1. **No SQL queries** - All database access through core functions
2. **No business logic** - Don't decide what's "valid" or "meaningful"
3. **No filtering by business rules** - Core decides if warmup sessions are excluded
4. **No direct database imports** - Use core types and functions

### ✅ DO in Interface Layers:

1. **Call core functions** - `db.ListSessions()`, `search.Search()`
2. **Transform for display** - Format dates, truncate text, add colors
3. **Apply presentation limits** - Pagination, "show 3 matches per session"
4. **Parse user input** - Convert CLI flags/TUI keys/MCP params into core function calls
5. **Handle protocol concerns** - JSON marshaling, terminal rendering, progress bars

## Common Violations to Avoid

### 🚨 Filtering in Interface

```go
// ❌ WRONG - business logic in interface
for _, session := range sessions {
    if session.Summary != "" && !strings.Contains(session.Summary, "warmup") {
        // Only show non-warmup sessions
    }
}
```

This logic must be in core - it's a business rule that applies to ALL interfaces.

### 🚨 Database Queries in Interface

```go
// ❌ WRONG - SQL in interface layer
rows, err := database.Query("SELECT * FROM sessions WHERE project_path LIKE ?", filter)
```

Move to core. Interface should call `db.ListSessions(projectFilter)`.

### 🚨 Date Filtering in Interface

```go
// ❌ WRONG - business logic about date ranges
if args.AfterDate != "" && coreResult.Timestamp < args.AfterDate {
    continue
}
```

If date filtering is a business requirement (applies to all interfaces), move to core.
If it's MCP-specific (other interfaces don't need it), it can stay but only if clearly protocol-specific.

### ✅ Correct Separation

```go
// ✅ CORE - business logic
func (db *DB) ListSessions(projectPath string) ([]Session, error) {
    // SQL query with business rules:
    // - Exclude sessions with no meaningful content
    // - Fallback summary to first user message
    // - Order by updated_at DESC
}

// ✅ INTERFACE - presentation
func runList(cmd *cobra.Command, args []string) error {
    sessions, err := database.ListSessions(projectFilter) // Call core
    if len(sessions) > listLimit {
        sessions = sessions[:listLimit] // Apply display limit
    }
    for _, s := range sessions {
        fmt.Printf("[%d] %s\n", i, s.SessionID)
        fmt.Printf("    Updated: %s\n", formatTimestamp(s.UpdatedAt)) // Format for display
    }
}
```

## When Working on Code

Before adding logic to interface layer, ask:

1. **"Would CLI and TUI and MCP all need this same logic?"**

   - YES → Move to core
   - NO → Keep in interface

2. **"Is this about WHAT data to get or HOW to display it?"**

   - WHAT → Core
   - HOW → Interface

3. **"Does this involve a database query or business rule?"**
   - YES → Core
   - NO → Probably interface

## File Organization

```
internal/core/
├── db/           # Database operations, domain types
│   ├── db.go     # Connection, base types
│   ├── sessions.go  # Session queries: ListSessions, GetSessionDetail, GetSessionLaunchInfo
│   └── stats.go     # Stats queries: GetStats
└── search/       # Search operations
    └── search.go # FTS5 search: Search, SearchCode

internal/interface/
├── cli/          # Command-line interface
│   ├── list.go   # Calls db.ListSessions(), formats as table
│   ├── stats.go  # Calls db.GetStats(), formats as table
│   └── debug_prompt.go  # Calls db.GetSessionLaunchInfo(), formats prompt
└── tui/          # Terminal UI
    └── messages.go  # Calls search.Search(), formats as colored list

cmd/ccrider/mcp/
└── server.go     # MCP server
    # Calls search.Search(), db.GetSessionDetail(), db.ListSessions()
    # Formats as JSON for MCP protocol
```

## Summary

**Core = What and Why**

- What data to retrieve
- Why certain sessions are excluded
- What constitutes "valid" data

**Interface = How and When**

- How to display data (colors, format, structure)
- When to truncate (pagination limits)
- How user specifies what they want (CLI flags, TUI keys, MCP params)
