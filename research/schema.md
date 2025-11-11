# Claude Code Session Format Schema

## Overview

Claude Code stores conversation data in JSONL (JSON Lines) format across several locations:

- **`~/.claude/history.jsonl`** - Global command history across all projects
- **`~/.claude/projects/[project-path]/[sessionId].jsonl`** - Individual session transcripts
- **`~/.claude/projects/[project-path]/agent-[agentId].jsonl`** - Subagent session transcripts

Each line in a `.jsonl` file is a complete JSON object representing a single event or message.

## Session File Naming

- Main sessions: `[uuid].jsonl` where uuid matches the `sessionId` field
- Agent sessions: `agent-[shortId].jsonl` where shortId is the `agentId` field
- Project path is normalized (e.g., `/Users/neil/xuku/invoice` → `-Users-neil-xuku-invoice`)

## Entry Types

### 1. Summary Entry (First Line)

Always the first line of a session file.

```json
{
  "type": "summary",
  "summary": "Human-readable session title",
  "leafUuid": "uuid-of-final-message"
}
```

**Fields:**

- `type`: Always `"summary"`
- `summary`: Generated title describing the session
- `leafUuid`: UUID of the last message in the conversation (or null)

### 2. User Message Entry

Represents a message from the user.

```json
{
  "type": "user",
  "parentUuid": "parent-message-uuid-or-null",
  "isSidechain": false,
  "userType": "external",
  "cwd": "/Users/neil/path/to/project",
  "sessionId": "session-uuid",
  "version": "2.0.29",
  "gitBranch": "main",
  "uuid": "message-uuid",
  "timestamp": "2025-11-01T01:23:44.615Z",
  "message": {
    "role": "user",
    "content": "The actual user message text"
  },
  "thinkingMetadata": {
    "level": "high",
    "disabled": false,
    "triggers": []
  },
  "toolUseResult": {
    "stdout": "command output",
    "stderr": "",
    "interrupted": false,
    "isImage": false
  }
}
```

**Core Fields:**

- `type`: `"user"`
- `uuid`: Unique identifier for this message
- `parentUuid`: UUID of the previous message (null if first user message)
- `timestamp`: ISO 8601 timestamp
- `sessionId`: Session identifier (matches filename without .jsonl)
- `cwd`: Current working directory when message was sent
- `gitBranch`: Git branch name (empty string if not in repo)
- `version`: Claude Code version
- `userType`: Usually `"external"`
- `isSidechain`: Boolean indicating if this is a branched conversation

**Message Content:**

- `message.role`: `"user"`
- `message.content`: String containing the user's message

**Optional Fields:**

- `thinkingMetadata`: Configuration for Claude's thinking process
  - `level`: `"high"`, `"medium"`, `"low"`
  - `disabled`: Boolean
  - `triggers`: Array (purpose unclear)
- `toolUseResult`: Present when the user message contains tool results
  - Can be a string (error message) or object with:
    - `stdout`: Command stdout output
    - `stderr`: Command stderr output
    - `interrupted`: Boolean
    - `isImage`: Boolean
    - For WebFetch: `bytes`, `code`, `codeText`, `result`, `durationMs`, `url`
    - For TodoWrite: `oldTodos`, `newTodos` arrays

### 3. Assistant Message Entry

Represents Claude's response.

```json
{
  "type": "assistant",
  "parentUuid": "parent-message-uuid",
  "isSidechain": false,
  "userType": "external",
  "cwd": "/Users/neil/path/to/project",
  "sessionId": "session-uuid",
  "version": "2.0.29",
  "gitBranch": "main",
  "uuid": "message-uuid",
  "timestamp": "2025-11-01T01:25:48.367Z",
  "requestId": "req_01...",
  "message": {
    "model": "claude-sonnet-4-5-20250929",
    "id": "msg_01...",
    "type": "message",
    "role": "assistant",
    "content": [
      {
        "type": "text",
        "text": "Response text here"
      },
      {
        "type": "tool_use",
        "id": "toolu_01...",
        "name": "Bash",
        "input": {
          "command": "ls -la",
          "description": "List files"
        }
      }
    ],
    "stop_reason": "end_turn",
    "stop_sequence": null,
    "usage": {
      "input_tokens": 617,
      "cache_creation_input_tokens": 0,
      "cache_read_input_tokens": 0,
      "cache_creation": {
        "ephemeral_5m_input_tokens": 0,
        "ephemeral_1h_input_tokens": 0
      },
      "output_tokens": 118,
      "service_tier": "standard"
    }
  }
}
```

**Core Fields:**

- `type`: `"assistant"`
- `uuid`: Unique identifier for this message
- `parentUuid`: UUID of the previous message
- `requestId`: API request identifier
- `timestamp`: ISO 8601 timestamp
- All the same context fields as user messages (sessionId, cwd, etc.)

**Message Content:**

- `message.model`: Model identifier (e.g., `"claude-sonnet-4-5-20250929"`)
- `message.id`: API message ID
- `message.role`: `"assistant"`
- `message.type`: `"message"`
- `message.content`: Array of content blocks
- `message.stop_reason`: `"end_turn"`, `"max_tokens"`, etc. (null if streaming)
- `message.stop_sequence`: Stop sequence that triggered end (if any)
- `message.usage`: Token usage statistics

**Content Block Types:**

1. **Text Block:**

```json
{
  "type": "text",
  "text": "The actual response text"
}
```

2. **Tool Use Block:**

```json
{
  "type": "tool_use",
  "id": "toolu_01PDzPWrXnSHeB8QvqqUAghV",
  "name": "Bash",
  "input": {
    "command": "ls",
    "description": "List files"
  }
}
```

### 4. System Message Entry

System-generated messages (commands, status updates).

```json
{
  "type": "system",
  "subtype": "local_command",
  "parentUuid": null,
  "isSidechain": false,
  "userType": "external",
  "cwd": "/Users/neil",
  "sessionId": "session-uuid",
  "version": "2.0.29",
  "gitBranch": "",
  "timestamp": "2025-11-01T01:23:44.615Z",
  "uuid": "message-uuid",
  "isMeta": false,
  "content": "<command-name>/resume</command-name>\n<command-message>resume</command-message>\n<command-args></command-args>",
  "level": "info"
}
```

**Fields:**

- `type`: `"system"`
- `subtype`: Type of system message (e.g., `"local_command"`)
- `content`: Message content (often XML-formatted)
- `level`: `"info"`, `"warning"`, `"error"`
- `isMeta`: Boolean

### 5. File History Snapshot Entry

Tracks file backup snapshots.

```json
{
  "type": "file-history-snapshot",
  "messageId": "associated-message-uuid",
  "isSnapshotUpdate": false,
  "snapshot": {
    "messageId": "first-snapshot-message-uuid",
    "timestamp": "2025-10-20T16:50:11.755Z",
    "trackedFileBackups": {
      ".bash_profile": {
        "backupFileName": "d3729f6a94c1f530@v1",
        "version": 1,
        "backupTime": "2025-10-20T16:54:10.996Z"
      }
    }
  }
}
```

**Fields:**

- `type`: `"file-history-snapshot"`
- `messageId`: UUID of associated message
- `isSnapshotUpdate`: Boolean indicating if this updates a previous snapshot
- `snapshot.trackedFileBackups`: Object mapping file paths to backup info
  - `backupFileName`: Name of backup file in file-history directory
  - `version`: Version number
  - `backupTime`: When backup was created

## History File Format

`~/.claude/history.jsonl` contains simple command history entries:

```json
{
  "display": "the command text the user typed",
  "pastedContents": {},
  "timestamp": 1759022024295,
  "project": "/Users/neil/personal/mommail"
}
```

**Fields:**

- `display`: The text displayed in history (user's input)
- `timestamp`: Unix timestamp in milliseconds
- `project`: Absolute path to the project directory
- `pastedContents`: Object (usually empty)

## Session Continuation

When a session is resumed with `/resume`:

- All messages append to the **same** `.jsonl` file
- The `sessionId` remains unchanged
- The conversation thread continues via `parentUuid` linkage

## Agent Sessions

Agent/subagent sessions are stored separately:

- Filename: `agent-[shortId].jsonl`
- Same message format as main sessions
- `agentId` field contains the short ID
- `isSidechain` is typically `true`
- May reference parent session via `parentUuid`

## Working Directory Tracking

The `cwd` field can change within a single session as the user navigates directories. This allows reconstructing the exact context for each command.

## Message Threading

Messages form a tree structure via `parentUuid`:

- Main conversation thread
- Branching conversations (when `isSidechain: true`)
- Agent subthreads

To reconstruct conversation order:

1. Start with `parentUuid: null`
2. Follow `uuid` → `parentUuid` links
3. The `leafUuid` in the summary points to the final message

## Version Information

- Format version is not explicitly stored
- Claude Code version is in the `version` field
- Schema may evolve with new Claude Code versions
- Example versions seen: `"2.0.22"`, `"2.0.29"`
