package importer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/neilberkman/ccrider/pkg/antigravitysessions"
	"github.com/neilberkman/ccrider/pkg/ccsessions"
	"github.com/neilberkman/ccrider/pkg/codexsessions"
	"github.com/neilberkman/ccrider/pkg/copilotsessions"
	"github.com/neilberkman/ccrider/pkg/opencodesessions"
	"github.com/neilberkman/ccrider/pkg/pisessions"
)

const remoteSyncTimeout = 2 * time.Minute

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
	List  func(context.Context) ([]RemoteSessionRef, error)
	Fetch func(context.Context, RemoteSessionRef) (*ccsessions.ParsedSession, error)
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
// are included when their data exists; Amp also requires explicit opt-in and
// its CLI on PATH.
func DefaultSources(ampEnabled bool) []Source {
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

	if ampEnabled {
		if _, err := exec.LookPath("amp"); err != nil {
			return sources
		}
		client := newAmpClient()
		sources = append(sources, Source{
			Path:     "authenticated Amp account",
			Provider: ampProvider,
			Optional: true,
			Remote: &RemoteSource{
				List: func(ctx context.Context) ([]RemoteSessionRef, error) {
					return client.listThreads(ctx)
				},
				Fetch: func(ctx context.Context, ref RemoteSessionRef) (*ccsessions.ParsedSession, error) {
					return client.exportThread(ctx, ref.ImportID)
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
	run      func(context.Context, ProgressCallback, bool) (ImportResult, error)
}

// Run imports the prepared source, reporting per-unit outcomes.
func (p PreparedSource) Run(ctx context.Context, progress ProgressCallback, force bool) (ImportResult, error) {
	return p.run(ctx, progress, force)
}

// PrepareSource resolves a Source into its work count and import action.
func (i *Importer) PrepareSource(ctx context.Context, src Source) (PreparedSource, error) {
	if err := ctx.Err(); err != nil {
		return PreparedSource{}, err
	}
	if src.Remote != nil {
		deadline := time.Now().Add(remoteSyncTimeout)
		listCtx, cancel := context.WithDeadline(ctx, deadline)
		refs, err := src.Remote.List(listCtx)
		cancel()
		if err != nil {
			if src.Optional && errors.Is(err, context.DeadlineExceeded) {
				return skippedPreparedSource(src, err), nil
			}
			if ctx.Err() != nil {
				return PreparedSource{}, ctx.Err()
			}
			if src.Optional {
				return skippedPreparedSource(src, err), nil
			}
			return PreparedSource{}, err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			if src.Optional {
				return skippedPreparedSource(src, context.DeadlineExceeded), nil
			}
			return PreparedSource{}, context.DeadlineExceeded
		}
		return PreparedSource{
			Provider: src.Provider,
			Path:     src.Path,
			Total:    len(refs),
			run: func(ctx context.Context, progress ProgressCallback, force bool) (ImportResult, error) {
				// Count list and export execution toward the whole remote budget,
				// but not time a prepared source waits behind another provider.
				remoteCtx, cancel := context.WithTimeout(ctx, remaining)
				defer cancel()
				result, err := i.ImportRemote(remoteCtx, refs, src.Remote.Fetch, progress, force, src.Provider)
				if src.Optional && errors.Is(err, context.DeadlineExceeded) {
					return result, nil
				}
				return result, err
			},
		}, nil
	}

	if src.EnumerateFn != nil {
		sessions, err := src.EnumerateFn()
		if err != nil {
			if ctx.Err() != nil {
				return PreparedSource{}, ctx.Err()
			}
			if src.Optional {
				return skippedPreparedSource(src, err), nil
			}
			return PreparedSource{}, err
		}
		return PreparedSource{
			Provider: src.Provider,
			Path:     src.Path,
			Total:    len(sessions),
			run: func(ctx context.Context, progress ProgressCallback, force bool) (ImportResult, error) {
				if err := ctx.Err(); err != nil {
					return ImportResult{}, err
				}
				return i.ImportEnumerated(ctx, sessions, progress, force, src.Provider)
			},
		}, nil
	}

	total, err := CountJSONLFiles(src.Path, src.SkipSubagents)
	if err != nil {
		if ctx.Err() != nil {
			return PreparedSource{}, ctx.Err()
		}
		if src.Optional {
			return skippedPreparedSource(src, err), nil
		}
		return PreparedSource{}, err
	}
	return PreparedSource{
		Provider: src.Provider,
		Path:     src.Path,
		Total:    total,
		run: func(ctx context.Context, progress ProgressCallback, force bool) (ImportResult, error) {
			if err := ctx.Err(); err != nil {
				return ImportResult{}, err
			}
			return i.ImportDirectory(ctx, src.Path, progress, force, src.SkipSubagents, src.ParseFn, src.Provider)
		},
	}, nil
}

func skippedPreparedSource(src Source, warning error) PreparedSource {
	return PreparedSource{
		Provider: src.Provider,
		Path:     src.Path,
		Warning:  warning,
		run: func(context.Context, ProgressCallback, bool) (ImportResult, error) {
			return ImportResult{}, nil
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

// SyncAll imports all supplied sources for background consumers and aggregates
// individual failures for the interface to report. Callers that can serve
// cached data should treat the returned error as a degraded refresh, not a
// failed query.
func (i *Importer) SyncAll(ctx context.Context, sources []Source, force bool) error {
	var failures []error
	for _, src := range sources {
		prepared, err := i.PrepareSource(ctx, src)
		if err != nil {
			return err
		}
		if prepared.Warning != nil {
			continue
		}
		result, err := prepared.Run(ctx, nil, force)
		if err != nil {
			return err
		}
		for _, failure := range result.Failures {
			failures = append(failures, fmt.Errorf("%s %s: %w", src.Provider, failure.ID, failure.Err))
		}
	}
	return errors.Join(failures...)
}
