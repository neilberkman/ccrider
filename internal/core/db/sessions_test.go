package db

import (
	"errors"
	"os"
	"testing"
)

// newSessionTestDB creates a temp database seeded with one claude-style
// session (bare UUID id) and one codex-style session (rollout-prefixed id),
// each with a couple of messages.
func newSessionTestDB(t *testing.T) *DB {
	t.Helper()

	tmpfile, err := os.CreateTemp("", "test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(tmpfile.Name()) })
	_ = tmpfile.Close()

	database, err := New(tmpfile.Name())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	seed := []struct {
		sessionID string
		provider  string
	}{
		{"8650b828-b3a9-4db4-81c0-64d0fee6dd70", "claude"},
		{"rollout-2026-07-24T08-08-11-019f94ab-7288-7043-b7ef-5e5b852ed3a5", "codex"},
	}
	for _, s := range seed {
		res, err := database.Exec(`
			INSERT INTO sessions (session_id, project_path, summary, provider, created_at, updated_at)
			VALUES (?, ?, ?, ?, datetime('now'), datetime('now'))
		`, s.sessionID, "/test/project", "Test session", s.provider)
		if err != nil {
			t.Fatalf("failed to insert session %s: %v", s.sessionID, err)
		}
		rowID, err := res.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		for seq := 1; seq <= 2; seq++ {
			_, err := database.Exec(`
				INSERT INTO messages (uuid, session_id, type, sender, text_content, timestamp, sequence)
				VALUES (?, ?, 'user', 'user', 'hello', datetime('now'), ?)
			`, s.sessionID+"-msg-"+string(rune('0'+seq)), rowID, seq)
			if err != nil {
				t.Fatalf("failed to insert message: %v", err)
			}
		}
	}

	return database
}

func TestResolveSessionID(t *testing.T) {
	database := newSessionTestDB(t)

	codexFull := "rollout-2026-07-24T08-08-11-019f94ab-7288-7043-b7ef-5e5b852ed3a5"
	codexUUID := "019f94ab-7288-7043-b7ef-5e5b852ed3a5"
	claudeUUID := "8650b828-b3a9-4db4-81c0-64d0fee6dd70"

	t.Run("exact match claude", func(t *testing.T) {
		got, err := database.ResolveSessionID(claudeUUID)
		if err != nil {
			t.Fatalf("ResolveSessionID() error = %v", err)
		}
		if got != claudeUUID {
			t.Errorf("got %q, want %q", got, claudeUUID)
		}
	})

	t.Run("exact match codex full id", func(t *testing.T) {
		got, err := database.ResolveSessionID(codexFull)
		if err != nil {
			t.Fatalf("ResolveSessionID() error = %v", err)
		}
		if got != codexFull {
			t.Errorf("got %q, want %q", got, codexFull)
		}
	})

	t.Run("bare uuid resolves to codex full id", func(t *testing.T) {
		got, err := database.ResolveSessionID(codexUUID)
		if err != nil {
			t.Fatalf("ResolveSessionID() error = %v", err)
		}
		if got != codexFull {
			t.Errorf("got %q, want %q", got, codexFull)
		}
	})

	t.Run("unknown id returns ErrSessionNotFound", func(t *testing.T) {
		_, err := database.ResolveSessionID("00000000-0000-0000-0000-000000000000")
		if !errors.Is(err, ErrSessionNotFound) {
			t.Errorf("got %v, want ErrSessionNotFound", err)
		}
	})

	t.Run("ambiguous suffix is an error", func(t *testing.T) {
		_, err := database.ResolveSessionID("de5936619") // no session ends with -de5936619
		if !errors.Is(err, ErrSessionNotFound) {
			t.Errorf("got %v, want ErrSessionNotFound", err)
		}

		// Two sessions sharing a suffix must not silently pick one
		for _, id := range []string{"rollout-2026-01-01T00-00-00-dupe-suffix", "rollout-2026-01-02T00-00-00-dupe-suffix"} {
			if _, err := database.Exec(`
				INSERT INTO sessions (session_id, project_path, summary, provider, created_at, updated_at)
				VALUES (?, '/p', 's', 'codex', datetime('now'), datetime('now'))
			`, id); err != nil {
				t.Fatal(err)
			}
		}
		_, err = database.ResolveSessionID("dupe-suffix")
		if err == nil || errors.Is(err, ErrSessionNotFound) {
			t.Errorf("got %v, want ambiguity error", err)
		}
	})
}

func TestGetSessionMessagesResolvesBareUUID(t *testing.T) {
	database := newSessionTestDB(t)

	msgs, total, err := database.GetSessionMessages("019f94ab-7288-7043-b7ef-5e5b852ed3a5", GetSessionMessagesOptions{})
	if err != nil {
		t.Fatalf("GetSessionMessages() error = %v", err)
	}
	if total != 2 || len(msgs) != 2 {
		t.Errorf("got %d messages (total %d), want 2", len(msgs), total)
	}
}

func TestGetSessionMessagesUnknownSessionErrors(t *testing.T) {
	database := newSessionTestDB(t)

	_, _, err := database.GetSessionMessages("00000000-0000-0000-0000-000000000000", GetSessionMessagesOptions{})
	if !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("got %v, want ErrSessionNotFound", err)
	}
}

func TestGetSessionDetailResolvesBareUUID(t *testing.T) {
	database := newSessionTestDB(t)

	codexFull := "rollout-2026-07-24T08-08-11-019f94ab-7288-7043-b7ef-5e5b852ed3a5"
	detail, err := database.GetSessionDetail("019f94ab-7288-7043-b7ef-5e5b852ed3a5")
	if err != nil {
		t.Fatalf("GetSessionDetail() error = %v", err)
	}
	if detail.SessionID != codexFull {
		t.Errorf("SessionID = %q, want %q", detail.SessionID, codexFull)
	}
	if len(detail.Messages) != 2 {
		t.Errorf("got %d messages, want 2", len(detail.Messages))
	}
}

func TestGetSessionLaunchInfoResolvesBareUUID(t *testing.T) {
	database := newSessionTestDB(t)

	codexFull := "rollout-2026-07-24T08-08-11-019f94ab-7288-7043-b7ef-5e5b852ed3a5"
	info, _, err := database.GetSessionLaunchInfo("019f94ab-7288-7043-b7ef-5e5b852ed3a5")
	if err != nil {
		t.Fatalf("GetSessionLaunchInfo() error = %v", err)
	}
	if info.SessionID != codexFull {
		t.Errorf("SessionID = %q, want %q", info.SessionID, codexFull)
	}
}
