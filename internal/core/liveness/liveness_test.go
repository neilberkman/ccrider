package liveness

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/neilberkman/ccrider/internal/core/db"
)

type fakeSource struct{ procs []Process }

func (f fakeSource) Processes(context.Context) ([]Process, error) { return f.procs, nil }

func newLivenessTestDB(t *testing.T) *db.DB {
	t.Helper()
	tmpfile, err := os.CreateTemp("", "test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(tmpfile.Name()) })
	_ = tmpfile.Close()

	database, err := db.New(tmpfile.Name())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	seed := []struct {
		id, project, provider, created string
	}{
		{"8650b828-b3a9-4db4-81c0-64d0fee6dd70", "/Users/neil/xuku/ccrider", "claude", "2026-08-10 09:00:00"},
		{"rollout-2026-07-24T08-08-11-019f94ab-7288-7043-b7ef-5e5b852ed3a5", "/Users/neil/xuku/sidereon", "codex", "2026-07-24 08:08:11"},
		{"11111111-2222-4333-8444-555555555555", "/Users/neil/xuku/ccrider", "claude", "2026-08-12 12:00:00"},
	}
	for _, s := range seed {
		if _, err := database.Exec(`
			INSERT INTO sessions (session_id, project_path, summary, provider, created_at, updated_at)
			VALUES (?, ?, 'seeded session', ?, ?, ?)
		`, s.id, s.project, s.provider, s.created, s.created); err != nil {
			t.Fatal(err)
		}
	}
	return database
}

func TestScanMatchesArgvTier(t *testing.T) {
	database := newLivenessTestDB(t)
	src := fakeSource{procs: []Process{
		{
			PID:       101,
			Argv:      []string{"claude", "--resume", "8650b828-b3a9-4db4-81c0-64d0fee6dd70"},
			Cwd:       "/somewhere/else",
			TTY:       "ttys004",
			StartedAt: time.Now().Add(-time.Hour),
		},
		{
			// bare UUID in argv resolves to the rollout id via core resolution
			PID:       102,
			Argv:      []string{"codex", "resume", "019f94ab-7288-7043-b7ef-5e5b852ed3a5"},
			StartedAt: time.Now().Add(-time.Hour),
		},
	}}

	live, err := Scan(context.Background(), src, database)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if len(live) != 2 {
		t.Fatalf("got %d live sessions, want 2", len(live))
	}
	byPID := map[int32]LiveSession{}
	for _, l := range live {
		byPID[l.PID] = l
	}
	if got := byPID[101]; got.Match != MatchArgv || got.SessionID != "8650b828-b3a9-4db4-81c0-64d0fee6dd70" || got.ProjectPath != "/Users/neil/xuku/ccrider" {
		t.Errorf("claude row = %+v", got)
	}
	if got := byPID[102]; got.Match != MatchArgv || got.SessionID != "rollout-2026-07-24T08-08-11-019f94ab-7288-7043-b7ef-5e5b852ed3a5" {
		t.Errorf("codex row = %+v", got)
	}
}

func TestScanMatchesCwdTier(t *testing.T) {
	database := newLivenessTestDB(t)
	started, _ := time.Parse("2006-01-02 15:04:05", "2026-08-12 11:59:00")
	src := fakeSource{procs: []Process{
		{
			// fresh claude session (no --resume) in a subdirectory of the project
			PID:       201,
			Argv:      []string{"claude", "--dangerously-skip-permissions"},
			Cwd:       "/Users/neil/xuku/ccrider/internal/core",
			StartedAt: started,
		},
	}}

	live, err := Scan(context.Background(), src, database)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if len(live) != 1 {
		t.Fatalf("got %d live sessions, want 1", len(live))
	}
	got := live[0]
	if got.Match != MatchCwd {
		t.Fatalf("match = %q, want cwd; row %+v", got.Match, got)
	}
	// Must pick the session created after process start (11111111-...),
	// not the older one in the same project.
	if got.SessionID != "11111111-2222-4333-8444-555555555555" {
		t.Errorf("session = %q, want the post-start session", got.SessionID)
	}
}

func TestScanUnknownWhenNothingMatches(t *testing.T) {
	database := newLivenessTestDB(t)
	src := fakeSource{procs: []Process{
		{
			PID:       301,
			Argv:      []string{"claude"},
			Cwd:       "/tmp/never-a-project",
			StartedAt: time.Now(),
		},
	}}

	live, err := Scan(context.Background(), src, database)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if len(live) != 1 {
		t.Fatalf("got %d live sessions, want 1", len(live))
	}
	if got := live[0]; got.Match != MatchUnknown || got.SessionID != "" || got.ProjectPath != "/tmp/never-a-project" {
		t.Errorf("row = %+v, want unknown match keeping cwd", got)
	}
}

func TestGroupByProjectOrdersByRecency(t *testing.T) {
	now := time.Now()
	live := []LiveSession{
		{ProjectPath: "/a", LastActivity: now.Add(-72 * time.Hour)},
		{ProjectPath: "/b", LastActivity: now.Add(-time.Hour)},
		{ProjectPath: "/a", LastActivity: now.Add(-96 * time.Hour)},
	}
	groups := GroupByProject(live)
	if len(groups) != 2 {
		t.Fatalf("got %d groups, want 2", len(groups))
	}
	if groups[0].ProjectPath != "/b" {
		t.Errorf("first group = %q, want most recently active (/b)", groups[0].ProjectPath)
	}
	if len(groups[1].Sessions) != 2 {
		t.Errorf("group /a has %d sessions, want 2", len(groups[1].Sessions))
	}
}

func TestScanDeclaredBeatsArgv(t *testing.T) {
	database := newLivenessTestDB(t)
	src := fakeSource{procs: []Process{
		{
			// launched with --resume, then /clear moved it to another session
			PID:       401,
			Argv:      []string{"claude", "--resume", "8650b828-b3a9-4db4-81c0-64d0fee6dd70"},
			StartedAt: time.Now().Add(-time.Hour),
			Declared:  []string{"11111111-2222-4333-8444-555555555555"},
		},
	}}

	live, err := Scan(context.Background(), src, database)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if got := live[0]; got.Match != MatchDeclared || got.SessionID != "11111111-2222-4333-8444-555555555555" {
		t.Errorf("row = %+v, want declared session over argv", got)
	}
}

func TestScanCreditsHelperDeclarationsToListedParent(t *testing.T) {
	database := newLivenessTestDB(t)
	src := fakeSource{procs: []Process{
		{PID: 501, Argv: []string{"node", "/opt/homebrew/bin/codex"}, Cwd: "/tmp/elsewhere"},
		{
			// the native child holds the thread lock; only the wrapper is listed
			PID:      502,
			PPID:     501,
			Argv:     []string{"/opt/homebrew/lib/node_modules/@openai/codex/vendor/codex/codex"},
			Declared: []string{"019f94ab-7288-7043-b7ef-5e5b852ed3a5"},
		},
	}}

	live, err := Scan(context.Background(), src, database)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if len(live) != 1 {
		t.Fatalf("got %d live sessions, want 1 (helper folded into parent)", len(live))
	}
	if got := live[0]; got.PID != 501 || got.Match != MatchDeclared || got.SessionID != "rollout-2026-07-24T08-08-11-019f94ab-7288-7043-b7ef-5e5b852ed3a5" {
		t.Errorf("row = %+v, want wrapper carrying the child's declared thread", got)
	}
}

func TestScanPicksMostRecentDeclaredKnownSession(t *testing.T) {
	database := newLivenessTestDB(t)
	if _, err := database.Exec(`UPDATE sessions SET updated_at = '2026-08-20 10:00:00' WHERE session_id = '8650b828-b3a9-4db4-81c0-64d0fee6dd70'`); err != nil {
		t.Fatal(err)
	}
	src := fakeSource{procs: []Process{
		{
			PID:  601,
			Argv: []string{"codex"},
			// a sub-thread, the active thread, and one not imported yet
			Declared: []string{
				"11111111-2222-4333-8444-555555555555",
				"8650b828-b3a9-4db4-81c0-64d0fee6dd70",
				"99999999-2222-4333-8444-555555555555",
			},
		},
	}}

	live, err := Scan(context.Background(), src, database)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if got := live[0]; got.Match != MatchDeclared || got.SessionID != "8650b828-b3a9-4db4-81c0-64d0fee6dd70" {
		t.Errorf("row = %+v, want the most recently updated known session", got)
	}
}

func TestScanCwdTierIsOneToOne(t *testing.T) {
	database := newLivenessTestDB(t)
	// Three fresh claude processes in one project. The newest owns the newest
	// session; the middle one owns the older session; the oldest process's
	// session was never imported and must not borrow either.
	at := func(v string) time.Time {
		ts, _ := time.Parse("2006-01-02 15:04:05", v)
		return ts
	}
	src := fakeSource{procs: []Process{
		{PID: 701, Argv: []string{"claude"}, Cwd: "/Users/neil/xuku/ccrider", StartedAt: at("2026-08-01 08:00:00")},
		{PID: 702, Argv: []string{"claude"}, Cwd: "/Users/neil/xuku/ccrider", StartedAt: at("2026-08-10 08:59:00")},
		{PID: 703, Argv: []string{"claude"}, Cwd: "/Users/neil/xuku/ccrider", StartedAt: at("2026-08-12 11:59:00")},
	}}

	live, err := Scan(context.Background(), src, database)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	byPID := map[int32]LiveSession{}
	for _, l := range live {
		byPID[l.PID] = l
	}
	if got := byPID[703]; got.Match != MatchCwd || got.SessionID != "11111111-2222-4333-8444-555555555555" {
		t.Errorf("newest process = %+v", got)
	}
	if got := byPID[702]; got.Match != MatchCwd || got.SessionID != "8650b828-b3a9-4db4-81c0-64d0fee6dd70" {
		t.Errorf("middle process = %+v, want the session started soonest after it", got)
	}
	if got := byPID[701]; got.Match != MatchUnknown {
		t.Errorf("oldest process = %+v, want unknown rather than a session another process holds", got)
	}
}

func TestScanCwdTierSkipsSessionsHeldExactly(t *testing.T) {
	database := newLivenessTestDB(t)
	started, _ := time.Parse("2006-01-02 15:04:05", "2026-08-12 11:59:00")
	src := fakeSource{procs: []Process{
		{PID: 801, Argv: []string{"claude"}, Cwd: "/Users/neil/xuku/ccrider", StartedAt: started},
		{PID: 802, Argv: []string{"claude"}, Declared: []string{"11111111-2222-4333-8444-555555555555"}},
	}}

	live, err := Scan(context.Background(), src, database)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	for _, l := range live {
		if l.PID == 801 && l.Match != MatchUnknown {
			t.Errorf("cwd row = %+v, want unknown: its only candidate is declared by 802", l)
		}
	}
}
