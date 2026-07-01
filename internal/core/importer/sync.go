package importer

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/neilberkman/ccrider/pkg/ccsessions"
	"github.com/neilberkman/ccrider/pkg/codexsessions"
	"github.com/neilberkman/ccrider/pkg/copilotsessions"
	"github.com/neilberkman/ccrider/pkg/opencodesessions"
	"github.com/neilberkman/ccrider/pkg/pisessions"
)

// EnumerateFunc returns all parsed sessions for a database/event-log-backed
// provider (e.g. Copilot, OpenCode) that does not store one JSONL file per
// session in a flat, walkable directory.
type EnumerateFunc func() ([]*ccsessions.ParsedSession, error)

// Source describes a session source to import.
//
// File-based providers (Claude, Codex) set Path + ParseFn and are imported by
// walking a directory of JSONL files. Enumerated providers set EnumerateFn
// instead, which yields all sessions in one call.
type Source struct {
	Path          string
	ParseFn       ParseFunc
	EnumerateFn   EnumerateFunc
	Provider      string
	SkipSubagents bool
	Optional      bool
}

// DefaultSources returns the standard import sources (Claude + Codex + Copilot + OpenCode).
// Optional providers are only included when their data exists on disk.
func DefaultSources() []Source {
	home, err := os.UserHomeDir()
	if err != nil {
		return []Source{}
	}

	sources := []Source{
		{
			Path:          filepath.Join(home, ".claude", "projects"),
			ParseFn:       ccsessions.ParseFile,
			Provider:      "claude",
			SkipSubagents: true,
		},
	}

	codexPath := filepath.Join(home, ".codex", "sessions")
	if _, err := os.Stat(codexPath); err == nil {
		sources = append(sources, Source{
			Path:          codexPath,
			ParseFn:       codexsessions.ParseFile,
			Provider:      "codex",
			SkipSubagents: false,
		})
	}

	piPath := filepath.Join(home, ".pi", "agent", "sessions")
	if _, err := os.Stat(piPath); err == nil {
		sources = append(sources, Source{
			Path:          piPath,
			ParseFn:       pisessions.ParseFile,
			Provider:      pisessions.Provider,
			SkipSubagents: false,
		})
	}

	copilotStateDir := copilotsessions.DefaultStateDir()
	if copilotStateDir != "" {
		if _, err := os.Stat(copilotStateDir); err == nil {
			sources = append(sources, Source{
				Path:     copilotStateDir,
				Provider: "copilot",
				EnumerateFn: func() ([]*ccsessions.ParsedSession, error) {
					return copilotsessions.ParseAll(copilotStateDir)
				},
			})
		}
	}

	for _, dbPath := range opencodesessions.DefaultDBPaths() {
		path := dbPath
		sources = append(sources, Source{
			Path:     path,
			Provider: opencodesessions.Provider,
			Optional: true,
			EnumerateFn: func() ([]*ccsessions.ParsedSession, error) {
				return opencodesessions.ParseAll(path)
			},
		})
	}

	return sources
}

// PreparedSource bundles a source's total unit-of-work count with the action
// that imports it. Computing the count and choosing the import strategy
// (walk a directory vs enumerate sessions) is business logic shared by every
// interface, so it lives here rather than being re-derived in the CLI and TUI.
type PreparedSource struct {
	Provider string
	Path     string
	Total    int
	Warning  error
	run      func(progress ProgressCallback, force bool) (skipped int, err error)
}

// Run imports the prepared source, reporting progress, and returns the number
// of skipped (unchanged) units.
func (p PreparedSource) Run(progress ProgressCallback, force bool) (int, error) {
	return p.run(progress, force)
}

// PrepareSource resolves a Source into its work count and import action. For
// enumerated sources the sessions are read once here and reused by Run, so the
// underlying store/event logs are not parsed twice.
func (i *Importer) PrepareSource(src Source) (PreparedSource, error) {
	if src.EnumerateFn != nil {
		sessions, err := src.EnumerateFn()
		if err != nil {
			if src.Optional {
				return skippedPreparedSource(src, err), nil
			}
			return PreparedSource{}, err
		}
		return PreparedSource{
			Provider: src.Provider,
			Path:     src.Path,
			Total:    len(sessions),
			run: func(progress ProgressCallback, force bool) (int, error) {
				return i.ImportEnumerated(sessions, progress, force, src.Provider)
			},
		}, nil
	}

	total, err := CountJSONLFiles(src.Path, src.SkipSubagents)
	if err != nil {
		if src.Optional {
			return skippedPreparedSource(src, err), nil
		}
		return PreparedSource{}, err
	}
	return PreparedSource{
		Provider: src.Provider,
		Path:     src.Path,
		Total:    total,
		run: func(progress ProgressCallback, force bool) (int, error) {
			return i.ImportDirectory(src.Path, progress, force, src.SkipSubagents, src.ParseFn, src.Provider)
		},
	}, nil
}

func skippedPreparedSource(src Source, warning error) PreparedSource {
	return PreparedSource{
		Provider: src.Provider,
		Path:     src.Path,
		Warning:  warning,
		run: func(ProgressCallback, bool) (int, error) {
			return 0, nil
		},
	}
}

// CountJSONLFiles counts importable .jsonl files under dirPath, applying the
// same subagent/edit-conflict exclusions ImportDirectory uses.
func CountJSONLFiles(dirPath string, skipSubagents bool) (int, error) {
	count := 0
	err := filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || filepath.Ext(path) != ".jsonl" {
			return nil
		}
		basename := filepath.Base(path)
		if strings.Contains(basename, "Edit conflict") {
			return nil
		}
		if skipSubagents && (strings.Contains(path, "/subagents/") || strings.HasPrefix(basename, "agent-")) {
			return nil
		}
		count++
		return nil
	})
	return count, err
}

// SyncAll imports from all default sources. Silent (nil progress) for background use.
func (i *Importer) SyncAll(force bool) error {
	for _, src := range DefaultSources() {
		prepared, err := i.PrepareSource(src)
		if err != nil {
			return err
		}
		if prepared.Warning != nil {
			continue
		}
		if _, err := prepared.Run(nil, force); err != nil {
			return err
		}
	}
	return nil
}
