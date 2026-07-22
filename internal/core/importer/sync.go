package importer

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/neilberkman/ccrider/pkg/ampsessions"
	"github.com/neilberkman/ccrider/pkg/antigravitysessions"
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

// RemoteSessionRef identifies a remotely stored session and the opaque
// revision token used to detect whether it must be fetched again.
type RemoteSessionRef struct {
	ImportID string
	Revision string
}

// RemoteSource lists lightweight remote references and fetches one full
// session on demand. This avoids downloading every remote transcript before
// the importer can determine which ones are unchanged.
type RemoteSource struct {
	List  func() ([]RemoteSessionRef, error)
	Fetch func(RemoteSessionRef) (*ccsessions.ParsedSession, error)
}

// Source describes a session source to import.
//
// File-based providers (Claude, Codex) set Path + ParseFn and are imported by
// walking a directory of JSONL files. Enumerated providers set EnumerateFn
// instead, which yields all sessions in one call. Cloud-backed providers set
// Remote, allowing the importer to fetch only new or changed sessions.
type Source struct {
	Path          string
	ParseFn       ParseFunc
	EnumerateFn   EnumerateFunc
	Remote        *RemoteSource
	Provider      string
	SkipSubagents bool
	Optional      bool
}

// DefaultSources returns the standard import sources. Local optional providers
// are included when their data exists; Amp is included when its CLI is on PATH.
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

	piPath := filepath.Join(home, ".pi", "agent", "sessions")
	if _, err := os.Stat(piPath); err == nil {
		sources = append(sources, Source{
			Path:          piPath,
			ParseFn:       pisessions.ParseFile,
			Provider:      pisessions.Provider,
			SkipSubagents: false,
		})
	}

	antigravityRoot := antigravitysessions.DefaultRoot()
	if _, err := os.Stat(filepath.Join(antigravityRoot, "brain")); err == nil {
		root := antigravityRoot
		sources = append(sources, Source{
			Path:     root,
			Provider: antigravitysessions.Provider,
			EnumerateFn: func() ([]*ccsessions.ParsedSession, error) {
				return antigravitysessions.ParseAll(root)
			},
		})
	}

	if _, err := exec.LookPath("amp"); err == nil {
		client := ampsessions.NewClient()
		sources = append(sources, Source{
			Path:     "authenticated Amp account",
			Provider: ampsessions.Provider,
			Optional: true,
			Remote: &RemoteSource{
				List: func() ([]RemoteSessionRef, error) {
					threads, err := client.ListThreads()
					if err != nil {
						return nil, err
					}
					refs := make([]RemoteSessionRef, len(threads))
					for i, thread := range threads {
						refs[i] = RemoteSessionRef{ImportID: thread.ID, Revision: thread.Revision}
					}
					return refs, nil
				},
				Fetch: func(ref RemoteSessionRef) (*ccsessions.ParsedSession, error) {
					return client.ExportThread(ref.ImportID)
				},
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

// PrepareSource resolves a Source into its work count and import action.
func (i *Importer) PrepareSource(src Source) (PreparedSource, error) {
	if src.Remote != nil {
		refs, err := src.Remote.List()
		if err != nil {
			if src.Optional {
				return skippedPreparedSource(src, err), nil
			}
			return PreparedSource{}, err
		}
		return PreparedSource{
			Provider: src.Provider,
			Path:     src.Path,
			Total:    len(refs),
			run: func(progress ProgressCallback, force bool) (int, error) {
				return i.ImportRemote(refs, src.Remote.Fetch, progress, force, src.Provider)
			},
		}, nil
	}

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
