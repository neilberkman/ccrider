# Phase 1: Core & CLI Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Build the core JSONL parser, SQLite backend, and basic CLI for importing and searching Claude Code sessions.

**Architecture:** Core/interface separation with reusable libraries. Core handles all business logic (parsing, DB, search) with zero UI dependencies. CLI is a thin wrapper that normalizes input and formats output.

**Tech Stack:** Go 1.21+, SQLite with FTS5 (modernc.org/sqlite - pure Go), Cobra for CLI

**Success Criteria:**

- Import 100+ sessions in < 5 seconds
- Zero data loss from JSONL
- Search returns results in < 100ms
- Single binary, no dependencies

---

## Task 1: Project Setup

**Files:**

- Create: `go.mod`
- Create: `cmd/ccrider/main.go`
- Create: `.golangci.yml`
- Create: `Makefile`

**Step 1: Initialize Go module**

```bash
cd /Users/neil/xuku/ccrider/.worktrees/phase1-core
go mod init github.com/yourusername/ccrider
```

**Step 2: Create main.go**

Create `cmd/ccrider/main.go`:

```go
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Println("ccrider v0.1.0")
	os.Exit(0)
}
```

**Step 3: Create golangci-lint config**

Create `.golangci.yml`:

```yaml
linters:
  enable:
    - gofmt
    - govet
    - staticcheck
    - errcheck
    - gosimple
    - ineffassign
    - unused

issues:
  exclude-use-default: false
  max-issues-per-linter: 0
  max-same-issues: 0

run:
  timeout: 5m
```

**Step 4: Create Makefile**

Create `Makefile`:

```makefile
.PHONY: build test lint clean

build:
	go build -o ccrider cmd/ccrider/main.go

test:
	go test ./... -v -race -coverprofile=coverage.out

lint:
	golangci-lint run ./...

clean:
	rm -f ccrider coverage.out

install:
	go install ./cmd/ccrider
```

**Step 5: Test build**

```bash
make build
./ccrider
```

Expected output: `ccrider v0.1.0`

**Step 6: Commit**

```bash
git add go.mod cmd/ccrider/main.go .golangci.yml Makefile
git commit -m "feat: initial project setup with Go module and build config"
```

---

## Task 2: Core Models (Strongly-Typed Structs)

**Files:**

- Create: `internal/core/models/session.go`
- Create: `internal/core/models/message.go`
- Create: `internal/core/models/session_test.go`

**Step 1: Write test for session model**

Create `internal/core/models/session_test.go`:

```go
package models

import (
	"testing"
	"time"
)

func TestSessionValidation(t *testing.T) {
	tests := []struct {
		name    string
		session Session
		wantErr bool
	}{
		{
			name: "valid session",
			session: Session{
				SessionID:   "abc-123",
				ProjectPath: "/Users/neil/xuku/invoice",
				Summary:     "Test session",
				CreatedAt:   time.Now(),
				UpdatedAt:   time.Now(),
			},
			wantErr: false,
		},
		{
			name: "missing session ID",
			session: Session{
				ProjectPath: "/Users/neil/xuku/invoice",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.session.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./internal/core/models -v
```

Expected: FAIL (package not found or Validate method missing)

**Step 3: Implement session model**

Create `internal/core/models/session.go`:

```go
package models

import (
	"errors"
	"time"
)

// Session represents a Claude Code conversation session
type Session struct {
	ID            int64
	SessionID     string    // UUID from filename
	ProjectPath   string    // Normalized project path
	Summary       string    // From summary JSONL entry
	LeafUUID      string    // Last message UUID
	CWD           string    // Working directory
	GitBranch     string    // Git branch
	CreatedAt     time.Time
	UpdatedAt     time.Time
	MessageCount  int
	Version       string    // Claude Code version
	ImportedAt    time.Time
	LastSyncedAt  time.Time
	FileHash      string    // SHA256 for change detection
	FileSize      int64
	FileMtime     time.Time
}

// Validate checks if the session has required fields
func (s *Session) Validate() error {
	if s.SessionID == "" {
		return errors.New("session_id is required")
	}
	if s.ProjectPath == "" {
		return errors.New("project_path is required")
	}
	return nil
}
```

**Step 4: Run test to verify it passes**

```bash
go test ./internal/core/models -v
```

Expected: PASS

**Step 5: Implement message models**

Create `internal/core/models/message.go`:

```go
package models

import (
	"encoding/json"
	"time"
)

// MessageType represents the type of JSONL entry
type MessageType string

const (
	MessageTypeSummary            MessageType = "summary"
	MessageTypeUser               MessageType = "user"
	MessageTypeAssistant          MessageType = "assistant"
	MessageTypeSystem             MessageType = "system"
	MessageTypeFileHistorySnapshot MessageType = "file-history-snapshot"
)

// Message represents a single entry in a session JSONL file
type Message struct {
	ID           int64
	UUID         string
	SessionID    int64
	ParentUUID   string
	Type         MessageType
	Sender       string    // "human" or "assistant" for user/assistant types
	Content      json.RawMessage // Full message content as JSON
	TextContent  string    // Extracted text for FTS
	Timestamp    time.Time
	Sequence     int
	IsSidechain  bool
	CWD          string
	GitBranch    string
	Version      string
}

// UserMessage represents a parsed user message
type UserMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// AssistantMessage represents a parsed assistant message
type AssistantMessage struct {
	Model       string          `json:"model"`
	ID          string          `json:"id"`
	Role        string          `json:"role"`
	Content     json.RawMessage `json:"content"` // Array of content blocks
	StopReason  string          `json:"stop_reason"`
	Usage       TokenUsage      `json:"usage"`
}

// TokenUsage tracks API token usage
type TokenUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// ContentBlock represents a content block in assistant messages
type ContentBlock struct {
	Type string          `json:"type"` // "text" or "tool_use"
	Text string          `json:"text,omitempty"`
	ID   string          `json:"id,omitempty"`
	Name string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

// SummaryEntry is the first line of every session file
type SummaryEntry struct {
	Type     string `json:"type"`
	Summary  string `json:"summary"`
	LeafUUID string `json:"leafUuid"`
}
```

**Step 6: Commit**

```bash
git add internal/core/models/
git commit -m "feat: add core domain models with validation"
```

---

## Task 3: JSONL Parser (Reusable Package)

**Files:**

- Create: `pkg/ccsessions/parser.go`
- Create: `pkg/ccsessions/parser_test.go`
- Create: `pkg/ccsessions/testdata/sample.jsonl`

**Step 1: Create test data**

Create `pkg/ccsessions/testdata/sample.jsonl`:

```json
{"type":"summary","summary":"Test session","leafUuid":"leaf-123"}
{"parentUuid":null,"type":"user","message":{"role":"user","content":"test message"},"uuid":"msg-1","timestamp":"2025-11-08T12:00:00Z","sessionId":"session-123","cwd":"/test","gitBranch":"main","version":"2.0.35","isSidechain":false,"userType":"external"}
{"parentUuid":"msg-1","type":"assistant","message":{"model":"claude-sonnet-4-5-20250929","id":"msg-2","role":"assistant","content":[{"type":"text","text":"response"}],"usage":{"input_tokens":100,"output_tokens":50}},"uuid":"msg-2","timestamp":"2025-11-08T12:00:05Z","sessionId":"session-123","cwd":"/test","gitBranch":"main","version":"2.0.35","isSidechain":false,"userType":"external","requestId":"req-123"}
```

**Step 2: Write parser test**

Create `pkg/ccsessions/parser_test.go`:

```go
package ccsessions

import (
	"testing"
)

func TestParseFile(t *testing.T) {
	session, err := ParseFile("testdata/sample.jsonl")
	if err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}

	// Check summary
	if session.Summary != "Test session" {
		t.Errorf("Summary = %v, want 'Test session'", session.Summary)
	}

	// Check message count
	if len(session.Messages) != 2 {
		t.Errorf("Message count = %v, want 2", len(session.Messages))
	}

	// Check first message
	if session.Messages[0].Type != "user" {
		t.Errorf("First message type = %v, want 'user'", session.Messages[0].Type)
	}
}

func TestParseFile_InvalidPath(t *testing.T) {
	_, err := ParseFile("nonexistent.jsonl")
	if err == nil {
		t.Error("ParseFile() should return error for invalid path")
	}
}
```

**Step 3: Run test to verify failure**

```bash
go test ./pkg/ccsessions -v
```

Expected: FAIL (ParseFile not defined)

**Step 4: Implement parser**

Create `pkg/ccsessions/parser.go`:

```go
package ccsessions

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ParsedSession represents a fully parsed session file
type ParsedSession struct {
	SessionID   string
	Summary     string
	LeafUUID    string
	Messages    []ParsedMessage
	FilePath    string
	FileSize    int64
	FileMtime   time.Time
}

// ParsedMessage represents a parsed JSONL message entry
type ParsedMessage struct {
	UUID        string
	ParentUUID  string
	Type        string
	Sender      string
	Content     json.RawMessage
	TextContent string
	Timestamp   time.Time
	Sequence    int
	IsSidechain bool
	CWD         string
	GitBranch   string
	Version     string
}

// rawEntry represents a raw JSONL line
type rawEntry struct {
	Type        string          `json:"type"`
	Summary     string          `json:"summary,omitempty"`
	LeafUUID    string          `json:"leafUuid,omitempty"`
	UUID        string          `json:"uuid,omitempty"`
	ParentUUID  string          `json:"parentUuid,omitempty"`
	SessionID   string          `json:"sessionId,omitempty"`
	Message     json.RawMessage `json:"message,omitempty"`
	Timestamp   string          `json:"timestamp,omitempty"`
	IsSidechain bool            `json:"isSidechain,omitempty"`
	CWD         string          `json:"cwd,omitempty"`
	GitBranch   string          `json:"gitBranch,omitempty"`
	Version     string          `json:"version,omitempty"`
}

// ParseFile parses a Claude Code session JSONL file
func ParseFile(path string) (*ParsedSession, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	// Get file info for metadata
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("failed to stat file: %w", err)
	}

	// Extract session ID from filename
	sessionID := filepath.Base(path)
	sessionID = sessionID[:len(sessionID)-len(filepath.Ext(sessionID))]

	session := &ParsedSession{
		SessionID: sessionID,
		FilePath:  path,
		FileSize:  info.Size(),
		FileMtime: info.ModTime(),
		Messages:  make([]ParsedMessage, 0),
	}

	scanner := bufio.NewScanner(file)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Bytes()

		var raw rawEntry
		if err := json.Unmarshal(line, &raw); err != nil {
			return nil, fmt.Errorf("line %d: failed to parse JSON: %w", lineNum, err)
		}

		// First line is always summary
		if lineNum == 1 {
			if raw.Type != "summary" {
				return nil, fmt.Errorf("first line must be summary, got %s", raw.Type)
			}
			session.Summary = raw.Summary
			session.LeafUUID = raw.LeafUUID
			continue
		}

		// Parse message entries
		msg, err := parseMessage(&raw, lineNum)
		if err != nil {
			// Log warning but don't fail - some message types we may not support yet
			fmt.Fprintf(os.Stderr, "Warning: line %d: %v\n", lineNum, err)
			continue
		}

		session.Messages = append(session.Messages, *msg)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading file: %w", err)
	}

	return session, nil
}

func parseMessage(raw *rawEntry, sequence int) (*ParsedMessage, error) {
	msg := &ParsedMessage{
		UUID:        raw.UUID,
		ParentUUID:  raw.ParentUUID,
		Type:        raw.Type,
		Sequence:    sequence,
		IsSidechain: raw.IsSidechain,
		CWD:         raw.CWD,
		GitBranch:   raw.GitBranch,
		Version:     raw.Version,
		Content:     raw.Message,
	}

	// Parse timestamp
	if raw.Timestamp != "" {
		t, err := time.Parse(time.RFC3339, raw.Timestamp)
		if err != nil {
			return nil, fmt.Errorf("invalid timestamp: %w", err)
		}
		msg.Timestamp = t
	}

	// Extract text content and sender based on type
	switch raw.Type {
	case "user":
		var userMsg struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal(raw.Message, &userMsg); err == nil {
			msg.TextContent = userMsg.Content
			msg.Sender = "human"
		}

	case "assistant":
		var assistantMsg struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text,omitempty"`
			} `json:"content"`
		}
		if err := json.Unmarshal(raw.Message, &assistantMsg); err == nil {
			for _, block := range assistantMsg.Content {
				if block.Type == "text" {
					msg.TextContent += block.Text + "\n"
				}
			}
			msg.Sender = "assistant"
		}

	case "system", "file-history-snapshot":
		// These types don't have extractable text content
		msg.TextContent = ""

	default:
		return nil, fmt.Errorf("unknown message type: %s", raw.Type)
	}

	return msg, nil
}
```

**Step 5: Add dependencies**

```bash
# No external deps needed - pure Go!
go mod tidy
```

**Step 6: Run test to verify it passes**

```bash
go test ./pkg/ccsessions -v
```

Expected: PASS

**Step 7: Commit**

```bash
git add pkg/ccsessions/
git commit -m "feat: add JSONL parser for Claude Code sessions

Parses all 5 message types: summary, user, assistant, system, file-history-snapshot
Extracts text content for FTS indexing
Reusable package with zero dependencies"
```

---

## Task 4: Database Schema

**Files:**

- Create: `internal/core/db/schema.go`
- Create: `internal/core/db/db.go`
- Create: `internal/core/db/db_test.go`

**Step 1: Write database test**

Create `internal/core/db/db_test.go`:

```go
package db

import (
	"os"
	"testing"
)

func TestNew(t *testing.T) {
	// Use temp file for test DB
	tmpfile, err := os.CreateTemp("", "test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())
	tmpfile.Close()

	database, err := New(tmpfile.Name())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer database.Close()

	// Verify schema initialized
	var count int
	err = database.conn.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table'").Scan(&count)
	if err != nil {
		t.Fatalf("Failed to query schema: %v", err)
	}

	if count < 4 { // sessions, messages, tool_uses, import_log minimum
		t.Errorf("Expected at least 4 tables, got %d", count)
	}
}
```

**Step 2: Run test to verify failure**

```bash
go test ./internal/core/db -v
```

Expected: FAIL (package not found)

**Step 3: Add SQLite dependency**

```bash
go get modernc.org/sqlite
```

**Step 4: Implement database wrapper**

Create `internal/core/db/db.go`:

```go
package db

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// DB wraps a SQLite database connection
type DB struct {
	conn *sql.DB
}

// New creates a new database connection and initializes schema
func New(dbPath string) (*DB, error) {
	// Open with WAL mode for concurrent reads
	dsn := dbPath + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Set connection pool settings
	conn.SetMaxOpenConns(1) // SQLite only supports one writer
	conn.SetMaxIdleConns(1)
	conn.SetConnMaxLifetime(time.Hour)

	db := &DB{conn: conn}

	// Initialize schema
	if err := db.initSchema(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to initialize schema: %w", err)
	}

	return db, nil
}

// Close closes the database connection
func (db *DB) Close() error {
	return db.conn.Close()
}

// Begin starts a new transaction
func (db *DB) Begin() (*sql.Tx, error) {
	return db.conn.Begin()
}

// Exec executes a query
func (db *DB) Exec(query string, args ...interface{}) (sql.Result, error) {
	return db.conn.Exec(query, args...)
}

// Query executes a query that returns rows
func (db *DB) Query(query string, args ...interface{}) (*sql.Rows, error) {
	return db.conn.Query(query, args...)
}

// QueryRow executes a query that returns a single row
func (db *DB) QueryRow(query string, args ...interface{}) *sql.Row {
	return db.conn.QueryRow(query, args...)
}
```

**Step 5: Implement schema**

Create `internal/core/db/schema.go`:

```go
package db

func (db *DB) initSchema() error {
	schema := `
	-- Sessions table
	CREATE TABLE IF NOT EXISTS sessions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id TEXT UNIQUE NOT NULL,
		project_path TEXT NOT NULL,
		summary TEXT,
		leaf_uuid TEXT,
		cwd TEXT,
		git_branch TEXT,
		created_at DATETIME,
		updated_at DATETIME,
		message_count INTEGER DEFAULT 0,
		version TEXT,
		imported_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		last_synced_at DATETIME,
		file_hash TEXT,
		file_size INTEGER,
		file_mtime DATETIME
	);

	CREATE INDEX IF NOT EXISTS idx_sessions_session_id ON sessions(session_id);
	CREATE INDEX IF NOT EXISTS idx_sessions_project_path ON sessions(project_path);
	CREATE INDEX IF NOT EXISTS idx_sessions_updated_at ON sessions(updated_at);
	CREATE INDEX IF NOT EXISTS idx_sessions_git_branch ON sessions(git_branch);

	-- Messages table
	CREATE TABLE IF NOT EXISTS messages (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		uuid TEXT UNIQUE NOT NULL,
		session_id INTEGER NOT NULL,
		parent_uuid TEXT,
		type TEXT NOT NULL,
		sender TEXT,
		content TEXT,
		text_content TEXT,
		timestamp DATETIME,
		sequence INTEGER,
		is_sidechain BOOLEAN,
		cwd TEXT,
		git_branch TEXT,
		version TEXT,
		FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
	);

	CREATE INDEX IF NOT EXISTS idx_messages_uuid ON messages(uuid);
	CREATE INDEX IF NOT EXISTS idx_messages_session_id ON messages(session_id);
	CREATE INDEX IF NOT EXISTS idx_messages_parent_uuid ON messages(parent_uuid);
	CREATE INDEX IF NOT EXISTS idx_messages_timestamp ON messages(timestamp);

	-- Tool uses table
	CREATE TABLE IF NOT EXISTS tool_uses (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		message_id INTEGER NOT NULL,
		tool_name TEXT NOT NULL,
		tool_id TEXT,
		input TEXT,
		output TEXT,
		created_at DATETIME,
		FOREIGN KEY (message_id) REFERENCES messages(id) ON DELETE CASCADE
	);

	CREATE INDEX IF NOT EXISTS idx_tool_uses_message_id ON tool_uses(message_id);
	CREATE INDEX IF NOT EXISTS idx_tool_uses_tool_name ON tool_uses(tool_name);

	-- Import log table
	CREATE TABLE IF NOT EXISTS import_log (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		file_path TEXT NOT NULL,
		file_hash TEXT NOT NULL,
		imported_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		sessions_imported INTEGER,
		messages_imported INTEGER,
		status TEXT CHECK(status IN ('success', 'partial', 'failed')),
		error_message TEXT
	);

	CREATE INDEX IF NOT EXISTS idx_import_log_file_hash ON import_log(file_hash);

	-- FTS5 tables for full-text search
	CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
		text_content,
		content=messages,
		content_rowid=id,
		tokenize='porter unicode61'
	);

	CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts_code USING fts5(
		text_content,
		content=messages,
		content_rowid=id,
		tokenize='unicode61'
	);

	-- Triggers to keep FTS in sync
	CREATE TRIGGER IF NOT EXISTS messages_ai AFTER INSERT ON messages BEGIN
		INSERT INTO messages_fts(rowid, text_content) VALUES (new.id, new.text_content);
		INSERT INTO messages_fts_code(rowid, text_content) VALUES (new.id, new.text_content);
	END;

	CREATE TRIGGER IF NOT EXISTS messages_ad AFTER DELETE ON messages BEGIN
		DELETE FROM messages_fts WHERE rowid = old.id;
		DELETE FROM messages_fts_code WHERE rowid = old.id;
	END;

	CREATE TRIGGER IF NOT EXISTS messages_au AFTER UPDATE ON messages BEGIN
		UPDATE messages_fts SET text_content = new.text_content WHERE rowid = new.id;
		UPDATE messages_fts_code SET text_content = new.text_content WHERE rowid = new.id;
	END;
	`

	_, err := db.conn.Exec(schema)
	return err
}
```

**Step 6: Run test to verify it passes**

```bash
go test ./internal/core/db -v
```

Expected: PASS

**Step 7: Commit**

```bash
git add internal/core/db/
git commit -m "feat: add SQLite schema with FTS5 search

Tables: sessions, messages, tool_uses, import_log
FTS5 with dual tokenizers (porter + unicode61)
WAL mode for concurrent reads"
```

---

## Task 5: Importer with Progress Feedback

**Files:**

- Create: `internal/core/importer/importer.go`
- Create: `internal/core/importer/progress.go`
- Create: `internal/core/importer/importer_test.go`

**Step 1: Write progress display helpers**

Create `internal/core/importer/progress.go`:

```go
package importer

import (
	"fmt"
	"io"
	"strings"
	"time"
)

// ProgressReporter handles progress feedback during import
type ProgressReporter struct {
	writer    io.Writer
	total     int
	current   int
	startTime time.Time
	lastMsg   string
}

// NewProgressReporter creates a new progress reporter
func NewProgressReporter(w io.Writer, total int) *ProgressReporter {
	return &ProgressReporter{
		writer:    w,
		total:     total,
		current:   0,
		startTime: time.Now(),
	}
}

// Update updates the progress bar with current session info
func (p *ProgressReporter) Update(sessionSummary string, firstMsg string) {
	p.current++

	// Calculate progress percentage
	pct := float64(p.current) / float64(p.total) * 100

	// Draw progress bar (50 chars wide)
	barWidth := 50
	filled := int(float64(barWidth) * float64(p.current) / float64(p.total))
	bar := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)

	// Truncate display text to fit terminal
	displayText := sessionSummary
	if len(displayText) > 60 {
		displayText = displayText[:57] + "..."
	}

	// Calculate ETA
	elapsed := time.Since(p.startTime)
	rate := float64(p.current) / elapsed.Seconds()
	remaining := float64(p.total-p.current) / rate
	eta := time.Duration(remaining) * time.Second

	// Print progress
	fmt.Fprintf(p.writer, "\r[%s] %3.0f%% (%d/%d) ETA: %s | %s",
		bar, pct, p.current, p.total, eta.Round(time.Second), displayText)

	p.lastMsg = displayText
}

// Finish completes the progress display
func (p *ProgressReporter) Finish() {
	elapsed := time.Since(p.startTime)
	fmt.Fprintf(p.writer, "\n✓ Imported %d sessions in %s\n", p.total, elapsed.Round(time.Millisecond))
}
```

**Step 2: Write importer test**

Create `internal/core/importer/importer_test.go`:

```go
package importer

import (
	"os"
	"testing"

	"github.com/yourusername/ccrider/internal/core/db"
	"github.com/yourusername/ccrider/pkg/ccsessions"
)

func TestImportSession(t *testing.T) {
	// Setup test database
	tmpfile, err := os.CreateTemp("", "test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())
	tmpfile.Close()

	database, err := db.New(tmpfile.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	imp := New(database)

	// Parse test session
	session, err := ccsessions.ParseFile("../../pkg/ccsessions/testdata/sample.jsonl")
	if err != nil {
		t.Fatal(err)
	}

	// Import it
	err = imp.ImportSession(session)
	if err != nil {
		t.Fatalf("ImportSession() error = %v", err)
	}

	// Verify it was imported
	var count int
	err = database.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&count)
	if err != nil {
		t.Fatal(err)
	}

	if count != 1 {
		t.Errorf("Expected 1 session, got %d", count)
	}

	// Verify messages imported
	err = database.QueryRow("SELECT COUNT(*) FROM messages").Scan(&count)
	if err != nil {
		t.Fatal(err)
	}

	if count != 2 { // 2 messages in sample.jsonl
		t.Errorf("Expected 2 messages, got %d", count)
	}
}
```

**Step 3: Run test to verify failure**

```bash
go test ./internal/core/importer -v
```

Expected: FAIL (package not found)

**Step 4: Implement importer**

Create `internal/core/importer/importer.go`:

```go
package importer

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/yourusername/ccrider/internal/core/db"
	"github.com/yourusername/ccrider/pkg/ccsessions"
)

// Importer handles importing sessions into the database
type Importer struct {
	db *db.DB
}

// New creates a new importer
func New(database *db.DB) *Importer {
	return &Importer{db: database}
}

// ImportSession imports a single parsed session
func (i *Importer) ImportSession(session *ccsessions.ParsedSession) error {
	// Compute file hash
	hash, err := computeFileHash(session.FilePath)
	if err != nil {
		return fmt.Errorf("failed to hash file: %w", err)
	}

	// Check if already imported
	var exists bool
	err = i.db.QueryRow("SELECT EXISTS(SELECT 1 FROM import_log WHERE file_hash = ?)", hash).Scan(&exists)
	if err != nil {
		return fmt.Errorf("failed to check import log: %w", err)
	}

	if exists {
		// File already imported - could implement incremental update here
		return nil
	}

	// Begin transaction
	tx, err := i.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Extract project path from file path
	projectPath := extractProjectPath(session.FilePath)

	// Insert session
	result, err := tx.Exec(`
		INSERT INTO sessions (
			session_id, project_path, summary, leaf_uuid,
			created_at, updated_at, message_count, file_hash,
			file_size, file_mtime
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		session.SessionID,
		projectPath,
		session.Summary,
		session.LeafUUID,
		time.Now(), // Use first message timestamp if available
		time.Now(),
		len(session.Messages),
		hash,
		session.FileSize,
		session.FileMtime,
	)
	if err != nil {
		return fmt.Errorf("failed to insert session: %w", err)
	}

	sessionDBID, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("failed to get session ID: %w", err)
	}

	// Insert messages
	for _, msg := range session.Messages {
		_, err := tx.Exec(`
			INSERT INTO messages (
				uuid, session_id, parent_uuid, type, sender,
				content, text_content, timestamp, sequence,
				is_sidechain, cwd, git_branch, version
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`,
			msg.UUID,
			sessionDBID,
			msg.ParentUUID,
			msg.Type,
			msg.Sender,
			string(msg.Content),
			msg.TextContent,
			msg.Timestamp,
			msg.Sequence,
			msg.IsSidechain,
			msg.CWD,
			msg.GitBranch,
			msg.Version,
		)
		if err != nil {
			return fmt.Errorf("failed to insert message %s: %w", msg.UUID, err)
		}
	}

	// Record import
	_, err = tx.Exec(`
		INSERT INTO import_log (file_path, file_hash, sessions_imported, messages_imported, status)
		VALUES (?, ?, 1, ?, 'success')
	`, session.FilePath, hash, len(session.Messages))
	if err != nil {
		return fmt.Errorf("failed to record import: %w", err)
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit: %w", err)
	}

	return nil
}

// ImportDirectory imports all sessions from a directory tree
func (i *Importer) ImportDirectory(dirPath string, progress *ProgressReporter) error {
	// Find all .jsonl files
	var files []string
	err := filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && filepath.Ext(path) == ".jsonl" {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to walk directory: %w", err)
	}

	// Import each file
	for _, file := range files {
		session, err := ccsessions.ParseFile(file)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to parse %s: %v\n", file, err)
			continue
		}

		if err := i.ImportSession(session); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to import %s: %v\n", file, err)
			continue
		}

		// Update progress
		if progress != nil {
			firstMsg := ""
			if len(session.Messages) > 0 {
				firstMsg = session.Messages[0].TextContent
				if len(firstMsg) > 100 {
					firstMsg = firstMsg[:97] + "..."
				}
			}
			progress.Update(session.Summary, firstMsg)
		}
	}

	return nil
}

func computeFileHash(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

func extractProjectPath(filePath string) string {
	// Extract from ~/.claude/projects/-Users-neil-xuku-invoice/session.jsonl
	// Returns /Users/neil/xuku/invoice
	dir := filepath.Dir(filePath)
	base := filepath.Base(dir)

	// Decode the project path
	if len(base) > 0 && base[0] == '-' {
		// Remove leading dash and replace dashes with slashes
		decoded := base[1:]
		// This is a simplified decoder - may need refinement
		return "/" + decoded
	}

	return dir
}
```

**Step 5: Run test to verify it passes**

```bash
go test ./internal/core/importer -v
```

Expected: PASS

**Step 6: Commit**

```bash
git add internal/core/importer/
git commit -m "feat: add session importer with progress feedback

Imports parsed sessions into SQLite
Progress bar shows session summary + first message
SHA256 hash tracking to prevent duplicates"
```

---

## Task 6: CLI Foundation (Cobra Setup)

**Files:**

- Create: `internal/interface/cli/root.go`
- Create: `internal/interface/cli/sync.go`

**Step 1: Add Cobra dependency**

```bash
go get github.com/spf13/cobra
```

**Step 2: Create root command**

Create `internal/interface/cli/root.go`:

```go
package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var (
	dbPath string
)

// Execute runs the CLI
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:   "ccrider",
	Short: "Claude Code session manager",
	Long: `ccrider - search, browse, and resume your Claude Code sessions

A fast, reliable tool for managing Claude Code sessions with full-text search,
incremental sync, and native resume integration.`,
	Version: "0.1.0",
}

func init() {
	// Global flags
	home, err := os.UserHomeDir()
	if err != nil {
		home = "~"
	}
	defaultDB := filepath.Join(home, ".config", "ccrider", "sessions.db")

	rootCmd.PersistentFlags().StringVar(&dbPath, "db", defaultDB, "Database path")
}
```

**Step 3: Create sync command**

Create `internal/interface/cli/sync.go`:

```go
package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/yourusername/ccrider/internal/core/db"
	"github.com/yourusername/ccrider/internal/core/importer"
)

var syncCmd = &cobra.Command{
	Use:   "sync [path]",
	Short: "Import/sync Claude Code sessions",
	Long: `Import sessions from ~/.claude/projects/ or a specified directory.

Performs incremental sync - only imports new or changed sessions.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runSync,
}

func init() {
	rootCmd.AddCommand(syncCmd)
}

func runSync(cmd *cobra.Command, args []string) error {
	// Determine source path
	sourcePath := getDefaultClaudeDir()
	if len(args) > 0 {
		sourcePath = args[0]
	}

	fmt.Printf("Syncing sessions from: %s\n", sourcePath)
	fmt.Printf("Database: %s\n\n", dbPath)

	// Ensure database directory exists
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return fmt.Errorf("failed to create db directory: %w", err)
	}

	// Open database
	database, err := db.New(dbPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer database.Close()

	// Count total files for progress
	total, err := countJSONLFiles(sourcePath)
	if err != nil {
		return fmt.Errorf("failed to count files: %w", err)
	}

	if total == 0 {
		fmt.Println("No session files found")
		return nil
	}

	// Create importer with progress
	imp := importer.New(database)
	progress := importer.NewProgressReporter(os.Stdout, total)

	// Import
	if err := imp.ImportDirectory(sourcePath, progress); err != nil {
		return fmt.Errorf("import failed: %w", err)
	}

	progress.Finish()

	return nil
}

func getDefaultClaudeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "~/.claude/projects"
	}
	return filepath.Join(home, ".claude", "projects")
}

func countJSONLFiles(dirPath string) (int, error) {
	count := 0
	err := filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && filepath.Ext(path) == ".jsonl" {
			count++
		}
		return nil
	})
	return count, err
}
```

**Step 4: Update main.go to use CLI**

Update `cmd/ccrider/main.go`:

```go
package main

import (
	"github.com/yourusername/ccrider/internal/interface/cli"
)

func main() {
	cli.Execute()
}
```

**Step 5: Test build and run**

```bash
make build
./ccrider --help
```

Expected output: Shows help with sync command

**Step 6: Test sync command**

```bash
./ccrider sync --help
```

Expected output: Shows sync command help

**Step 7: Commit**

```bash
git add cmd/ccrider/main.go internal/interface/cli/
git commit -m "feat: add CLI with sync command

Cobra-based CLI with progress feedback
Syncs from ~/.claude/projects by default
Shows progress bar with session summaries"
```

---

## Task 7: Search Command

**Files:**

- Create: `internal/core/search/search.go`
- Create: `internal/core/search/search_test.go`
- Create: `internal/interface/cli/search.go`

(Implementation details follow same TDD pattern as above tasks...)

**Step 1-7:** Similar pattern to sync command:

- Write test for search query execution
- Implement FTS5 search in core
- Add search CLI command
- Test and commit

**Commit message:** `feat: add full-text search with FTS5`

---

## Task 8: List and Stats Commands

**Files:**

- Create: `internal/interface/cli/list.go`
- Create: `internal/interface/cli/stats.go`

(Follow same TDD pattern...)

**Commit messages:**

- `feat: add list command for browsing sessions`
- `feat: add stats command for database info`

---

## Final Integration Test

**Step 1: Run full sync on real data**

```bash
./ccrider sync
```

Expected: Progress bar shows sessions being imported

**Step 2: Search**

```bash
./ccrider search "authentication"
```

Expected: Returns matching sessions

**Step 3: List**

```bash
./ccrider list --limit 10
```

Expected: Shows 10 most recent sessions

**Step 4: Stats**

```bash
./ccrider stats
```

Expected: Shows database statistics

**Step 5: Run tests**

```bash
make test
```

Expected: All tests pass

**Step 6: Run linter**

```bash
make lint
```

Expected: No issues

**Step 7: Final commit**

```bash
git commit --allow-empty -m "Phase 1 complete: Core & CLI working

- JSONL parser with full schema support
- SQLite backend with FTS5 search
- Import with progress feedback
- CLI commands: sync, search, list, stats
- All tests passing
- Ready for Phase 2 (TUI)"
```

---

## Success Checklist

- [ ] Single binary builds successfully
- [ ] Can import real Claude Code sessions
- [ ] Progress bar shows during import
- [ ] Search returns results in < 100ms
- [ ] All core tests pass
- [ ] No linter errors
- [ ] Zero external runtime dependencies

**Next Phase:** TUI with Bubbletea + resume integration
