package importer

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/neilberkman/ccrider/internal/core/db"
	"github.com/neilberkman/ccrider/internal/core/search"
	"github.com/neilberkman/ccrider/pkg/ccsessions"
	"github.com/neilberkman/ccrider/pkg/codexsessions"
	"github.com/neilberkman/ccrider/pkg/pisessions"
)

func TestProgressReporterFinishReportsImportedCount(t *testing.T) {
	var output bytes.Buffer
	progress := NewProgressReporter(&output, 5, false)
	progress.Update("first", "")
	progress.Update("second", "")
	progress.Finish()

	if got := output.String(); !strings.Contains(got, "Completed: Imported 2 sessions") {
		t.Fatalf("progress output = %q, want imported count", got)
	}
}

func TestPrepareSourceOptionalEnumerateErrorSkips(t *testing.T) {
	boom := errors.New("schema changed")
	imp := New(nil)

	prepared, err := imp.PrepareSource(context.Background(), Source{
		Path:     "/tmp/opencode.db",
		Provider: "opencode",
		Optional: true,
		EnumerateFn: func() ([]*ccsessions.ParsedSession, error) {
			return nil, boom
		},
	})
	if err != nil {
		t.Fatalf("PrepareSource() error = %v, want nil", err)
	}
	if prepared.Provider != "opencode" {
		t.Fatalf("Provider = %q, want opencode", prepared.Provider)
	}
	if !errors.Is(prepared.Warning, boom) {
		t.Fatalf("Warning = %v, want %v", prepared.Warning, boom)
	}
	if prepared.Total != 0 {
		t.Fatalf("Total = %d, want 0", prepared.Total)
	}
	if result, err := prepared.Run(context.Background(), nil, false); err != nil || result.Skipped != 0 {
		t.Fatalf("Run() = (%+v, %v), want no skipped sessions", result, err)
	}
}

func TestPrepareSourceEnumerateErrorFatalByDefault(t *testing.T) {
	boom := errors.New("schema changed")
	imp := New(nil)

	_, err := imp.PrepareSource(context.Background(), Source{
		Path:     "/tmp/copilot",
		Provider: "copilot",
		EnumerateFn: func() ([]*ccsessions.ParsedSession, error) {
			return nil, boom
		},
	})
	if !errors.Is(err, boom) {
		t.Fatalf("PrepareSource() error = %v, want %v", err, boom)
	}
}

func TestPrepareSourceDoesNotSwallowCancellationForOptionalSource(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	imp := New(nil)

	_, err := imp.PrepareSource(ctx, Source{
		Path:     "/tmp/opencode.db",
		Provider: "opencode",
		Optional: true,
		EnumerateFn: func() ([]*ccsessions.ParsedSession, error) {
			cancel()
			return nil, errors.New("enumeration interrupted")
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("PrepareSource() error = %v, want context cancellation", err)
	}
}

func TestPrepareSourceOptionalRemoteErrorSkips(t *testing.T) {
	boom := errors.New("Amp is offline")
	imp := New(nil)
	prepared, err := imp.PrepareSource(context.Background(), Source{
		Path:     "authenticated Amp account",
		Provider: "amp",
		Optional: true,
		Remote: &RemoteSource{
			List: func(context.Context) ([]RemoteSessionRef, error) { return nil, boom },
		},
	})
	if err != nil {
		t.Fatalf("PrepareSource() error = %v, want nil", err)
	}
	if !errors.Is(prepared.Warning, boom) {
		t.Fatalf("Warning = %v, want %v", prepared.Warning, boom)
	}
	if result, err := prepared.Run(context.Background(), nil, false); err != nil || result.Skipped != 0 {
		t.Fatalf("Run() = (%+v, %v), want no skipped sessions", result, err)
	}
}

func TestDefaultSourcesIncludesAmpWhenCLIExists(t *testing.T) {
	home := t.TempDir()
	binDir := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", binDir)
	if err := os.WriteFile(filepath.Join(binDir, "amp"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	for _, source := range DefaultSources(true) {
		if source.Provider == "amp" {
			if source.Remote == nil || source.Remote.List == nil || source.Remote.Fetch == nil {
				t.Fatal("Amp source must use remote list/fetch import")
			}
			if !source.Optional {
				t.Fatal("Amp source must be optional when offline or unauthenticated")
			}
			return
		}
	}
	t.Fatal("DefaultSources() did not include Amp")
}

func TestDefaultSourcesOmitsAmpUntilEnabled(t *testing.T) {
	binDir := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", binDir)
	if err := os.WriteFile(filepath.Join(binDir, "amp"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, source := range DefaultSources(false) {
		if source.Provider == ampProvider {
			t.Fatal("DefaultSources(false) included Amp")
		}
	}
}

type unitProgress struct{ current int }

func (p *unitProgress) Update(string, string) { p.current++ }
func (p *unitProgress) Skip()                 { p.current++ }
func (*unitProgress) Finish()                 {}

func TestPreparedRemoteProgressIncludesFailures(t *testing.T) {
	database, err := db.New(filepath.Join(t.TempDir(), "progress.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	stamp := time.Now().UTC()
	source := Source{Provider: ampProvider, Remote: &RemoteSource{
		List: func(context.Context) ([]RemoteSessionRef, error) {
			return []RemoteSessionRef{{ImportID: "ok", Revision: "1"}, {ImportID: "bad", Revision: "1"}}, nil
		},
		Fetch: func(_ context.Context, ref RemoteSessionRef) (*ccsessions.ParsedSession, error) {
			if ref.ImportID == "bad" {
				return nil, errors.New("export failed")
			}
			return &ccsessions.ParsedSession{SessionID: ref.ImportID, ProjectPath: "/tmp", FileMtime: stamp, Messages: []ccsessions.ParsedMessage{{UUID: "one", Type: "user", Sender: "human", TextContent: "hello", Timestamp: stamp}}}, nil
		},
	}}
	prepared, err := New(database).PrepareSource(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	progress := &unitProgress{}
	result, err := prepared.Run(context.Background(), progress, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.Imported != 1 || len(result.Failures) != 1 || progress.current != 2 {
		t.Fatalf("result = %+v, progress = %d; want one import, one failure, 2/2", result, progress.current)
	}
}

func TestPreparedOptionalRemoteTimeoutKeepsPartialImport(t *testing.T) {
	database, err := db.New(filepath.Join(t.TempDir(), "partial-timeout.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()

	stamp := time.Now().UTC()
	source := Source{Provider: ampProvider, Optional: true, Remote: &RemoteSource{
		List: func(context.Context) ([]RemoteSessionRef, error) {
			return []RemoteSessionRef{
				{ImportID: "imported", Revision: "1"},
				{ImportID: "timed-out", Revision: "1"},
				{ImportID: "not-attempted", Revision: "1"},
			}, nil
		},
		Fetch: func(ctx context.Context, ref RemoteSessionRef) (*ccsessions.ParsedSession, error) {
			if ref.ImportID == "timed-out" {
				<-ctx.Done()
				return nil, ctx.Err()
			}
			return &ccsessions.ParsedSession{
				SessionID:   ref.ImportID,
				ProjectPath: "/tmp",
				FileMtime:   stamp,
				Messages: []ccsessions.ParsedMessage{{
					UUID: "partial-timeout-message", Type: "user", Sender: "human", TextContent: "hello", Timestamp: stamp,
				}},
			}, nil
		},
	}}

	prepared, err := New(database).PrepareSource(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	progress := &unitProgress{}
	result, err := prepared.Run(ctx, progress, false)
	if err != nil {
		t.Fatalf("Run() error = %v, want optional timeout to degrade", err)
	}
	if result.Imported != 1 || len(result.Failures) != 2 || progress.current != 3 {
		t.Fatalf("result = %+v, progress = %d; want partial import, two pending failures, 3/3", result, progress.current)
	}
	wantFailureIDs := []string{"timed-out", "not-attempted"}
	for index, failure := range result.Failures {
		if failure.ID != wantFailureIDs[index] || !errors.Is(failure.Err, context.DeadlineExceeded) {
			t.Fatalf("failure %d = %+v, want deadline for %q", index, failure, wantFailureIDs[index])
		}
	}
}

func TestImportRemoteCancellationDoesNotFailUntouchedSessions(t *testing.T) {
	database, err := db.New(filepath.Join(t.TempDir(), "remote-cancel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	stamp := time.Now().UTC()
	refs := []RemoteSessionRef{{ImportID: "imported", Revision: "1"}, {ImportID: "cancel", Revision: "1"}, {ImportID: "untouched", Revision: "1"}}
	fetch := func(_ context.Context, ref RemoteSessionRef) (*ccsessions.ParsedSession, error) {
		if ref.ImportID == "cancel" {
			cancel()
			return nil, context.Canceled
		}
		return &ccsessions.ParsedSession{
			SessionID: ref.ImportID, ProjectPath: "/tmp", FileMtime: stamp,
			Messages: []ccsessions.ParsedMessage{{
				UUID: "remote-cancel-message", Type: "user", Sender: "human", TextContent: "hello", Timestamp: stamp,
			}},
		}, nil
	}
	progress := &unitProgress{}
	result, err := New(database).ImportRemote(ctx, refs, fetch, progress, false, ampProvider)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ImportRemote() error = %v, want context cancellation", err)
	}
	if result.Imported != 1 || len(result.Failures) != 0 || progress.current != 1 {
		t.Fatalf("result = %+v, progress = %d; untouched sessions must not become failures", result, progress.current)
	}
}

func TestSyncAllReportsPerSessionFailures(t *testing.T) {
	database, err := db.New(filepath.Join(t.TempDir(), "best-effort.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bad.jsonl"), []byte("bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	source := Source{
		Path:     dir,
		Provider: "claude",
		ParseFn: func(string) (*ccsessions.ParsedSession, error) {
			return nil, errors.New("unsupported schema version")
		},
	}
	err = New(database).SyncAll(context.Background(), []Source{source}, false)
	if err == nil || !strings.Contains(err.Error(), "unsupported schema version") {
		t.Fatalf("SyncAll() error = %v, want structured session failure", err)
	}
}

func TestDefaultSourcesIncludesAntigravityWhenStoreExists(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".gemini", "antigravity-cli", "brain"), 0755); err != nil {
		t.Fatal(err)
	}

	for _, source := range DefaultSources(false) {
		if source.Provider == "antigravity" {
			if source.EnumerateFn == nil {
				t.Fatal("Antigravity source must enumerate canonical transcripts")
			}
			return
		}
	}
	t.Fatal("DefaultSources() did not include Antigravity")
}

func TestImportSession(t *testing.T) {
	// Setup test database
	tmpfile, err := os.CreateTemp("", "test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Remove(tmpfile.Name())
	}()
	_ = tmpfile.Close()

	database, err := db.New(tmpfile.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = database.Close()
	}()

	imp := New(database)

	// Parse test session
	session, err := ccsessions.ParseFile("../../../pkg/ccsessions/testdata/sample.jsonl")
	if err != nil {
		t.Fatal(err)
	}

	inode, device, _ := getFileIdentity(session.FilePath)
	hash, err := computeFileHash(session.FilePath)
	if err != nil {
		t.Fatal(err)
	}

	err = imp.ImportSession(session, 0, inode, device, hash, "claude")
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

func TestImportSession_ResumedSession(t *testing.T) {
	// Setup test database
	tmpfile, err := os.CreateTemp("", "test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Remove(tmpfile.Name())
	}()
	_ = tmpfile.Close()

	database, err := db.New(tmpfile.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = database.Close()
	}()

	imp := New(database)

	// Import first session
	session1, err := ccsessions.ParseFile("../../../pkg/ccsessions/testdata/sample.jsonl")
	if err != nil {
		t.Fatal(err)
	}

	inode, device, _ := getFileIdentity(session1.FilePath)
	hash, err := computeFileHash(session1.FilePath)
	if err != nil {
		t.Fatal(err)
	}

	err = imp.ImportSession(session1, 0, inode, device, hash, "claude")
	if err != nil {
		t.Fatalf("ImportSession() error = %v", err)
	}

	err = imp.ImportSession(session1, 0, inode, device, hash, "claude")
	if err != nil {
		t.Fatalf("ImportSession() second import error = %v", err)
	}

	// Should still have only 1 session
	var sessionCount int
	err = database.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&sessionCount)
	if err != nil {
		t.Fatal(err)
	}

	if sessionCount != 1 {
		t.Errorf("Expected 1 session after duplicate import, got %d", sessionCount)
	}

	// Should still have only 2 messages (duplicates ignored)
	var messageCount int
	err = database.QueryRow("SELECT COUNT(*) FROM messages").Scan(&messageCount)
	if err != nil {
		t.Fatal(err)
	}

	if messageCount != 2 {
		t.Errorf("Expected 2 messages after duplicate import, got %d", messageCount)
	}
}

func TestImportSession_AgentSession(t *testing.T) {
	// Setup test database
	tmpfile, err := os.CreateTemp("", "test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Remove(tmpfile.Name())
	}()
	_ = tmpfile.Close()

	database, err := db.New(tmpfile.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = database.Close()
	}()

	imp := New(database)

	// Parse and import agent session
	session, err := ccsessions.ParseFile("../../../pkg/ccsessions/testdata/agent-session.jsonl")
	if err != nil {
		t.Fatal(err)
	}

	inode, device, _ := getFileIdentity(session.FilePath)
	hash, err := computeFileHash(session.FilePath)
	if err != nil {
		t.Fatal(err)
	}

	err = imp.ImportSession(session, 0, inode, device, hash, "claude")
	if err != nil {
		t.Fatalf("ImportSession() error = %v", err)
	}

	// Verify session was imported with correct sessionId (not filename)
	var sessionID string
	err = database.QueryRow("SELECT session_id FROM sessions").Scan(&sessionID)
	if err != nil {
		t.Fatal(err)
	}

	if sessionID != "agent-session" {
		t.Errorf("Expected session_id 'agent-session' (filename), got %s", sessionID)
	}
}

func TestImportSession_CodexSession(t *testing.T) {
	tmpfile, err := os.CreateTemp("", "test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(tmpfile.Name()) }()
	_ = tmpfile.Close()

	database, err := db.New(tmpfile.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()

	imp := New(database)

	session, err := codexsessions.ParseFile("../../../pkg/codexsessions/testdata/sample.jsonl")
	if err != nil {
		t.Fatal(err)
	}

	inode, device, _ := getFileIdentity(session.FilePath)
	hash, err := computeFileHash(session.FilePath)
	if err != nil {
		t.Fatal(err)
	}

	err = imp.ImportSession(session, 0, inode, device, hash, "codex")
	if err != nil {
		t.Fatalf("ImportSession() error = %v", err)
	}

	// Verify provider is stored correctly
	var provider string
	err = database.QueryRow("SELECT provider FROM sessions").Scan(&provider)
	if err != nil {
		t.Fatal(err)
	}
	if provider != "codex" {
		t.Errorf("Expected provider 'codex', got %q", provider)
	}

	// Verify messages were imported
	var msgCount int
	err = database.QueryRow("SELECT COUNT(*) FROM messages").Scan(&msgCount)
	if err != nil {
		t.Fatal(err)
	}
	if msgCount != 4 {
		t.Errorf("Expected 4 messages, got %d", msgCount)
	}

	// Verify idempotent re-import
	err = imp.ImportSession(session, 0, inode, device, hash, "codex")
	if err != nil {
		t.Fatalf("ImportSession() re-import error = %v", err)
	}

	var sessionCount int
	err = database.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&sessionCount)
	if err != nil {
		t.Fatal(err)
	}
	if sessionCount != 1 {
		t.Errorf("Expected 1 session after re-import, got %d", sessionCount)
	}
}

func TestDefaultSourcesIncludesPiWhenSessionDirExists(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	piDir := filepath.Join(home, ".pi", "agent", "sessions")
	if err := os.MkdirAll(piDir, 0755); err != nil {
		t.Fatal(err)
	}

	var found bool
	for _, src := range DefaultSources(false) {
		if src.Provider == pisessions.Provider {
			found = true
			if src.Path != piDir {
				t.Fatalf("Pi source path = %q, want %q", src.Path, piDir)
			}
			if src.ParseFn == nil {
				t.Fatal("Pi source ParseFn is nil")
			}
		}
	}
	if !found {
		t.Fatal("DefaultSources() did not include Pi source")
	}
}

func TestDefaultSourcesOmitsPiWhenSessionDirMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	for _, src := range DefaultSources(false) {
		if src.Provider == pisessions.Provider {
			t.Fatalf("DefaultSources() included Pi source without session dir: %#v", src)
		}
	}
}

func TestImportDirectory_PiSessionSearchable(t *testing.T) {
	tmpfile, err := os.CreateTemp("", "test-pi-*.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(tmpfile.Name()) }()
	_ = tmpfile.Close()

	database, err := db.New(tmpfile.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()

	imp := New(database)
	result, err := imp.ImportDirectory(context.Background(), "../../../pkg/pisessions/testdata", nil, false, false, pisessions.ParseFile, pisessions.Provider)
	if err != nil {
		t.Fatalf("ImportDirectory() error = %v", err)
	}
	if result.Skipped != 0 || result.Imported != 3 || len(result.Failures) != 0 {
		t.Fatalf("ImportDirectory() result = %+v, want 3 imported", result)
	}

	var provider, projectPath, version string
	if err := database.QueryRow(`SELECT provider, project_path FROM sessions WHERE session_id = ?`, "basic").Scan(&provider, &projectPath); err != nil {
		t.Fatal(err)
	}
	if provider != pisessions.Provider {
		t.Fatalf("provider = %q, want %q", provider, pisessions.Provider)
	}
	if projectPath != "/tmp/pi-demo" {
		t.Fatalf("project_path = %q, want /tmp/pi-demo", projectPath)
	}
	if err := database.QueryRow(`
		SELECT COALESCE(m.version, '')
		FROM messages m
		JOIN sessions s ON s.id = m.session_id
		WHERE s.session_id = ? AND m.sender = 'assistant'
		ORDER BY m.sequence
		LIMIT 1
	`, "basic").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != "openai-codex/gpt-5.5" {
		t.Fatalf("version = %q, want openai-codex/gpt-5.5", version)
	}

	results, err := search.SearchWithFilters(database, search.SearchFilters{Query: "frobnicator", Provider: pisessions.Provider})
	if err != nil {
		t.Fatalf("Pi provider search failed: %v", err)
	}
	if len(results) != 1 || results[0].Provider != pisessions.Provider {
		t.Fatalf("Pi provider search returned %#v", results)
	}

	codexResults, err := search.SearchWithFilters(database, search.SearchFilters{Query: "frobnicator", Provider: "codex"})
	if err != nil {
		t.Fatalf("Codex provider search failed: %v", err)
	}
	if len(codexResults) != 0 {
		t.Fatalf("Codex provider search returned Pi results: %#v", codexResults)
	}

	result, err = imp.ImportDirectory(context.Background(), "../../../pkg/pisessions/testdata", nil, false, false, pisessions.ParseFile, pisessions.Provider)
	if err != nil {
		t.Fatalf("ImportDirectory() second run error = %v", err)
	}
	if result.Skipped != 3 || result.Imported != 0 || len(result.Failures) != 0 {
		t.Fatalf("ImportDirectory() second result = %+v, want 3 skipped", result)
	}
}

func TestImportSession_ProviderStoredForClaude(t *testing.T) {
	tmpfile, err := os.CreateTemp("", "test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(tmpfile.Name()) }()
	_ = tmpfile.Close()

	database, err := db.New(tmpfile.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()

	imp := New(database)

	session, err := ccsessions.ParseFile("../../../pkg/ccsessions/testdata/sample.jsonl")
	if err != nil {
		t.Fatal(err)
	}

	inode, device, _ := getFileIdentity(session.FilePath)
	hash, err := computeFileHash(session.FilePath)
	if err != nil {
		t.Fatal(err)
	}

	err = imp.ImportSession(session, 0, inode, device, hash, "claude")
	if err != nil {
		t.Fatal(err)
	}

	var provider string
	err = database.QueryRow("SELECT provider FROM sessions").Scan(&provider)
	if err != nil {
		t.Fatal(err)
	}
	if provider != "claude" {
		t.Errorf("Expected provider 'claude', got %q", provider)
	}
}

// TestImportEnumeratedRefreshesEditedText verifies that when an enumerated
// provider (e.g. Copilot) rewrites a message's text in place — same UUID, new
// content — a re-sync updates both the stored text and the FTS index, rather
// than discarding the new text at the ON CONFLICT(uuid) clause.
func TestImportEnumeratedRefreshesEditedText(t *testing.T) {
	tmpfile, err := os.CreateTemp("", "test-edit-*.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(tmpfile.Name()) }()
	_ = tmpfile.Close()

	database, err := db.New(tmpfile.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()

	imp := New(database)

	makeSession := func(text string, mtime time.Time) *ccsessions.ParsedSession {
		return &ccsessions.ParsedSession{
			SessionID: "copilot-session",
			Summary:   "edit test",
			FilePath:  "/tmp/copilot/copilot-session.copilot",
			FileMtime: mtime,
			Messages: []ccsessions.ParsedMessage{{
				UUID:        "msg-stable-uuid",
				Type:        "assistant",
				Sender:      "assistant",
				TextContent: text,
				Timestamp:   mtime,
				Sequence:    1,
			}},
		}
	}

	// Initial import.
	t0 := time.Date(2026, 6, 3, 10, 0, 0, 0, time.UTC)
	if _, err := imp.ImportEnumerated(context.Background(), []*ccsessions.ParsedSession{makeSession("aardvark original", t0)}, nil, false, "copilot"); err != nil {
		t.Fatalf("initial import: %v", err)
	}

	textContent := func() string {
		var got string
		if err := database.QueryRow(`SELECT text_content FROM messages WHERE uuid = ?`, "msg-stable-uuid").Scan(&got); err != nil {
			t.Fatalf("query text: %v", err)
		}
		return got
	}
	ftsMatches := func(term string) int {
		var n int
		if err := database.QueryRow(`SELECT count(*) FROM messages_fts WHERE messages_fts MATCH ?`, term).Scan(&n); err != nil {
			t.Fatalf("query fts %q: %v", term, err)
		}
		return n
	}

	if got := textContent(); got != "aardvark original" {
		t.Fatalf("after initial import text = %q", got)
	}
	if ftsMatches("aardvark") != 1 || ftsMatches("beluga") != 0 {
		t.Fatalf("initial FTS state wrong: aardvark=%d beluga=%d", ftsMatches("aardvark"), ftsMatches("beluga"))
	}

	// Re-import the same UUID with rewritten text and a newer mtime (so the
	// session's change-detection hash differs and the import actually runs).
	t1 := t0.Add(time.Hour)
	if _, err := imp.ImportEnumerated(context.Background(), []*ccsessions.ParsedSession{makeSession("beluga rewritten", t1)}, nil, false, "copilot"); err != nil {
		t.Fatalf("re-import: %v", err)
	}

	if got := textContent(); got != "beluga rewritten" {
		t.Errorf("after re-import text = %q, want %q (edit was discarded)", got, "beluga rewritten")
	}
	if ftsMatches("beluga") != 1 {
		t.Errorf("FTS does not match new text 'beluga' after edit")
	}
	if ftsMatches("aardvark") != 0 {
		t.Errorf("FTS still matches stale text 'aardvark' after edit")
	}
}

func TestEnumeratedSessionHash(t *testing.T) {
	mk := func(mtime time.Time, msgs ...ccsessions.ParsedMessage) *ccsessions.ParsedSession {
		return &ccsessions.ParsedSession{SessionID: "s", FilePath: "/x/s.copilot", FileMtime: mtime, Messages: msgs}
	}
	m := func(uuid, text string) ccsessions.ParsedMessage {
		return ccsessions.ParsedMessage{UUID: uuid, TextContent: text}
	}
	t0 := time.Unix(1000, 0)
	t1 := time.Unix(2000, 0)

	base := enumeratedSessionHash("s", mk(t0, m("a", "hello"), m("b", "world")))

	// Same content, different mtime -> same hash (mtime is no longer an input).
	if h := enumeratedSessionHash("s", mk(t1, m("a", "hello"), m("b", "world"))); h != base {
		t.Errorf("hash changed on mtime-only change: %s vs %s", h, base)
	}
	// Edited text with a different length -> different hash.
	if h := enumeratedSessionHash("s", mk(t0, m("a", "hello!"), m("b", "world"))); h == base {
		t.Error("hash unchanged after a text edit")
	}
	// Same-length in-place edit ("world" -> "WORLD") -> different hash.
	if h := enumeratedSessionHash("s", mk(t0, m("a", "hello"), m("b", "WORLD"))); h == base {
		t.Error("hash unchanged after a same-length text edit")
	}
	// Added message -> different hash.
	if h := enumeratedSessionHash("s", mk(t0, m("a", "hello"), m("b", "world"), m("c", "!"))); h == base {
		t.Error("hash unchanged after adding a message")
	}
	// Changed UUID -> different hash.
	if h := enumeratedSessionHash("s", mk(t0, m("a", "hello"), m("z", "world"))); h == base {
		t.Error("hash unchanged after a uuid change")
	}
}

func TestImportEnumeratedUsesExplicitImportID(t *testing.T) {
	tmpfile, err := os.CreateTemp("", "test-antigravity-*.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(tmpfile.Name()) }()
	_ = tmpfile.Close()

	database, err := db.New(tmpfile.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()

	stamp := time.Date(2026, 7, 10, 20, 0, 0, 0, time.UTC)
	makeSession := func(id, project, text string) *ccsessions.ParsedSession {
		return &ccsessions.ParsedSession{
			SessionID:   id,
			ImportID:    id,
			ProjectPath: project,
			Summary:     text,
			FilePath:    "/tmp/antigravity/transcript.jsonl",
			FileMtime:   stamp,
			Messages: []ccsessions.ParsedMessage{{
				UUID:        ccsessions.DeterministicUUID("antigravity:" + id + ":0"),
				Type:        "user",
				Sender:      "human",
				TextContent: text,
				Timestamp:   stamp,
				Sequence:    1,
				CWD:         project,
			}},
		}
	}

	imp := New(database)
	sessions := []*ccsessions.ParsedSession{
		makeSession("first-conversation", "/tmp/project-one", "first prompt"),
		makeSession("second-conversation", "/tmp/project-two", "second prompt"),
	}
	if _, err := imp.ImportEnumerated(context.Background(), sessions, nil, false, "antigravity"); err != nil {
		t.Fatal(err)
	}

	rows, err := database.Query(`SELECT session_id, project_path FROM sessions ORDER BY session_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var got []string
	for rows.Next() {
		var id, project string
		if err := rows.Scan(&id, &project); err != nil {
			t.Fatal(err)
		}
		got = append(got, id+":"+project)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := []string{"first-conversation:/tmp/project-one", "second-conversation:/tmp/project-two"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("imported sessions = %v, want %v", got, want)
	}
}

// countingProgress records how many units each import reported, so tests can
// assert the bar advances for skipped files and not just imported ones.
type countingProgress struct {
	updates int
	skips   int
}

func (c *countingProgress) Update(string, string) { c.updates++ }
func (c *countingProgress) Skip()                 { c.skips++ }
func (c *countingProgress) Finish()               {}

// A file whose content is unchanged but whose mtime drifted (an in-place
// rewrite, a restore, a cloud-drive resync) must re-arm the cheap stat check
// after the hash proves it unchanged. Otherwise every later sync re-reads and
// re-hashes the whole file forever — brutal for multi-hundred-MB transcripts.
func TestImportDirectory_HashMatchRefreshesStat(t *testing.T) {
	tmpfile, err := os.CreateTemp("", "test-rearm-*.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(tmpfile.Name()) }()
	_ = tmpfile.Close()

	database, err := db.New(tmpfile.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()

	srcData, err := os.ReadFile("../../../pkg/ccsessions/testdata/sample.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	sessionFile := filepath.Join(dir, "sample.jsonl")
	if err := os.WriteFile(sessionFile, srcData, 0o600); err != nil {
		t.Fatal(err)
	}

	imp := New(database)
	if _, err := imp.ImportDirectory(context.Background(), dir, nil, false, true, ccsessions.ParseFile, "claude"); err != nil {
		t.Fatalf("ImportDirectory() error = %v", err)
	}

	// Touch the file without changing a byte of content.
	drifted := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(sessionFile, drifted, drifted); err != nil {
		t.Fatal(err)
	}

	progress := &countingProgress{}
	result, err := imp.ImportDirectory(context.Background(), dir, progress, false, true, ccsessions.ParseFile, "claude")
	if err != nil {
		t.Fatalf("ImportDirectory() second run error = %v", err)
	}
	if result.Skipped != 1 {
		t.Fatalf("second run skipped = %d, want 1 (content unchanged)", result.Skipped)
	}
	if progress.skips != 1 || progress.updates != 0 {
		t.Fatalf("second run progress = %d skips / %d updates, want 1/0", progress.skips, progress.updates)
	}

	var storedMtime time.Time
	if err := database.QueryRow(`SELECT file_mtime FROM sessions WHERE session_id = ?`, "sample").Scan(&storedMtime); err != nil {
		t.Fatal(err)
	}
	if diff := storedMtime.Sub(drifted); diff > time.Second || diff < -time.Second {
		t.Fatalf("stored file_mtime = %v, want ~%v (fast path never re-arms)", storedMtime, drifted)
	}
}

func TestImportRemoteFetchesOnlyNewOrChangedSessions(t *testing.T) {
	database, err := db.New(filepath.Join(t.TempDir(), "remote.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()

	imp := New(database)
	stamp := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	text := "first version"
	includeRemovedMessage := true
	fetches := 0
	failFetch := false
	fetch := func(_ context.Context, ref RemoteSessionRef) (*ccsessions.ParsedSession, error) {
		fetches++
		if failFetch || ref.ImportID == "T-bad" {
			return nil, errors.New("temporary export failure")
		}
		messages := []ccsessions.ParsedMessage{{
			UUID:        ccsessions.DeterministicUUID("amp:" + ref.ImportID + ":1"),
			Type:        "user",
			Sender:      "human",
			TextContent: text,
			Timestamp:   stamp,
			Sequence:    1,
			CWD:         "/tmp/project",
		}}
		if includeRemovedMessage {
			messages = append(messages, ccsessions.ParsedMessage{
				UUID:        ccsessions.DeterministicUUID("amp:" + ref.ImportID + ":removed"),
				Type:        "assistant",
				Sender:      "assistant",
				TextContent: "obsolete narwhal answer",
				Timestamp:   stamp.Add(time.Second),
				Sequence:    2,
				CWD:         "/tmp/project",
			})
		}
		return &ccsessions.ParsedSession{
			SessionID:   ref.ImportID,
			ImportID:    "wrong-export-id",
			ProjectPath: "/tmp/project",
			Summary:     "Amp thread",
			FilePath:    "amp://threads/" + ref.ImportID,
			FileMtime:   stamp,
			Messages:    messages,
		}, nil
	}

	refs := []RemoteSessionRef{
		{ImportID: "T-one", Revision: "revision-1"},
		{ImportID: "T-bad", Revision: "revision-1"},
	}
	result, err := imp.ImportRemote(context.Background(), refs, fetch, nil, false, "amp")
	if err != nil {
		t.Fatalf("initial ImportRemote() error = %v", err)
	}
	if result.Skipped != 0 || result.Imported != 1 || len(result.Failures) != 1 || fetches != 2 {
		t.Fatalf("initial ImportRemote() = %+v, fetches %d; want one import and one failure", result, fetches)
	}

	var provider, hash, storedText string
	if err := database.QueryRow(`SELECT provider, file_hash FROM sessions WHERE session_id = ?`, "T-one").Scan(&provider, &hash); err != nil {
		t.Fatal(err)
	}
	currentUUID := ccsessions.DeterministicUUID("amp:T-one:1")
	if err := database.QueryRow(`SELECT text_content FROM messages WHERE uuid = ?`, currentUUID).Scan(&storedText); err != nil {
		t.Fatal(err)
	}
	if provider != "amp" || hash != "revision-1" || storedText != "first version" {
		t.Fatalf("initial stored values = %q/%q/%q", provider, hash, storedText)
	}
	var badCount int
	if err := database.QueryRow(`SELECT COUNT(*) FROM sessions WHERE session_id = ?`, "T-bad").Scan(&badCount); err != nil {
		t.Fatal(err)
	}
	if badCount != 0 {
		t.Fatal("failed remote export created a partial session")
	}
	var obsoleteMatches int
	if err := database.QueryRow(`SELECT COUNT(*) FROM messages_fts WHERE messages_fts MATCH 'narwhal'`).Scan(&obsoleteMatches); err != nil {
		t.Fatal(err)
	}
	if obsoleteMatches != 1 {
		t.Fatalf("initial obsolete FTS matches = %d, want 1", obsoleteMatches)
	}
	var sessionDBID int64
	if err := database.QueryRow(`SELECT id FROM sessions WHERE session_id = ?`, "T-one").Scan(&sessionDBID); err != nil {
		t.Fatal(err)
	}
	derivedFixtures := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO session_summaries (session_id, one_line_summary) VALUES (?, ?)`, []any{sessionDBID, "stale summary"}},
		{`INSERT INTO summary_chunks (session_id, chunk_index, summary) VALUES (?, ?, ?)`, []any{sessionDBID, 0, "stale chunk"}},
		{`INSERT INTO session_issues (session_id, issue_id, issue_id_lower) VALUES (?, ?, ?)`, []any{sessionDBID, "OLD-1", "old-1"}},
		{`INSERT INTO session_files (session_id, file_path, file_name) VALUES (?, ?, ?)`, []any{sessionDBID, "old/file.go", "file.go"}},
		{`UPDATE sessions SET llm_summary = ?, llm_summary_at = CURRENT_TIMESTAMP WHERE id = ?`, []any{"stale legacy summary", sessionDBID}},
	}
	for _, fixture := range derivedFixtures {
		if _, err := database.Exec(fixture.query, fixture.args...); err != nil {
			t.Fatal(err)
		}
	}

	// The unchanged reference is skipped before Fetch is called.
	result, err = imp.ImportRemote(context.Background(), refs[:1], fetch, nil, false, "amp")
	if err != nil {
		t.Fatal(err)
	}
	if result.Skipped != 1 || fetches != 2 {
		t.Fatalf("unchanged ImportRemote() = %+v, fetches %d; want 1 skipped", result, fetches)
	}

	// A new revision fetches again and updates stable message UUIDs in place.
	text = "second version"
	includeRemovedMessage = false
	refs[0].Revision = "revision-2"
	if _, err := imp.ImportRemote(context.Background(), refs[:1], fetch, nil, false, "amp"); err != nil {
		t.Fatal(err)
	}
	if fetches != 3 {
		t.Fatalf("changed ImportRemote() fetches = %d, want 3", fetches)
	}
	if err := database.QueryRow(`SELECT file_hash FROM sessions WHERE session_id = ?`, "T-one").Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT text_content FROM messages WHERE uuid = ?`, currentUUID).Scan(&storedText); err != nil {
		t.Fatal(err)
	}
	if hash != "revision-2" || storedText != "second version" {
		t.Fatalf("changed stored values = %q/%q", hash, storedText)
	}
	var storedMessageCount, actualMessageCount int
	if err := database.QueryRow(`SELECT message_count FROM sessions WHERE session_id = ?`, "T-one").Scan(&storedMessageCount); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&actualMessageCount); err != nil {
		t.Fatal(err)
	}
	if storedMessageCount != 1 || actualMessageCount != 1 {
		t.Fatalf("message counts = stored %d, actual %d; want 1, 1", storedMessageCount, actualMessageCount)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM messages_fts WHERE messages_fts MATCH 'narwhal'`).Scan(&obsoleteMatches); err != nil {
		t.Fatal(err)
	}
	if obsoleteMatches != 0 {
		t.Fatalf("removed remote message remains in FTS: %d matches", obsoleteMatches)
	}
	for _, table := range []string{"session_summaries", "summary_chunks", "session_issues", "session_files"} {
		var count int
		if err := database.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE session_id = ?`, sessionDBID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("replaced transcript retained %d rows in %s", count, table)
		}
	}
	var legacySummary sql.NullString
	var legacySummaryAt sql.NullTime
	if err := database.QueryRow(`SELECT llm_summary, llm_summary_at FROM sessions WHERE id = ?`, sessionDBID).Scan(&legacySummary, &legacySummaryAt); err != nil {
		t.Fatal(err)
	}
	if legacySummary.Valid || legacySummaryAt.Valid {
		t.Fatalf("replaced transcript retained legacy summary: %v/%v", legacySummary, legacySummaryAt)
	}

	// A later fetch failure leaves the previously imported revision intact.
	failFetch = true
	refs[0].Revision = "revision-3"
	if _, err := imp.ImportRemote(context.Background(), refs[:1], fetch, nil, false, "amp"); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT file_hash FROM sessions WHERE session_id = ?`, "T-one").Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if hash != "revision-2" {
		t.Fatalf("failed fetch replaced cached revision with %q", hash)
	}

	// Force sync fetches an unchanged reference.
	failFetch = false
	refs[0].Revision = "revision-2"
	beforeForce := fetches
	if _, err := imp.ImportRemote(context.Background(), refs[:1], fetch, nil, true, "amp"); err != nil {
		t.Fatal(err)
	}
	if fetches != beforeForce+1 {
		t.Fatalf("forced ImportRemote() fetches = %d, want %d", fetches, beforeForce+1)
	}
}

func TestImportRemoteDoesNotDeriveProjectFromSyntheticPath(t *testing.T) {
	database, err := db.New(filepath.Join(t.TempDir(), "remote-project.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()

	stamp := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	imp := New(database)
	refs := []RemoteSessionRef{{ImportID: "T-no-tree", Revision: "revision-1"}}
	fetch := func(_ context.Context, ref RemoteSessionRef) (*ccsessions.ParsedSession, error) {
		return &ccsessions.ParsedSession{
			SessionID: ref.ImportID,
			ImportID:  ref.ImportID,
			FilePath:  "amp://threads/" + ref.ImportID,
			FileMtime: stamp,
			Messages: []ccsessions.ParsedMessage{{
				UUID:        ccsessions.DeterministicUUID("amp:" + ref.ImportID + ":1"),
				Type:        "user",
				Sender:      "human",
				TextContent: "No local tree metadata",
				Timestamp:   stamp,
				Sequence:    1,
			}},
		}, nil
	}
	if _, err := imp.ImportRemote(context.Background(), refs, fetch, nil, false, "amp"); err != nil {
		t.Fatal(err)
	}

	var projectPath string
	if err := database.QueryRow(`SELECT project_path FROM sessions WHERE session_id = ?`, "T-no-tree").Scan(&projectPath); err != nil {
		t.Fatal(err)
	}
	if projectPath != "" {
		t.Fatalf("project_path = %q, want empty path for remote session without local tree", projectPath)
	}
}
