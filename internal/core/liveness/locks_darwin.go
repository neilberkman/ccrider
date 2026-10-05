//go:build darwin

package liveness

import (
	"context"
	"os/exec"
	"strconv"
	"strings"

	gops "github.com/shirou/gopsutil/v4/process"
)

// gopsutil's OpenFiles() is unimplemented on darwin, so lock holders come
// from one lsof invocation over the lock directory (non-recursive, machine
// output). A failed lsof leaves Codex on the argv and cwd tiers, never an
// error.
func lockHolders(ctx context.Context, dir string, _ []*gops.Process) map[int32][]string {
	if dir == "" {
		return nil
	}
	// lsof exits 1 when nothing in dir is open, so the output decides.
	out, _ := exec.CommandContext(ctx, "lsof", "-n", "-w", "-F", "pn", "+d", dir).Output()
	if len(out) == 0 {
		return nil
	}
	held := make(map[int32][]string)
	var pid int32
	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "p"):
			n, err := strconv.ParseInt(line[1:], 10, 32)
			if err != nil {
				pid = 0
				continue
			}
			pid = int32(n)
		case strings.HasPrefix(line, "n") && pid != 0:
			held[pid] = append(held[pid], line[1:])
		}
	}
	return held
}
