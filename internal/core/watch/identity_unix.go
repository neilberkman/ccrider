//go:build !windows

package watch

import (
	"os"
	"syscall"
)

// fileIdentity returns the device and inode of info. A rename over the
// watched path changes them.
func fileIdentity(info os.FileInfo) (dev, ino uint64) {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Dev), st.Ino
	}
	return 0, 0
}
