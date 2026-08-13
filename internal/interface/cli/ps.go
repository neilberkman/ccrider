package cli

import (
	"fmt"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/neilberkman/ccrider/internal/core/db"
	"github.com/neilberkman/ccrider/internal/core/liveness"
	"github.com/spf13/cobra"
)

var psCmd = &cobra.Command{
	Use:   "ps",
	Short: "List coding agent sessions with a live process attached",
	Long: `List currently running coding agent processes matched to their sessions,
grouped by project and annotated with idle time.

Matching confidence:
  - a session id in the process command line is an exact match
  - otherwise the process working directory is matched against session
    project paths (sessions started after the process began)
  - agent processes matching no known session are listed as unknown`,
	Args: cobra.NoArgs,
	RunE: runPs,
}

func init() {
	rootCmd.AddCommand(psCmd)
}

func runPs(cmd *cobra.Command, args []string) error {
	database, err := db.New(dbPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer func() {
		_ = database.Close()
	}()

	live, err := liveness.Scan(cmd.Context(), liveness.SystemSource{}, database)
	if err != nil {
		return fmt.Errorf("process scan failed: %w", err)
	}
	if len(live) == 0 {
		fmt.Println("No live agent sessions.")
		return nil
	}

	now := time.Now()
	for _, group := range liveness.GroupByProject(live) {
		rows := collapseSessions(group.Sessions)
		fmt.Printf("%s (%d session%s)\n", group.ProjectPath, len(rows), plural(len(rows)))
		for _, r := range rows {
			fmt.Printf("  ● %-8s %-44s %-18s %s\n",
				r.session.Provider, psSummary(r.session), psWindows(r), psActivity(r.session, now))
		}
		fmt.Println()
	}
	return nil
}

// psRow is one display row: a session and every window (TTY/process) it is
// open in. The same session resumed in several terminals is one row.
type psRow struct {
	session liveness.LiveSession
	ttys    []string
}

func collapseSessions(sessions []liveness.LiveSession) []psRow {
	index := make(map[string]int)
	var rows []psRow
	for _, l := range sessions {
		if l.SessionID == "" {
			// unknown sessions stay per-process; there is nothing to merge on
			rows = append(rows, psRow{session: l, ttys: ttyList(l)})
			continue
		}
		if i, ok := index[l.SessionID]; ok {
			rows[i].ttys = append(rows[i].ttys, ttyList(l)...)
			continue
		}
		index[l.SessionID] = len(rows)
		rows = append(rows, psRow{session: l, ttys: ttyList(l)})
	}
	return rows
}

func ttyList(l liveness.LiveSession) []string {
	if l.TTY == "" {
		return nil
	}
	return []string{l.TTY}
}

func psWindows(r psRow) string {
	switch len(r.ttys) {
	case 0:
		return ""
	case 1:
		return r.ttys[0]
	case 2:
		return r.ttys[0] + "," + r.ttys[1]
	default:
		return fmt.Sprintf("%s +%d more", r.ttys[0], len(r.ttys)-1)
	}
}

func psSummary(l liveness.LiveSession) string {
	switch {
	case l.Summary != "":
		return truncateMessage(l.Summary, 44)
	case l.SessionID != "":
		return l.SessionID
	default:
		return fmt.Sprintf("[unknown session, pid %d]", l.PID)
	}
}

func psActivity(l liveness.LiveSession, now time.Time) string {
	if l.LastActivity.IsZero() {
		if l.StartedAt.IsZero() {
			return ""
		}
		return "started " + humanize.RelTime(l.StartedAt, now, "ago", "")
	}
	idle := l.IdleFor(now)
	if idle < time.Hour {
		return "active " + humanize.RelTime(l.LastActivity, now, "ago", "")
	}
	return "idle " + humanize.RelTime(l.LastActivity, now, "", "")
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
