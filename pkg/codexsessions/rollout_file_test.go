package codexsessions

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func writeCompressedRollout(t *testing.T, path string, data []byte) {
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

func TestCompressedRolloutMatchesPlain(t *testing.T) {
	for _, fixture := range []string{"sample.jsonl", "response_item_only.jsonl", "subagent_v2.jsonl"} {
		t.Run(fixture, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", fixture))
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), fixture+".zst")
			writeCompressedRollout(t, path, data)
			plain, err := ParseFile(filepath.Join("testdata", fixture))
			if err != nil {
				t.Fatal(err)
			}
			compressed, err := ParseFile(path)
			if err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if compressed.FilePath != path || compressed.FileSize != info.Size() || !compressed.FileMtime.Equal(info.ModTime()) {
				t.Fatalf("compressed rollout must retain its on-disk metadata: %+v", compressed)
			}
			compressed.FilePath, compressed.FileSize, compressed.FileMtime = plain.FilePath, plain.FileSize, plain.FileMtime
			if !reflect.DeepEqual(compressed, plain) {
				t.Fatalf("compressed parse differs from plain: %+v vs %+v", compressed, plain)
			}
			via, err := ReadResumeVia(path)
			if err != nil || via != plain.ResumeVia {
				t.Fatalf("ReadResumeVia() = %q, %v; want %q", via, err, plain.ResumeVia)
			}
		})
	}
}

func TestCompressedRolloutFallbackSessionID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fallback.jsonl.zst")
	writeCompressedRollout(t, path, []byte(`{"timestamp":"2026-10-27T00:00:00Z","type":"event_msg","payload":{"type":"user_message","message":"hello"}}`+"\n"))
	parsed, err := ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.SessionID != "fallback" {
		t.Fatalf("SessionID = %q, want fallback", parsed.SessionID)
	}
}

func TestCompressedRolloutReportsCorruption(t *testing.T) {
	encoder, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := encoder.Close(); err != nil {
			t.Error(err)
		}
	}()
	data, err := os.ReadFile("testdata/sample.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	compressed := encoder.EncodeAll(data, nil)
	for name, content := range map[string][]byte{
		"invalid":   []byte("not zstd"),
		"truncated": compressed[:len(compressed)-4],
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "bad.jsonl.zst")
			if err := os.WriteFile(path, content, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := ParseFile(path); err == nil {
				t.Fatal("corrupt rollout parsed without an error")
			}
		})
	}
}
