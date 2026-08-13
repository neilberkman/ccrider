package liveness

import (
	"context"
	"time"

	"github.com/neilberkman/ccrider/internal/core/session"
	gops "github.com/shirou/gopsutil/v4/process"
)

// SystemSource scans the host process table via gopsutil. Only processes
// whose command line matches a provider are inspected further, so the
// expensive lookups (cwd) run for a handful of processes, not thousands.
type SystemSource struct{}

func (SystemSource) Processes(ctx context.Context) ([]Process, error) {
	procs, err := gops.ProcessesWithContext(ctx)
	if err != nil {
		return nil, err
	}

	ttys := ttyTable()

	var out []Process
	for _, p := range procs {
		argv, err := p.CmdlineSliceWithContext(ctx)
		if err != nil || len(argv) == 0 {
			continue
		}
		if _, ok := session.MatchLiveProcess(argv); !ok {
			continue
		}

		row := Process{PID: p.Pid, Argv: argv}
		if ppid, err := p.PpidWithContext(ctx); err == nil {
			row.PPID = ppid
		}
		if cwd, err := p.CwdWithContext(ctx); err == nil {
			row.Cwd = cwd
		}
		if created, err := p.CreateTimeWithContext(ctx); err == nil && created > 0 {
			row.StartedAt = time.UnixMilli(created)
		}
		row.TTY = processTTY(ctx, p, ttys)
		out = append(out, row)
	}
	return out, nil
}
