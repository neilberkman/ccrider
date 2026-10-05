package liveness

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Agents that record which session a process is serving give an exact,
// current answer, unlike argv (fixed at launch) or cwd (a guess). This file
// reads those records; Scan decides what to do with them.

// claudeRegistrySlack tolerates the gap between the process start time and
// the startedAt Claude Code stamps into its registry entry. A larger gap means
// the entry belongs to an earlier process that had the same PID.
const claudeRegistrySlack = 30 * time.Second

// claudeConfigDirs lists the directories that may hold Claude Code's per-PID
// session registry: $CLAUDE_CONFIG_DIR, ~/.claude, and the ~/.claude-<name>
// directories used to run several accounts side by side. A process's own
// CLAUDE_CONFIG_DIR is not readable from outside on every platform, so each
// directory is checked and the registry entry's startedAt picks the right one.
func claudeConfigDirs() []string {
	seen := make(map[string]bool)
	var dirs []string
	add := func(dir string) {
		if dir == "" || seen[dir] {
			return
		}
		if info, err := os.Stat(filepath.Join(dir, "sessions")); err == nil && info.IsDir() {
			seen[dir] = true
			dirs = append(dirs, dir)
		}
	}
	add(os.Getenv("CLAUDE_CONFIG_DIR"))
	if home, err := os.UserHomeDir(); err == nil {
		add(filepath.Join(home, ".claude"))
		matches, _ := filepath.Glob(filepath.Join(home, ".claude-*"))
		for _, m := range matches {
			add(m)
		}
	}
	return dirs
}

// claudeRegistryEntry is the subset of <config>/sessions/<pid>.json that
// liveness reads. Claude Code rewrites sessionId when /clear or an in-session
// resume moves the process to another session, and removes the file on exit.
type claudeRegistryEntry struct {
	PID       int32  `json:"pid"`
	SessionID string `json:"sessionId"`
	StartedAt int64  `json:"startedAt"` // unix milliseconds
}

// claudeRegistry reads every registry entry in dirs. The registry holds one
// small file per running Claude Code process, so reading it whole is cheap
// and also finds sessions whose process is a child of the listed one (a
// background session's pty host runs the session binary as its child).
func claudeRegistry(dirs []string) []claudeRegistryEntry {
	var entries []claudeRegistryEntry
	for _, dir := range dirs {
		files, _ := filepath.Glob(filepath.Join(dir, "sessions", "*.json"))
		for _, f := range files {
			data, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			var entry claudeRegistryEntry
			if json.Unmarshal(data, &entry) != nil || entry.PID <= 0 || entry.SessionID == "" {
				continue
			}
			if filepath.Base(f) != strconv.Itoa(int(entry.PID))+".json" {
				continue
			}
			entries = append(entries, entry)
		}
	}
	return entries
}

// belongsTo reports whether the entry was written by the process with this
// start time. A larger gap means an earlier process that had the same PID
// left the entry behind.
func (e claudeRegistryEntry) belongsTo(startedAt time.Time) bool {
	if startedAt.IsZero() || e.StartedAt <= 0 {
		return true
	}
	gap := time.UnixMilli(e.StartedAt).Sub(startedAt)
	return gap >= -claudeRegistrySlack && gap <= claudeRegistrySlack
}

// codexLockDir is where Codex keeps one lock file per thread, named
// <thread-uuid>.lock and held open by the process writing that thread.
func codexLockDir() string {
	home := os.Getenv("CODEX_HOME")
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		home = filepath.Join(userHome, ".codex")
	}
	return filepath.Join(home, "thread-writer-locks")
}

// codexThreadID turns a held lock path into the thread id it names, or ""
// for anything else in the directory (its coordination lock, stray files).
func codexThreadID(path string) string {
	id, ok := strings.CutSuffix(filepath.Base(path), ".lock")
	if !ok || !uuidLike(id) {
		return ""
	}
	return id
}

func uuidLike(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
				return false
			}
		}
	}
	return true
}
