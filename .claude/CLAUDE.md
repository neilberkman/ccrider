# CCRider Project Instructions

## Architecture Philosophy

**READ THIS FIRST:** This codebase follows strict Core/Interface separation.

See `AGENTS.md` in the project root for complete architectural philosophy and rules.

## Key Rules

1. **NO SQL queries in interface layers** (`internal/interface/`, `cmd/ccrider/mcp/`)

   - All database access goes through `internal/core/db/` functions

2. **NO business logic in interface layers**

   - Filtering, validation, data rules → Core
   - Formatting, display, pagination → Interface

3. **Before adding ANY logic to an interface layer:**
   - Ask: "Would CLI, TUI, and MCP all need this same logic?"
   - YES → Move to core
   - NO → Keep in interface

## Common Violations to Check For

When reviewing code, look for these in interface layers:

- Direct SQL queries
- Business rule filtering (e.g., excluding warmup sessions)
- Date range filtering (if it's a business requirement)
- Data validation
- Summary fallback logic
- Deciding what's "meaningful" or "valid"

## File Organization

```
internal/core/db/     # All database operations
internal/core/search/ # All search operations
internal/interface/   # CLI, TUI - call core, format output
cmd/ccrider/mcp/      # MCP server - calls core, formats JSON
```

Refer to `AGENTS.md` for detailed examples and principles.
