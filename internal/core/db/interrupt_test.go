package db

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// openHandles counts this process's open files under dir, or returns -1 where
// the platform gives no cheap way to list them.
func openHandles(t *testing.T, dir string) int {
	t.Helper()
	switch runtime.GOOS {
	case "linux":
		entries, err := os.ReadDir("/proc/self/fd")
		if err != nil {
			return -1
		}
		n := 0
		for _, e := range entries {
			if target, err := os.Readlink(filepath.Join("/proc/self/fd", e.Name())); err == nil && strings.HasPrefix(target, dir) {
				n++
			}
		}
		return n
	case "darwin":
		out, err := exec.Command("lsof", "-p", fmt.Sprint(os.Getpid())).Output()
		if err != nil {
			return -1
		}
		return strings.Count(string(out), dir)
	}
	return -1
}

// A query interrupted by its context's deadline must not leave the
// connection open after Close. Watches end every wait this way, so a leak
// grows with each wait, and on Windows the database file can't be deleted.
func TestInterruptedQueriesReleaseConnections(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "interrupt.db")
	database, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 2000 {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(i%50)*time.Microsecond)
		_, _ = database.SignalsAfter(ctx, "c", 0, 20)
		_, _ = database.SignalHighWater(ctx)
		cancel()
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if n := openHandles(t, dir); n > 0 {
		t.Errorf("%d handles to the database are open after Close", n)
	}
	if err := os.Remove(path); err != nil {
		t.Errorf("database file can't be removed after Close: %v", err)
	}
}
