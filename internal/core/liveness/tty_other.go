//go:build !darwin

package liveness

import (
	"context"

	gops "github.com/shirou/gopsutil/v4/process"
)

// On linux gopsutil reads the TTY from /proc directly; on windows there is
// no TTY concept and Terminal() errors, leaving the column blank.
func ttyTable() map[int32]string { return nil }

func processTTY(ctx context.Context, p *gops.Process, _ map[int32]string) string {
	tty, err := p.TerminalWithContext(ctx)
	if err != nil {
		return ""
	}
	return tty
}
