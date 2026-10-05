//go:build !darwin

package liveness

import (
	"context"
	"path/filepath"

	gops "github.com/shirou/gopsutil/v4/process"
)

// On linux gopsutil reads open files from /proc, so only the candidate
// processes are inspected. Where OpenFiles is unsupported the map stays
// empty and Codex falls back to the argv and cwd tiers.
func lockHolders(ctx context.Context, dir string, procs []*gops.Process) map[int32][]string {
	if dir == "" {
		return nil
	}
	held := make(map[int32][]string)
	for _, p := range procs {
		files, err := p.OpenFilesWithContext(ctx)
		if err != nil {
			continue
		}
		for _, f := range files {
			if filepath.Dir(f.Path) == dir {
				held[p.Pid] = append(held[p.Pid], f.Path)
			}
		}
	}
	return held
}
