package opencodesessions

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseAll(t *testing.T) {
	dbPath := createOpenCodeDB(t)

	sessions, err := ParseAll(dbPath)
	if err != nil {
		t.Fatalf("ParseAll() error = %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("ParseAll() returned %d sessions, want 1", len(sessions))
	}

	session := sessions[0]
	if session.SessionID != "ses_root" {
		t.Errorf("SessionID = %q, want ses_root", session.SessionID)
	}
	if session.Summary != "Fix auth flow" {
		t.Errorf("Summary = %q, want Fix auth flow", session.Summary)
	}
	if want := filepath.Join(dbPath+".sessions", "ses_root.opencode"); session.FilePath != want {
		t.Errorf("FilePath = %q, want %q", session.FilePath, want)
	}
	if session.FileSize == 0 {
		t.Error("FileSize = 0, want DB file size")
	}
	if session.FileMtime.IsZero() {
		t.Error("FileMtime is zero, want DB mtime")
	}
	if len(session.Messages) != 3 {
		t.Fatalf("Messages len = %d, want 3", len(session.Messages))
	}

	user := session.Messages[0]
	if user.UUID != "msg_user" || user.Type != "user" || user.Sender != "human" {
		t.Errorf("first message identity = (%q, %q, %q), want msg_user/user/human", user.UUID, user.Type, user.Sender)
	}
	if user.TextContent != "Please fix login" {
		t.Errorf("user TextContent = %q, want prompt text", user.TextContent)
	}
	if user.CWD != "/repo/worktree" {
		t.Errorf("user CWD = %q, want session directory", user.CWD)
	}
	if user.Sequence != 1 {
		t.Errorf("user Sequence = %d, want 1", user.Sequence)
	}
	if !user.Timestamp.Equal(time.UnixMilli(1_000).UTC()) {
		t.Errorf("user Timestamp = %v, want UnixMilli(1000)", user.Timestamp)
	}

	assistant := session.Messages[1]
	if assistant.UUID != "msg_assistant" || assistant.ParentUUID != "msg_user" {
		t.Errorf("assistant link = (%q, %q), want msg_assistant -> msg_user", assistant.UUID, assistant.ParentUUID)
	}
	for _, want := range []string{"Patched the login handler", "Ran tests", "2 passed"} {
		if !strings.Contains(assistant.TextContent, want) {
			t.Errorf("assistant TextContent %q missing %q", assistant.TextContent, want)
		}
	}

	var content map[string]json.RawMessage
	if err := json.Unmarshal(assistant.Content, &content); err != nil {
		t.Fatalf("assistant Content unmarshal error = %v", err)
	}
	if _, ok := content["info"]; !ok {
		t.Error("assistant Content missing info")
	}
	if _, ok := content["parts"]; !ok {
		t.Error("assistant Content missing parts")
	}

	file := session.Messages[2]
	if file.TextContent != "def login, do: :ok\n\nEmbedded span" {
		t.Errorf("file TextContent = %q, want file source text", file.TextContent)
	}
	if file.Sequence != 3 {
		t.Errorf("file Sequence = %d, want 3 after skipping blank message", file.Sequence)
	}
}

func TestParseAllUsesFirstUserMessageWhenTitleIsDefault(t *testing.T) {
	dbPath := createOpenCodeDB(t,
		withSession("ses_default", "New session - 2026-06-28T12:00:00.000Z", "", "/repo/main", 1_000, 2_000),
		withMessage("ses_default", "msg_long", 1_000, map[string]any{"role": "user"}),
		withTextPart("msg_long", "part_1", strings.Repeat("x", 140)),
	)

	sessions, err := ParseAll(dbPath)
	if err != nil {
		t.Fatalf("ParseAll() error = %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("ParseAll() returned %d sessions, want 1", len(sessions))
	}
	if got, want := sessions[0].Summary, strings.Repeat("x", 117)+"..."; got != want {
		t.Errorf("Summary = %q, want truncated first-user summary", got)
	}
}

func TestParseAllIncludesForkLikeRootSessions(t *testing.T) {
	dbPath := createOpenCodeDB(t,
		withSession("ses_original", "Original", "", "/repo/main", 1_000, 2_000),
		withMessage("ses_original", "msg_original", 1_000, map[string]any{"role": "user"}),
		withTextPart("msg_original", "part_original", "Original prompt"),
		withSession("ses_fork", "Fork", "", "/repo/main", 3_000, 4_000),
		withMessage("ses_fork", "msg_fork", 3_000, map[string]any{"role": "user"}),
		withTextPart("msg_fork", "part_fork", "Fork prompt"),
		withSession("ses_child", "Child session - 2026-06-28T12:00:00.000Z", "ses_original", "/repo/main", 5_000, 6_000),
		withMessage("ses_child", "msg_child", 5_000, map[string]any{"role": "user"}),
		withTextPart("msg_child", "part_child", "Child prompt"),
	)

	sessions, err := ParseAll(dbPath)
	if err != nil {
		t.Fatalf("ParseAll() error = %v", err)
	}
	got := make(map[string]bool)
	for _, session := range sessions {
		got[session.SessionID] = true
	}
	for _, want := range []string{"ses_original", "ses_fork"} {
		if !got[want] {
			t.Errorf("ParseAll() missing root session %s", want)
		}
	}
	if got["ses_child"] {
		t.Error("ParseAll() included child session, want only root sessions")
	}
}

func TestParseAllReadsWALDatabaseWithActiveWriter(t *testing.T) {
	dbPath := createOpenCodeDB(t)
	writer, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	var mode string
	if err := writer.QueryRow(`PRAGMA journal_mode=WAL`).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if strings.ToLower(mode) != "wal" {
		t.Fatalf("journal_mode = %q, want wal", mode)
	}
	if _, err := writer.Exec(`UPDATE session SET title = 'Fix auth flow from WAL' WHERE id = 'ses_root'`); err != nil {
		t.Fatal(err)
	}

	tx, err := writer.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = tx.Rollback()
	}()
	if _, err := tx.Exec(`UPDATE session SET title = 'uncommitted title' WHERE id = 'ses_root'`); err != nil {
		t.Fatal(err)
	}

	sessions, err := ParseAll(dbPath)
	if err != nil {
		t.Fatalf("ParseAll() error = %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("ParseAll() returned %d sessions, want 1", len(sessions))
	}
	if got, want := sessions[0].Summary, "Fix auth flow from WAL"; got != want {
		t.Errorf("Summary = %q, want committed WAL title %q", got, want)
	}
}

func TestParseAllReturnsNilForMissingOrIncompleteDB(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.db")
	if sessions, err := ParseAll(missing); err != nil || sessions != nil {
		t.Fatalf("ParseAll(missing) = (%v, %v), want (nil, nil)", sessions, err)
	}

	dbPath := filepath.Join(t.TempDir(), "opencode.db")
	conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`CREATE TABLE session (id TEXT)`); err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}

	if sessions, err := ParseAll(dbPath); err != nil || sessions != nil {
		t.Fatalf("ParseAll(incomplete) = (%v, %v), want (nil, nil)", sessions, err)
	}
}

func TestDefaultDBPaths(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_DATA_HOME", xdg)
	t.Setenv("OPENCODE_DB", "")

	dataDir := filepath.Join(xdg, "opencode")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatal(err)
	}
	mainDB := filepath.Join(dataDir, "opencode.db")
	betaDB := filepath.Join(dataDir, "opencode-beta.db")
	ignored := filepath.Join(dataDir, "notes.txt")
	for _, path := range []string{betaDB, mainDB, ignored} {
		if err := os.WriteFile(path, []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	if got, want := DefaultDBPaths(), []string{mainDB, betaDB}; !reflect.DeepEqual(got, want) {
		t.Fatalf("DefaultDBPaths() = %v, want %v", got, want)
	}

	t.Setenv("OPENCODE_DB", "custom.db")
	customDB := filepath.Join(dataDir, "custom.db")
	if err := os.WriteFile(customDB, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if got, want := DefaultDBPaths(), []string{customDB}; !reflect.DeepEqual(got, want) {
		t.Fatalf("DefaultDBPaths() with relative OPENCODE_DB = %v, want %v", got, want)
	}

	t.Setenv("OPENCODE_DB", ":memory:")
	if got := DefaultDBPaths(); got != nil {
		t.Fatalf("DefaultDBPaths() with :memory: = %v, want nil", got)
	}
}

func TestParseAllIntegrationDB(t *testing.T) {
	dbPath := os.Getenv("CCRIDER_OPENCODE_INTEGRATION_DB")
	if dbPath == "" {
		t.Skip("set CCRIDER_OPENCODE_INTEGRATION_DB to run against a real OpenCode DB")
	}
	sessionID := os.Getenv("CCRIDER_OPENCODE_INTEGRATION_SESSION_ID")

	sessions, err := ParseAll(dbPath)
	if err != nil {
		t.Fatalf("ParseAll(%q) error = %v", dbPath, err)
	}
	if len(sessions) == 0 {
		t.Fatalf("ParseAll(%q) returned no sessions", dbPath)
	}
	if sessionID == "" {
		return
	}

	var found bool
	for _, session := range sessions {
		if session.SessionID != sessionID {
			continue
		}
		found = true
		if len(session.Messages) == 0 {
			t.Fatalf("integration session %s has no parsed messages", sessionID)
		}
		var text strings.Builder
		for _, msg := range session.Messages {
			text.WriteString(msg.TextContent)
		}
		if strings.TrimSpace(text.String()) == "" {
			t.Fatalf("integration session %s has no parsed text", sessionID)
		}
	}
	if !found {
		t.Fatalf("integration session %s not found in %d parsed sessions", sessionID, len(sessions))
	}
}

type dbSeed func(*testing.T, *sql.DB)

func createOpenCodeDB(t *testing.T, seeds ...dbSeed) string {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "opencode.db")
	conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := conn.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	execAll(t, conn,
		`CREATE TABLE project (id TEXT PRIMARY KEY, worktree TEXT)`,
		`CREATE TABLE session (
			id TEXT PRIMARY KEY,
			project_id TEXT,
			parent_id TEXT,
			title TEXT,
			directory TEXT,
			version TEXT,
			time_created INTEGER,
			time_updated INTEGER
		)`,
		`CREATE TABLE message (
			id TEXT PRIMARY KEY,
			session_id TEXT,
			time_created INTEGER,
			data TEXT
		)`,
		`CREATE TABLE part (
			id TEXT PRIMARY KEY,
			message_id TEXT,
			data TEXT
		)`,
		`INSERT INTO project (id, worktree) VALUES ('proj_1', '/repo/main')`,
	)

	if len(seeds) == 0 {
		seeds = []dbSeed{
			withSession("ses_root", "Fix auth flow", "", "/repo/worktree", 1_000, 4_000),
			withSession("ses_child", "Child session - 2026-06-28T12:00:00.000Z", "ses_root", "/repo/worktree", 1_000, 4_000),
			withMessage("ses_root", "msg_user", 1_000, map[string]any{"role": "user"}),
			withTextPart("msg_user", "part_1", "Please fix login"),
			withMessage("ses_root", "msg_assistant", 2_000, map[string]any{"role": "assistant", "parentID": "msg_user"}),
			withTextPart("msg_assistant", "part_2", "Patched the login handler"),
			withToolPart("msg_assistant", "part_3", map[string]any{
				"status": "completed",
				"title":  "Ran tests",
				"output": map[string]any{"text": "2 passed"},
			}),
			withMessage("ses_root", "msg_file", 3_000, map[string]any{"role": "user", "parentID": "msg_assistant"}),
			withFilePart("msg_file", "part_4", map[string]any{"text": "def login, do: :ok"}),
			withFilePart("msg_file", "part_5", map[string]any{"text": map[string]any{"value": "Embedded span"}}),
			withMessage("ses_root", "msg_blank", 4_000, map[string]any{"role": "assistant", "parentID": "msg_file"}),
			withTextPart("msg_blank", "part_6", "   "),
			withMessage("ses_child", "msg_child", 1_000, map[string]any{"role": "user"}),
			withTextPart("msg_child", "part_1_child", "Skipped child session"),
		}
	}

	for _, seed := range seeds {
		seed(t, conn)
	}

	return dbPath
}

func withSession(id, title, parentID, directory string, createdMS, updatedMS int64) dbSeed {
	return func(t *testing.T, conn *sql.DB) {
		t.Helper()
		var parent any
		if parentID != "" {
			parent = parentID
		}
		_, err := conn.Exec(
			`INSERT INTO session (id, project_id, parent_id, title, directory, version, time_created, time_updated)
			 VALUES (?, 'proj_1', ?, ?, ?, '0.6.0', ?, ?)`,
			id,
			parent,
			title,
			directory,
			createdMS,
			updatedMS,
		)
		if err != nil {
			t.Fatal(err)
		}
	}
}

func withMessage(sessionID, id string, createdMS int64, data map[string]any) dbSeed {
	return func(t *testing.T, conn *sql.DB) {
		t.Helper()
		raw := mustJSON(t, data)
		if _, err := conn.Exec(`INSERT INTO message (id, session_id, time_created, data) VALUES (?, ?, ?, ?)`, id, sessionID, createdMS, raw); err != nil {
			t.Fatal(err)
		}
	}
}

func withTextPart(messageID, id, text string) dbSeed {
	return withPart(messageID, id, map[string]any{"type": "text", "text": text})
}

func withToolPart(messageID, id string, state map[string]any) dbSeed {
	return withPart(messageID, id, map[string]any{"type": "tool", "state": state})
}

func withFilePart(messageID, id string, source map[string]any) dbSeed {
	return withPart(messageID, id, map[string]any{"type": "file", "source": source})
}

func withPart(messageID, id string, data map[string]any) dbSeed {
	return func(t *testing.T, conn *sql.DB) {
		t.Helper()
		raw := mustJSON(t, data)
		if _, err := conn.Exec(`INSERT INTO part (id, message_id, data) VALUES (?, ?, ?)`, id, messageID, raw); err != nil {
			t.Fatal(err)
		}
	}
}

func execAll(t *testing.T, conn *sql.DB, statements ...string) {
	t.Helper()
	for _, statement := range statements {
		if _, err := conn.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
