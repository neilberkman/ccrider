//go:build windows

package watch

import (
	"os"
	"syscall"
)

// fileIdentity returns the volume serial number and file index of an open
// file, which together identify it the way device and inode do on Unix.
func fileIdentity(f *os.File) (dev, ino uint64, err error) {
	var d syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(syscall.Handle(f.Fd()), &d); err != nil {
		return 0, 0, err
	}
	return uint64(d.VolumeSerialNumber), uint64(d.FileIndexHigh)<<32 | uint64(d.FileIndexLow), nil
}
