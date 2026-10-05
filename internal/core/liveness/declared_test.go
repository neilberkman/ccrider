package liveness

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func writeRegistry(t *testing.T, dir string, pid int32, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(dir, "sessions", strconv.Itoa(int(pid))+".json")
	if err := os.WriteFile(name, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestClaudeRegistryReadsEveryConfigDir(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	writeRegistry(t, a, 10, `{"pid":10,"sessionId":"aaaaaaaa-0000-4000-8000-000000000000","startedAt":1000}`)
	writeRegistry(t, b, 20, `{"pid":20,"sessionId":"bbbbbbbb-0000-4000-8000-000000000000","startedAt":2000}`)
	writeRegistry(t, b, 30, `{"pid":31,"sessionId":"cccccccc-0000-4000-8000-000000000000"}`) // name and pid disagree
	writeRegistry(t, b, 40, `not json`)

	entries := claudeRegistry([]string{a, b})
	got := map[int32]string{}
	for _, e := range entries {
		got[e.PID] = e.SessionID
	}
	if len(got) != 2 || got[10] == "" || got[20] == "" {
		t.Errorf("entries = %+v, want pids 10 and 20 only", entries)
	}
}

func TestRegistryEntryRejectsReusedPID(t *testing.T) {
	started := time.UnixMilli(1_790_000_000_000)
	entry := claudeRegistryEntry{PID: 1, SessionID: "x", StartedAt: started.Add(2 * time.Second).UnixMilli()}
	if !entry.belongsTo(started) {
		t.Error("entry stamped 2s after process start should belong to it")
	}
	if entry.belongsTo(started.Add(-time.Hour)) {
		t.Error("entry from a process started an hour later must not belong to this one")
	}
}

func TestAttachClaudeRegistryCreditsChildToParentRow(t *testing.T) {
	// This test process stands in for a background session binary whose
	// parent (the test runner) is the listed pty host.
	self := int32(os.Getpid())
	parent := int32(os.Getppid())
	out := []Process{{PID: parent, Argv: []string{"claude", "--bg-pty-host", "/tmp/x.sock"}}}
	entries := []claudeRegistryEntry{{PID: self, SessionID: "dddddddd-0000-4000-8000-000000000000"}}

	attachClaudeRegistry(context.Background(), out, entries)
	if len(out[0].Declared) != 1 || out[0].Declared[0] != "dddddddd-0000-4000-8000-000000000000" {
		t.Errorf("parent row declared = %v, want the child's session", out[0].Declared)
	}
}

func TestCodexThreadID(t *testing.T) {
	cases := map[string]string{
		"/h/.codex/thread-writer-locks/01a0de95-c62a-7532-8ad6-a9ddf1514baf.lock": "01a0de95-c62a-7532-8ad6-a9ddf1514baf",
		"/h/.codex/thread-writer-locks/.coordination.lock":                        "",
		"/h/.codex/thread-writer-locks/01a0de95-c62a-7532-8ad6-a9ddf1514baf":      "",
	}
	for path, want := range cases {
		if got := codexThreadID(path); got != want {
			t.Errorf("codexThreadID(%q) = %q, want %q", path, got, want)
		}
	}
}
