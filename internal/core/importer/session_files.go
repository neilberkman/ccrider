package importer

import (
	"io/fs"
	"path/filepath"
	"strings"
)

func sessionFileID(path, provider string) string {
	name := filepath.Base(path)
	if provider == "codex" {
		name = strings.TrimSuffix(name, ".zst")
	}
	return strings.TrimSuffix(name, ".jsonl")
}

// sessionFiles is shared by discovery, import, and resume metadata backfills.
// Codex can archive cold rollouts in place. Prefer the plain sibling when both
// exist, as Codex does, so a stale archive never overwrites the live transcript.
func sessionFiles(dirPath string, skipSubagents bool, provider string) ([]string, error) {
	var files []string
	plain := make(map[string]bool)
	err := filepath.WalkDir(dirPath, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		compressed := provider == "codex" && strings.HasSuffix(path, ".jsonl.zst")
		if filepath.Ext(path) != ".jsonl" && !compressed {
			return nil
		}
		name := entry.Name()
		if strings.Contains(name, "Edit conflict") {
			return nil
		}
		if skipSubagents && (strings.Contains(filepath.ToSlash(path), "/subagents/") || strings.HasPrefix(name, "agent-")) {
			return nil
		}
		files = append(files, path)
		if !compressed {
			plain[path] = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	selected := files[:0]
	for _, path := range files {
		if strings.HasSuffix(path, ".jsonl.zst") && plain[strings.TrimSuffix(path, ".zst")] {
			continue
		}
		selected = append(selected, path)
	}
	return selected, nil
}
