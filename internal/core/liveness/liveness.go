// Package liveness discovers which coding agent sessions currently have a
// live process attached, and matches those processes back to sessions in the
// ccrider database. Detection is tiered: a session id recovered from the
// process command line is an exact match; otherwise the process working
// directory is matched against session project paths; a provider process
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
	MatchArgv    = "argv" // session id was present in the process command line
	MatchCwd     = "cwd"  // matched via process working directory + timing
	MatchUnknown = "none" // provider process with no matching session
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
	// window. A process whose parent is also a candidate is the helper.
	candidates := make(map[int32]bool, len(procs))
	for _, proc := range procs {
		candidates[proc.PID] = true
	}

	var live []LiveSession
	for _, proc := range procs {
		if proc.PPID != 0 && candidates[proc.PPID] {
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

		if match.SessionID != "" {
			if info := lookupSession(database, match.SessionID); info != nil {
				row.SessionID = info.SessionID
				row.Summary = info.Summary
				row.ProjectPath = info.ProjectPath
				row.LastActivity = info.UpdatedAt
				row.Match = MatchArgv
			}
		}
		if row.Match == MatchUnknown && proc.Cwd != "" {
			if info := matchByCwd(database, proc.Cwd, proc.StartedAt, match.Provider); info != nil {
				row.SessionID = info.SessionID
				row.Summary = info.Summary
				row.ProjectPath = info.ProjectPath
				row.LastActivity = info.UpdatedAt
				row.Match = MatchCwd
			}
		}
		live = append(live, row)
	}

	sort.Slice(live, func(i, j int) bool {
		if live[i].ProjectPath != live[j].ProjectPath {
			return live[i].ProjectPath < live[j].ProjectPath
		}
		return live[i].LastActivity.After(live[j].LastActivity)
	})
	return live, nil
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

// matchByCwd pairs a fresh (non-resumed) process with the most recent session
// of the same provider created at or after the process started, in the
// process's working directory or one of its ancestors. Walking up covers
// agents launched in a subdirectory of the recorded project path.
func matchByCwd(database *db.DB, cwd string, startedAt time.Time, provider string) *db.Session {
	const maxAncestors = 5
	dir := cwd
	for range maxAncestors {
		sessions, err := database.SessionsForProjectPath(dir, 10)
		if err == nil {
			for _, s := range sessions {
				if s.Provider != provider {
					continue
				}
				if !startedAt.IsZero() && s.CreatedAt.Before(startedAt.Add(-cwdCreatedSlack)) {
					continue
				}
				return &s
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return nil
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
