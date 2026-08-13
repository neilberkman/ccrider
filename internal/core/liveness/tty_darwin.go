//go:build darwin

package liveness

import (
	"context"
	"os/exec"
	"strconv"
	"strings"

	gops "github.com/shirou/gopsutil/v4/process"
)

// gopsutil's Terminal() is unimplemented on darwin, so the TTY column comes
// from one ps invocation for the whole scan (POSIX output format, trivially
// parseable). TTY is display-only: if ps fails the column is blank, never an
// error. This is the lone deliberate ps use — process discovery itself stays
// on gopsutil.
func ttyTable() map[int32]string {
	out, err := exec.Command("ps", "-axo", "pid=,tty=").Output()
	if err != nil {
		return nil
	}
	table := make(map[int32]string)
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || fields[1] == "??" {
			continue
		}
		pid, err := strconv.ParseInt(fields[0], 10, 32)
		if err != nil {
			continue
		}
		table[int32(pid)] = fields[1]
	}
	return table
}

func processTTY(_ context.Context, p *gops.Process, ttys map[int32]string) string {
	return ttys[p.Pid]
}
