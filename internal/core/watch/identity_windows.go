//go:build windows

package watch

import "os"

// fileIdentity has no portable device/inode on Windows. Replacement is then
// caught only by the size and boundary-hash checks.
func fileIdentity(_ os.FileInfo) (dev, ino uint64) { return 0, 0 }
