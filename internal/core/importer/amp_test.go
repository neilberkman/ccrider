package importer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestAmpClientListThreadsPaginatesAndDeduplicates(t *testing.T) {
	var calls [][]string
	client := &ampClient{
		pageSize: 2,
		timeout:  time.Minute,
		run: func(_ context.Context, args ...string) ([]byte, error) {
			calls = append(calls, append([]string(nil), args...))
			switch args[len(args)-1] {
			case "0":
				return []byte(`[
					{"id":"T-one","updated":"2026-07-22T10:00:00Z","messageCount":2},
					{"id":"T-two","updated":"2026-07-22T11:00:00Z","messageCount":4}
				]`), nil
			case "2":
				return []byte(`[
					{"id":"T-two","updated":"2026-07-22T11:00:00Z","messageCount":4},
					{"id":"T-three","updated":"2026-07-22T12:00:00Z","messageCount":6}
				]`), nil
			case "4":
				return []byte(`[]`), nil
			default:
				t.Fatalf("unexpected offset %s", args[len(args)-1])
				return nil, nil
			}
		},
	}

	refs, err := client.listThreads(context.Background())
	if err != nil {
		t.Fatalf("listThreads() error = %v", err)
	}
	ids := make([]string, len(refs))
	for i, ref := range refs {
		ids[i] = ref.ImportID
	}
	if want := []string{"T-one", "T-two", "T-three"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("thread ids = %v, want %v", ids, want)
	}
	if len(calls) != 3 {
		t.Fatalf("amp calls = %d, want 3", len(calls))
	}
	if got := strings.Join(calls[0], " "); got != "threads list --json --include-archived --limit 2 --offset 0" {
		t.Fatalf("first amp call = %q", got)
	}
}

func TestAmpThreadRevisionChangesWithRemoteMetadata(t *testing.T) {
	messageCount := 2
	base := ampThreadListItem{ID: "T-one", Updated: "2026-07-22T10:00:00Z", MessageCount: &messageCount}
	baseRevision, err := base.revision()
	if err != nil {
		t.Fatal(err)
	}
	stableRevision, err := base.revision()
	if err != nil {
		t.Fatal(err)
	}
	if baseRevision != stableRevision {
		t.Fatal("revision is not stable")
	}
	changedTime := base
	changedTime.Updated = "2026-07-22T10:01:00Z"
	changedTimeRevision, err := changedTime.revision()
	if err != nil {
		t.Fatal(err)
	}
	if baseRevision == changedTimeRevision {
		t.Fatal("updated timestamp did not change revision")
	}
	changedCount := base
	changedMessageCount := *base.MessageCount + 1
	changedCount.MessageCount = &changedMessageCount
	changedCountRevision, err := changedCount.revision()
	if err != nil {
		t.Fatal(err)
	}
	if baseRevision == changedCountRevision {
		t.Fatal("message count did not change revision")
	}
}

func TestAmpClientListThreadsReturnsActionableErrors(t *testing.T) {
	tests := []struct {
		name    string
		run     ampRunFunc
		wantErr string
	}{
		{
			name: "command failure",
			run: func(context.Context, ...string) ([]byte, error) {
				return nil, errors.New("not authenticated")
			},
			wantErr: "list Amp threads: not authenticated",
		},
		{
			name: "invalid json",
			run: func(context.Context, ...string) ([]byte, error) {
				return []byte(`not-json`), nil
			},
			wantErr: "parse Amp thread list at offset 0",
		},
		{
			name: "missing id",
			run: func(context.Context, ...string) ([]byte, error) {
				return []byte(`[{}]`), nil
			},
			wantErr: "thread id is missing",
		},
		{
			name: "missing revision metadata",
			run: func(context.Context, ...string) ([]byte, error) {
				return []byte(`[{"id":"T-one","messageCount":1}]`), nil
			},
			wantErr: "has no updated timestamp",
		},
		{
			name: "missing message count",
			run: func(context.Context, ...string) ([]byte, error) {
				return []byte(`[{"id":"T-one","updated":"2026-07-22T10:00:00Z"}]`), nil
			},
			wantErr: "has no message count",
		},
		{
			name: "negative message count",
			run: func(context.Context, ...string) ([]byte, error) {
				return []byte(`[{"id":"T-one","updated":"2026-07-22T10:00:00Z","messageCount":-1}]`), nil
			},
			wantErr: "has invalid message count -1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &ampClient{run: tt.run, pageSize: 100, timeout: time.Minute}
			_, err := client.listThreads(context.Background())
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("listThreads() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestAmpClientExportThreadRejectsMismatchedID(t *testing.T) {
	client := &ampClient{
		pageSize: 100,
		timeout:  time.Minute,
		run: func(_ context.Context, args ...string) ([]byte, error) {
			if got := strings.Join(args, " "); got != "threads export T-requested" {
				t.Fatalf("amp args = %q", got)
			}
			return []byte(`{"id":"T-other","messages":[]}`), nil
		},
	}
	_, err := client.exportThread(context.Background(), "T-requested")
	if err == nil || !strings.Contains(err.Error(), `export contained thread id "T-other"`) {
		t.Fatalf("exportThread() error = %v", err)
	}
}

func TestAmpClientCommandContextDerivesFromCaller(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	client := &ampClient{
		pageSize: 100,
		timeout:  time.Minute,
		run: func(ctx context.Context, _ ...string) ([]byte, error) {
			cancel()
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	_, err := client.listThreads(parent)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("listThreads() error = %v, want context cancellation", err)
	}
}

func TestAmpClientAddsPerCommandDeadline(t *testing.T) {
	client := &ampClient{
		pageSize: 100,
		timeout:  time.Second,
		run: func(ctx context.Context, args ...string) ([]byte, error) {
			deadline, ok := ctx.Deadline()
			if !ok || time.Until(deadline) > time.Second {
				t.Fatalf("command deadline = %v, ok = %v", deadline, ok)
			}
			if len(args) >= 2 && args[1] == "export" {
				return []byte(`{"id":"T-one","messages":[]}`), nil
			}
			return []byte("[]"), nil
		},
	}
	if _, err := client.listThreads(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := client.exportThread(context.Background(), "T-one"); err != nil {
		t.Fatal(err)
	}
}

func TestRunAmpPreservesCommandStderr(t *testing.T) {
	binDir := t.TempDir()
	ampPath := filepath.Join(binDir, "amp")
	if err := os.WriteFile(ampPath, []byte("#!/bin/sh\necho 'authentication required' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)

	_, err := runAmp(context.Background(), "threads", "list")
	if err == nil || !strings.Contains(err.Error(), "authentication required") {
		t.Fatalf("runAmp() error = %v, want command stderr", err)
	}
}

func TestRunAmpPreservesDeadline(t *testing.T) {
	binDir := t.TempDir()
	ampPath := filepath.Join(binDir, "amp")
	if err := os.WriteFile(ampPath, []byte("#!/bin/sh\necho 'export stalled' >&2\nexec /bin/sleep 10\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	_, err := runAmp(ctx, "threads", "export", "T-one")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("runAmp() error = %v, want deadline", err)
	}
}

func TestRunAmpUsesOutputAfterCleanExitWaitDelay(t *testing.T) {
	binDir := t.TempDir()
	ampPath := filepath.Join(binDir, "amp")
	if err := os.WriteFile(ampPath, []byte("#!/bin/sh\n(sleep 5) &\nprintf '[]'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	started := time.Now()
	output, err := runAmpWithWaitDelay(context.Background(), 20*time.Millisecond, "threads", "list")
	if err != nil {
		t.Fatalf("runAmpWithWaitDelay() error = %v, want clean output", err)
	}
	if string(output) != "[]" {
		t.Fatalf("output = %q, want []", output)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("clean process with inherited pipe returned after %s, want bounded wait", elapsed)
	}
}

func TestRunAmpCallerDeadlineTakesPrecedenceOverCleanExitWaitDelay(t *testing.T) {
	binDir := t.TempDir()
	ampPath := filepath.Join(binDir, "amp")
	if err := os.WriteFile(ampPath, []byte("#!/bin/sh\n(sleep 5) &\nprintf '[]'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := runAmpWithWaitDelay(ctx, time.Second, "threads", "list")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("runAmpWithWaitDelay() error = %v, want caller deadline", err)
	}
}
