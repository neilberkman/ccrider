package importer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/neilberkman/ccrider/internal/core/db"
	"github.com/neilberkman/ccrider/pkg/codexsessions"
)

func writeCodexArchive(t *testing.T, path string, data []byte) {
	t.Helper()
	encoder, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := encoder.Close(); err != nil {
			t.Error(err)
		}
	}()
	if err := os.WriteFile(path, encoder.EncodeAll(data, nil), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCodexCompressedSyncLifecycle(t *testing.T) {
	database, err := db.New(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	imp := New(database)
	dir := t.TempDir()
	fixture, err := os.ReadFile("../../../pkg/codexsessions/testdata/sample.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	plain := filepath.Join(dir, "rollout-sample.jsonl")
	archive := plain + ".zst"
	writeCodexArchive(t, archive, fixture)
	source := Source{Path: dir, Provider: "codex", ParseFn: codexsessions.ParseFile, ResumeViaFn: codexsessions.ReadResumeVia}
	sync := func(wantImported int) {
		t.Helper()
		prepared, err := imp.PrepareSource(context.Background(), source, false)
		if err != nil {
			t.Fatal(err)
		}
		if prepared.Total != 1 {
			t.Fatalf("Total = %d, want one rollout", prepared.Total)
		}
		result, err := prepared.Run(context.Background(), nil)
		if err != nil || len(result.Failures) != 0 {
			t.Fatalf("Run() = %+v, %v", result, err)
		}
		if result.Imported != wantImported {
			t.Fatalf("Imported = %d, want %d", result.Imported, wantImported)
		}
		var sessions, messages int
		if err := database.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&sessions); err != nil {
			t.Fatal(err)
		}
		if err := database.QueryRow("SELECT COUNT(*) FROM messages").Scan(&messages); err != nil {
			t.Fatal(err)
		}
		if sessions != 1 || messages != 4 {
			t.Fatalf("got %d sessions and %d messages, want 1 and 4", sessions, messages)
		}
	}
	sync(1) // Fresh database with compressed history only.
	sync(0) // Repeat sync preserves message IDs.
	if err := os.WriteFile(plain, fixture, 0o600); err != nil {
		t.Fatal(err)
	}
	// A broken stale archive must not be read while the plain sibling exists.
	if err := os.WriteFile(archive, []byte("invalid zstd"), 0o600); err != nil {
		t.Fatal(err)
	}
	sync(1)
	var filePath string
	if err := database.QueryRow("SELECT file_path FROM import_log ORDER BY id DESC LIMIT 1").Scan(&filePath); err != nil {
		t.Fatal(err)
	}
	if filePath != plain {
		t.Fatalf("file_path = %q, want plain sibling", filePath)
	}
	writeCodexArchive(t, archive, fixture)
	if err := os.Remove(plain); err != nil {
		t.Fatal(err)
	}
	sync(1) // Compressing a previously imported plain file keeps one session.
}

func TestCompressedRolloutsAreCodexOnly(t *testing.T) {
	dir := t.TempDir()
	writeCodexArchive(t, filepath.Join(dir, "archived.jsonl.zst"), []byte("{}\n"))
	for _, provider := range []string{"claude", "pi", "codex"} {
		prepared, err := New(nil).PrepareSource(context.Background(), Source{Path: dir, Provider: provider}, false)
		if err != nil {
			t.Fatal(err)
		}
		want := 0
		if provider == "codex" {
			want = 1
		}
		if prepared.Total != want {
			t.Errorf("%s Total = %d, want %d", provider, prepared.Total, want)
		}
	}
}

func TestBackfillResumeViaFromCompressedRollout(t *testing.T) {
	database, err := db.New(filepath.Join(t.TempDir(), "backfill.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	dir := t.TempDir()
	fixture, err := os.ReadFile("../../../pkg/codexsessions/testdata/subagent_v2.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	writeCodexArchive(t, filepath.Join(dir, "child.jsonl.zst"), fixture)
	if _, err := database.Exec(`INSERT INTO sessions (session_id, project_path, provider, resume_via) VALUES ('child', '/p', 'codex', NULL)`); err != nil {
		t.Fatal(err)
	}
	src := Source{Path: dir, Provider: "codex", ResumeViaFn: codexsessions.ReadResumeVia}
	if err := New(database).BackfillResumeVia(context.Background(), src); err != nil {
		t.Fatal(err)
	}
	var via string
	if err := database.QueryRow("SELECT resume_via FROM sessions WHERE session_id = 'child'").Scan(&via); err != nil {
		t.Fatal(err)
	}
	if via != "0199a000-0000-7000-8000-000000000001" {
		t.Fatalf("resume_via = %q, want root thread", via)
	}
}
