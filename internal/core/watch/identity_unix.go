//go:build !windows

package watch

import (
	"os"
	"syscall"
)

// fileIdentity returns the device and inode of an open file. A rename over
// the watched path changes them.
func fileIdentity(f *os.File) (dev, ino uint64, err error) {
	info, err := f.Stat()
	if err != nil {
		return 0, 0, err
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Dev), st.Ino, nil
	}
	return 0, 0, nil
}
