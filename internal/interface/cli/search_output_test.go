package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/neilberkman/ccrider/internal/core/db"
)

// A session titled by a multi-line pasted first prompt must print as one
// title line, the same as `ccrider list`.
func TestSearchPrintsPastedFirstPromptOnOneLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "search-output.db")
	database, err := db.New(path)
	if err != nil {
		t.Fatal(err)
	}
	res, err := database.Exec(`
		INSERT INTO sessions (session_id, project_path, summary, created_at, updated_at)
		VALUES ('pasted', '/p', '', datetime('now'), datetime('now'))
	`)
	if err != nil {
		t.Fatal(err)
	}
	rowID, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		INSERT INTO messages (uuid, session_id, type, text_content, timestamp, sequence)
		VALUES ('pasted-msg', ?, 'user', ?, datetime('now'), 1)
	`, rowID, "\n\n<pasted_content>\nRefactor the parser\nso it streams\n</pasted_content>"); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	savedDBPath, savedStdout := dbPath, os.Stdout
	defer func() { dbPath, os.Stdout = savedDBPath, savedStdout }()
	dbPath = path
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	runErr := runSearch(searchCmd, []string{"parser"})
	_ = w.Close()
	os.Stdout = savedStdout
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if runErr != nil {
		t.Fatalf("runSearch() error = %v", runErr)
	}

	if !strings.Contains(string(out), "=== Session 1 ===\nRefactor the parser so it streams\nID: pasted\n") {
		t.Errorf("output = %q, want the title on one line between the header and the ID", out)
	}
}
