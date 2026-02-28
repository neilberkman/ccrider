//go:build !windows

package importer

import (
	"os"
	"syscall"
)

func getFileIdentity(path string) (uint64, uint64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, 0, err
	}

	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, nil
	}

	return stat.Ino, uint64(stat.Dev), nil
}
