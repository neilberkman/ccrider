// Package liveness discovers which coding agent sessions currently have a
// live process attached, and matches those processes back to sessions in the
// ccrider database. Detection is tiered: a session id the process itself
// declares on disk (Claude Code's per-PID session registry, Codex's
// thread-writer locks) is exact and current; a session id recovered from the
// command line is exact for the session the process was launched with;
// otherwise the process working directory is matched against session project
// paths, each session going to at most one process; a provider process
// matching nothing is still reported, as unknown.
package liveness

import (
	"context"
	"path/filepath"
	"sort"
	"time"

	"github.com/neilberkman/ccrider/internal/core/db"
	"github.com/neilberkman/ccrider/internal/core/session"
)

// Match confidence for one live session row.
const (
	MatchDeclared = "declared" // the process declared its current session on disk
	MatchArgv     = "argv"     // session id was present in the process command line
	MatchCwd      = "cwd"      // matched via process working directory + timing
	MatchUnknown  = "none"     // provider process with no matching session
)

// Process is one row from a process table scan. Sources fill what the
// platform provides; TTY may be empty (Windows has none).
type Process struct {
	PID       int32
	PPID      int32
	Argv      []string
	Cwd       string
	TTY       string
	StartedAt time.Time
	// Declared holds session ids the process itself has recorded as its own
	// (bare UUIDs or full ids). Codex holds several when it has spawned
	// sub-threads; Scan picks the most recently active one it knows.
	Declared []string
}

// Source enumerates candidate processes. The production implementation scans
// the host process table; tests inject fixed slices.
type Source interface {
	Processes(ctx context.Context) ([]Process, error)
}

// LiveSession is one agent process paired with what ccrider knows about the
// session it hosts.
type LiveSession struct {
	Provider     string
	PID          int32
	TTY          string
	SessionID    string // canonical id; "" when Match is MatchUnknown
	Summary      string
	ProjectPath  string // session project path, or process cwd for unknowns
	StartedAt    time.Time
	LastActivity time.Time // session updated_at; zero when unknown
	Match        string
}

// IdleFor returns how long the session has been without activity as of now.
// Sessions with unknown activity report the process age instead.
func (l LiveSession) IdleFor(now time.Time) time.Duration {
	ref := l.LastActivity
	if ref.IsZero() {
		ref = l.StartedAt
	}
	if ref.IsZero() || ref.After(now) {
		return 0
	}
	return now.Sub(ref)
}

// cwdCreatedSlack tolerates session records stamped slightly before their
// process's recorded start time (clock granularity, exec ordering).
const cwdCreatedSlack = 2 * time.Minute

// Scan enumerates live agent processes and matches each to a session.
// Results are sorted by project path, then by most recent activity.
func Scan(ctx context.Context, src Source, database *db.DB) ([]LiveSession, error) {
	procs, err := src.Processes(ctx)
	if err != nil {
		return nil, err
	}

	// Agent CLIs fork helper copies of themselves (and node wrappers exec
	// native children); listing both parent and child would double-count one
	// window. A process whose parent is also a candidate is the helper. What
	// a helper declares belongs to the window it serves: the Codex node
	// wrapper is the listed process, but its native child holds the locks.
	byPID := make(map[int32]*Process, len(procs))
	for i := range procs {
		byPID[procs[i].PID] = &procs[i]
	}
	top := func(p *Process) *Process {
		for seen := 0; p.PPID != 0 && seen < len(procs); seen++ {
			parent, ok := byPID[p.PPID]
			if !ok {
				break
			}
			p = parent
		}
		return p
	}
	declared := make(map[int32][]string)
	for i := range procs {
		if len(procs[i].Declared) > 0 {
			root := top(&procs[i]).PID
			declared[root] = append(declared[root], procs[i].Declared...)
		}
	}

	var live []LiveSession
	claimed := make(map[string]bool)
	for _, proc := range procs {
		if proc.PPID != 0 && byPID[proc.PPID] != nil {
			continue
		}
		match, ok := session.MatchLiveProcess(proc.Argv)
		if !ok {
			continue
		}
		row := LiveSession{
			Provider:    match.Provider,
			PID:         proc.PID,
			TTY:         proc.TTY,
			ProjectPath: proc.Cwd,
			StartedAt:   proc.StartedAt,
			Match:       MatchUnknown,
		}

		// The declared session beats argv: a process launched with --resume
		// keeps that id in its command line after /clear or an in-session
		// resume moves it to another session.
		if info := mostRecentKnown(database, declared[proc.PID]); info != nil {
			row.setSession(info, MatchDeclared)
		} else if match.SessionID != "" {
			if info := lookupSession(database, match.SessionID); info != nil {
				row.setSession(info, MatchArgv)
			}
		}
		if row.SessionID != "" {
			claimed[row.SessionID] = true
		}
		live = append(live, row)
	}

	matchUnclaimedByCwd(database, live, claimed)

	sort.Slice(live, func(i, j int) bool {
		if live[i].ProjectPath != live[j].ProjectPath {
			return live[i].ProjectPath < live[j].ProjectPath
		}
		return live[i].LastActivity.After(live[j].LastActivity)
	})
	return live, nil
}

func (l *LiveSession) setSession(info *db.Session, match string) {
	l.SessionID = info.SessionID
	l.Summary = info.Summary
	l.ProjectPath = info.ProjectPath
	l.LastActivity = info.UpdatedAt
	l.Match = match
}

// lookupSession resolves an argv-recovered id (which may be a bare UUID) to
// its session record. A dangling id — process alive, session not imported
// yet — yields nil and the row stays unknown rather than erroring the scan.
func lookupSession(database *db.DB, id string) *db.Session {
	info, err := database.GetSessionOverview(id)
	if err != nil {
		return nil
	}
	return info
}

// mostRecentKnown resolves declared ids and returns the most recently
// updated one present in the database, or nil when none is imported yet.
func mostRecentKnown(database *db.DB, ids []string) *db.Session {
	var best *db.Session
	for _, id := range ids {
		info := lookupSession(database, id)
		if info != nil && (best == nil || info.UpdatedAt.After(best.UpdatedAt)) {
			best = info
		}
	}
	return best
}

// cwdCandidateLimit bounds the per-directory session read for the cwd tier.
// Sessions come back most recently updated first, and a session created after
// a process started was also updated after it, so the window holds every
// candidate unless that many sessions in one directory changed since.
const cwdCandidateLimit = 200

// matchUnclaimedByCwd fills rows still unknown after the exact tiers. Each
// fresh (non-resumed) process gets the earliest session of its provider
// created at or after it started, in its working directory or one of its
// ancestors (walking up covers agents launched in a subdirectory of the
// recorded project path). Sessions already held by a process are skipped and
// each session goes to at most one process; newer processes choose first, so
// a long-idle process whose own session is missing cannot take a newer
// process's session.
func matchUnclaimedByCwd(database *db.DB, live []LiveSession, claimed map[string]bool) {
	var pending []int
	for i := range live {
		if live[i].Match == MatchUnknown && live[i].ProjectPath != "" {
			pending = append(pending, i)
		}
	}
	sort.SliceStable(pending, func(a, b int) bool {
		return live[pending[a]].StartedAt.After(live[pending[b]].StartedAt)
	})

	candidates := make(map[string][]db.Session)
	sessionsIn := func(dir string) []db.Session {
		if s, ok := candidates[dir]; ok {
			return s
		}
		s, err := database.SessionsForProjectPath(dir, cwdCandidateLimit)
		if err != nil {
			s = nil
		}
		candidates[dir] = s
		return s
	}

	const maxAncestors = 5
	for _, i := range pending {
		row := &live[i]
		dir := row.ProjectPath
		for range maxAncestors {
			var best *db.Session
			for _, s := range sessionsIn(dir) {
				if s.Provider != row.Provider || claimed[s.SessionID] {
					continue
				}
				if !row.StartedAt.IsZero() && s.CreatedAt.Before(row.StartedAt.Add(-cwdCreatedSlack)) {
					continue
				}
				if best == nil || s.CreatedAt.Before(best.CreatedAt) {
					s := s
					best = &s
				}
			}
			if best != nil {
				row.setSession(best, MatchCwd)
				claimed[best.SessionID] = true
				break
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
}

// Group is a set of live sessions sharing a project path, ordered as Scan
// returns them. Groups are sorted by most recent activity across members.
type Group struct {
	ProjectPath string
	Sessions    []LiveSession
}

// GroupByProject buckets live sessions by project path. Every interface
// (CLI, TUI, MCP) shows the same grouping, so it lives in core.
func GroupByProject(live []LiveSession) []Group {
	index := make(map[string]int)
	var groups []Group
	for _, l := range live {
		key := l.ProjectPath
		if key == "" {
			key = "(unknown directory)"
		}
		i, ok := index[key]
		if !ok {
			i = len(groups)
			index[key] = i
			groups = append(groups, Group{ProjectPath: key})
		}
		groups[i].Sessions = append(groups[i].Sessions, l)
	}
	sort.SliceStable(groups, func(a, b int) bool {
		return latestActivity(groups[a]).After(latestActivity(groups[b]))
	})
	return groups
}

func latestActivity(g Group) time.Time {
	var latest time.Time
	for _, s := range g.Sessions {
		if s.LastActivity.After(latest) {
			latest = s.LastActivity
		}
		if s.StartedAt.After(latest) {
			latest = s.StartedAt
		}
	}
	return latest
}
