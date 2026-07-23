package importer

import (
	"context"
	"errors"
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
		run: func(ctx context.Context, _ ...string) ([]byte, error) {
			deadline, ok := ctx.Deadline()
			if !ok || time.Until(deadline) > time.Second {
				t.Fatalf("command deadline = %v, ok = %v", deadline, ok)
			}
			return []byte("[]"), nil
		},
	}
	if _, err := client.listThreads(context.Background()); err != nil {
		t.Fatal(err)
	}
}
