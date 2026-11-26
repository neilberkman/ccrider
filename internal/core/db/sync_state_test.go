package db

import (
	"path/filepath"
	"testing"
)

func TestSourceFingerprintRoundTrip(t *testing.T) {
	database, err := New(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()

	if fp, err := database.SourceFingerprint("antigravity", "/a"); err != nil || fp != "" {
		t.Fatalf("SourceFingerprint() before any record = %q, %v; want empty", fp, err)
	}
	for _, want := range []string{"v1", "v2"} {
		if err := database.SetSourceFingerprint("antigravity", "/a", want); err != nil {
			t.Fatal(err)
		}
		if fp, err := database.SourceFingerprint("antigravity", "/a"); err != nil || fp != want {
			t.Fatalf("SourceFingerprint() = %q, %v; want %q", fp, err, want)
		}
	}
	// Each data directory of a provider keeps its own fingerprint.
	if fp, _ := database.SourceFingerprint("antigravity", "/b"); fp != "" {
		t.Fatalf("SourceFingerprint(other path) = %q, want empty", fp)
	}
}
