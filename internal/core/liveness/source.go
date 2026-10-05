package liveness

import (
	"context"
	"time"

	"github.com/neilberkman/ccrider/internal/core/session"
	gops "github.com/shirou/gopsutil/v4/process"
)

// SystemSource scans the host process table via gopsutil. Only processes
// whose command line matches a provider are inspected further, so the
// expensive lookups (cwd, session registries, held locks) run for a handful
// of processes, not thousands.
type SystemSource struct{}

func (SystemSource) Processes(ctx context.Context) ([]Process, error) {
	procs, err := gops.ProcessesWithContext(ctx)
	if err != nil {
		return nil, err
	}

	ttys := ttyTable()

	var out []Process
	var codexProcs []*gops.Process
	for _, p := range procs {
		argv, err := p.CmdlineSliceWithContext(ctx)
		if err != nil || len(argv) == 0 {
			continue
		}
		match, ok := session.MatchLiveProcess(argv)
		if !ok {
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
		if match.Provider == session.ProviderCodex {
			codexProcs = append(codexProcs, p)
		}
		out = append(out, row)
	}

	attachClaudeRegistry(ctx, out, claudeRegistry(claudeConfigDirs()))

	if len(codexProcs) > 0 {
		held := lockHolders(ctx, codexLockDir(), codexProcs)
		for i := range out {
			for _, path := range held[out[i].PID] {
				if id := codexThreadID(path); id != "" {
					out[i].Declared = append(out[i].Declared, id)
				}
			}
		}
	}
	return out, nil
}

// attachClaudeRegistry gives each Claude Code row the session its registry
// entry names. An entry whose process is not itself a row (its binary is
// named by version, not "claude") is credited to its parent row, which is how
// a background session's pty host gets the session it runs.
func attachClaudeRegistry(ctx context.Context, out []Process, entries []claudeRegistryEntry) {
	rows := make(map[int32]int, len(out))
	for i := range out {
		if m, ok := session.MatchLiveProcess(out[i].Argv); ok && m.Provider == session.ProviderClaude {
			rows[out[i].PID] = i
		}
	}
	for _, entry := range entries {
		if i, ok := rows[entry.PID]; ok {
			if entry.belongsTo(out[i].StartedAt) {
				out[i].Declared = append(out[i].Declared, entry.SessionID)
			}
			continue
		}
		child, err := gops.NewProcessWithContext(ctx, entry.PID)
		if err != nil {
			continue
		}
		var started time.Time
		if created, err := child.CreateTimeWithContext(ctx); err == nil && created > 0 {
			started = time.UnixMilli(created)
		}
		if !entry.belongsTo(started) {
			continue
		}
		ppid, err := child.PpidWithContext(ctx)
		if err != nil {
			continue
		}
		if i, ok := rows[ppid]; ok {
			out[i].Declared = append(out[i].Declared, entry.SessionID)
		}
	}
}
