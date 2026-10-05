package opencodesessions

import (
	"database/sql"
	"strings"
	"testing"
)

const v2MessageTable = `CREATE TABLE session_message (
	id TEXT PRIMARY KEY,
	session_id TEXT NOT NULL,
	type TEXT NOT NULL,
	seq INTEGER NOT NULL,
	time_created INTEGER NOT NULL,
	time_updated INTEGER NOT NULL,
	data TEXT NOT NULL
)`

func withV2Table() dbSeed {
	return func(t *testing.T, conn *sql.DB) {
		t.Helper()
		execAll(t, conn, v2MessageTable)
	}
}

func withV2Message(sessionID, id, msgType string, seq, createdMS int64, data map[string]any) dbSeed {
	return func(t *testing.T, conn *sql.DB) {
		t.Helper()
		_, err := conn.Exec(
			`INSERT INTO session_message (id, session_id, type, seq, time_created, time_updated, data) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			id, sessionID, msgType, seq, createdMS, createdMS, mustJSON(t, data),
		)
		if err != nil {
			t.Fatal(err)
		}
	}
}

func v2Seeds() []dbSeed {
	return []dbSeed{
		withV2Table(),
		// v1 session, written before the upgrade
		withSession("ses_v1", "Old v1 session", "", "/repo/a", 1_000, 2_000),
		withMessage("ses_v1", "msg_v1", 1_000, map[string]any{"role": "user"}),
		withTextPart("msg_v1", "part_v1", "v1 prompt"),
		// v2 session; seq order disagrees with time_created on purpose
		withSession("ses_v2", "New session - 2026-10-01T12:00:00.000Z", "", "/repo/b", 5_000, 9_000),
		withV2Message("ses_v2", "msg_u", "user", 1, 6_000, map[string]any{
			"text":  "Add retry to the uploader",
			"files": []any{map[string]any{"uri": "file:///repo/b/up.go", "mime": "text/plain", "source": map[string]any{"start": 0, "end": 3, "text": "func Upload()"}}},
			"time":  map[string]any{"created": 6_000},
		}),
		withV2Message("ses_v2", "msg_switch", "model-switched", 2, 5_500, map[string]any{"model": map[string]any{"id": "m", "providerID": "p"}}),
		withV2Message("ses_v2", "msg_a", "assistant", 3, 5_000, map[string]any{
			"agent": "build",
			"model": map[string]any{"id": "m", "providerID": "p"},
			"content": []any{
				map[string]any{"type": "reasoning", "id": "r1", "text": "secret thoughts"},
				map[string]any{"type": "text", "id": "t1", "text": "Added exponential backoff"},
				map[string]any{"type": "tool", "id": "c1", "name": "bash", "state": map[string]any{
					"status": "completed", "input": "go test", "structured": map[string]any{},
					"content": []any{map[string]any{"type": "text", "text": "ok uploader 0.2s"}},
				}},
				map[string]any{"type": "tool", "id": "c2", "name": "edit", "state": map[string]any{
					"status": "error", "input": "{}", "structured": map[string]any{}, "content": []any{},
					"error": map[string]any{"message": "file changed on disk"},
				}},
				map[string]any{"type": "tool", "id": "c3", "name": "read", "state": map[string]any{"status": "pending", "input": "{}"}},
			},
			"time": map[string]any{"created": 5_000, "completed": 5_100},
		}),
		withV2Message("ses_v2", "msg_shell", "shell", 4, 7_000, map[string]any{"callID": "x", "command": "git status", "output": "clean"}),
		withV2Message("ses_v2", "msg_syn", "synthetic", 5, 7_100, map[string]any{"sessionID": "ses_v2", "text": "Continue if you have next steps"}),
		withV2Message("ses_v2", "msg_sys", "system", 6, 7_200, map[string]any{"text": "system reminder"}),
		withV2Message("ses_v2", "msg_c", "compaction", 7, 8_000, map[string]any{"reason": "auto", "summary": "Retry added and tested", "recent": "msg_shell"}),
		withV2Message("ses_v2", "msg_inflight", "assistant", 8, 9_000, map[string]any{"agent": "build", "model": map[string]any{"id": "m", "providerID": "p"}, "content": []any{}, "time": map[string]any{"created": 9_000}}),
		// v2-era session whose content OpenCode's migrations deleted
		withSession("ses_empty", "Emptied by migration", "", "/repo/c", 1_000, 1_000),
	}
}

func parseByID(t *testing.T, dbPath string) map[string][]string {
	t.Helper()
	sessions, err := ParseAll(dbPath)
	if err != nil {
		t.Fatalf("ParseAll() error = %v", err)
	}
	got := map[string][]string{}
	for _, s := range sessions {
		for _, m := range s.Messages {
			got[s.SessionID] = append(got[s.SessionID], m.Type+": "+m.TextContent)
		}
	}
	return got
}

func TestParseAllReadsV1AndV2SessionsFromOneDB(t *testing.T) {
	dbPath := createOpenCodeDB(t, v2Seeds()...)
	got := parseByID(t, dbPath)

	if len(got) != 2 {
		t.Fatalf("parsed sessions = %v, want ses_v1 and ses_v2 only", got)
	}
	if want := []string{"user: v1 prompt"}; strings.Join(got["ses_v1"], "|") != strings.Join(want, "|") {
		t.Errorf("ses_v1 = %q, want %q", got["ses_v1"], want)
	}

	want := []string{
		"user: Add retry to the uploader\n\nfunc Upload()",
		"assistant: Added exponential backoff\n\nok uploader 0.2s\n\nfile changed on disk",
		"user: $ git status\n\nclean",
		"user: Continue if you have next steps",
		"assistant: Retry added and tested",
	}
	if strings.Join(got["ses_v2"], "|") != strings.Join(want, "|") {
		t.Errorf("ses_v2 messages =\n%q\nwant\n%q", got["ses_v2"], want)
	}
}

func TestParseAllV2SessionMetadata(t *testing.T) {
	dbPath := createOpenCodeDB(t, v2Seeds()...)
	sessions, err := ParseAll(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range sessions {
		if s.SessionID != "ses_v2" {
			continue
		}
		// The default v2 title falls back to the first prompt.
		if !strings.HasPrefix(s.Summary, "Add retry to the uploader") {
			t.Errorf("Summary = %q", s.Summary)
		}
		for i, m := range s.Messages {
			if m.Sequence != i+1 {
				t.Errorf("message %d Sequence = %d", i, m.Sequence)
			}
			if m.CWD != "/repo/b" {
				t.Errorf("message %d CWD = %q", i, m.CWD)
			}
		}
		if s.Messages[1].ParentUUID != s.Messages[0].UUID {
			t.Errorf("ParentUUID = %q, want previous message %q", s.Messages[1].ParentUUID, s.Messages[0].UUID)
		}
		if strings.Contains(s.Messages[1].TextContent, "secret thoughts") {
			t.Error("reasoning text leaked into TextContent")
		}
		return
	}
	t.Fatal("ses_v2 not parsed")
}

const sessionV2Table = `CREATE TABLE session_v2 (
	id TEXT PRIMARY KEY,
	project_id TEXT NOT NULL,
	parent_id TEXT,
	fork_session_id TEXT,
	title TEXT,
	directory TEXT NOT NULL,
	version TEXT NOT NULL,
	time_created INTEGER NOT NULL,
	time_updated INTEGER NOT NULL
)`

func withSessionV2(id string, title any, parentID any, directory string, createdMS, updatedMS int64) dbSeed {
	return func(t *testing.T, conn *sql.DB) {
		t.Helper()
		_, err := conn.Exec(
			`INSERT INTO session_v2 (id, project_id, parent_id, title, directory, version, time_created, time_updated)
			 VALUES (?, 'proj_1', ?, ?, ?, '0.0.0-beta-19271', ?, ?)`,
			id, parentID, title, directory, createdMS, updatedMS,
		)
		if err != nil {
			t.Fatal(err)
		}
	}
}

// A DB created from scratch by OpenCode 2.0 has session_v2 and no v1
// session/message/part tables. Its title column is nullable.
func TestParseAllOpenCode2FreshDB(t *testing.T) {
	dbPath := createOpenCodeDB(t,
		func(t *testing.T, conn *sql.DB) {
			execAll(t, conn, `DROP TABLE session`, `DROP TABLE message`, `DROP TABLE part`, sessionV2Table, v2MessageTable)
		},
		withSessionV2("ses_v2", nil, nil, "/repo", 1_000, 2_000),
		withV2Message("ses_v2", "msg_u", "user", 4, 1_000, map[string]any{"text": "hello v2", "files": []any{}}),
		withV2Message("ses_v2", "msg_a", "assistant", 7, 1_100, map[string]any{
			"content": []any{
				map[string]any{"type": "tool", "id": "call_1", "name": "bash", "executed": false, "state": map[string]any{
					"status": "error", "input": map[string]any{"command": "ls"},
					"error": map[string]any{"type": "tool.execution", "message": "Unknown tool: bash"},
				}},
			},
		}),
		withSessionV2("ses_child", "child", "ses_v2", "/repo", 1_000, 2_000),
		withV2Message("ses_child", "msg_c", "user", 1, 1_000, map[string]any{"text": "subagent"}),
	)
	sessions, err := ParseAll(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].SessionID != "ses_v2" {
		t.Fatalf("sessions = %d, want only root ses_v2", len(sessions))
	}
	if sessions[0].Summary != "hello v2" {
		t.Errorf("Summary = %q, want first prompt for a NULL title", sessions[0].Summary)
	}
	got := parseByID(t, dbPath)
	want := "user: hello v2|assistant: Unknown tool: bash"
	if strings.Join(got["ses_v2"], "|") != want {
		t.Errorf("got %q, want %q", got["ses_v2"], want)
	}
}

// An upgraded DB keeps v1 sessions in session and adds OpenCode 2.0 sessions
// in session_v2; both import.
func TestParseAllUpgradedDBReadsSessionAndSessionV2(t *testing.T) {
	dbPath := createOpenCodeDB(t,
		func(t *testing.T, conn *sql.DB) { execAll(t, conn, sessionV2Table, v2MessageTable) },
		withSession("ses_old", "Old", "", "/repo", 1_000, 1_000),
		withMessage("ses_old", "msg_old", 1_000, map[string]any{"role": "user"}),
		withTextPart("msg_old", "part_old", "from v1"),
		withSessionV2("ses_new", "New", nil, "/repo", 2_000, 2_000),
		withV2Message("ses_new", "msg_new", "user", 1, 2_000, map[string]any{"text": "from 2.0"}),
	)
	got := parseByID(t, dbPath)
	if strings.Join(got["ses_old"], "|") != "user: from v1" || strings.Join(got["ses_new"], "|") != "user: from 2.0" {
		t.Errorf("got %v", got)
	}
}

// session_message from before OpenCode added seq is ignored; OpenCode's own
// migrations deleted those rows.
func TestParseAllIgnoresPreSeqSessionMessage(t *testing.T) {
	dbPath := createOpenCodeDB(t,
		func(t *testing.T, conn *sql.DB) {
			execAll(t, conn, `CREATE TABLE session_message (id TEXT PRIMARY KEY, session_id TEXT, type TEXT, data TEXT)`)
		},
		withSession("ses_v1", "Old", "", "/repo", 1_000, 1_000),
		withMessage("ses_v1", "msg_v1", 1_000, map[string]any{"role": "user"}),
		withTextPart("msg_v1", "part_v1", "still v1"),
	)
	got := parseByID(t, dbPath)
	if strings.Join(got["ses_v1"], "|") != "user: still v1" {
		t.Errorf("got %v", got)
	}
}
