package importer

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
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

// DefaultRemoteSyncTimeout is the standard total budget for one remote source.
// Interfaces may override it when their protocol has a different lifetime.
const DefaultRemoteSyncTimeout = 10 * time.Minute

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
// the importer can determine which ones are unchanged. Implementations must
// honor caller cancellation and independently bound each blocking external
// operation; the importer does not impose an account-wide timeout.
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
	// ResumeViaFn reads a session file's ParsedSession.ResumeVia without
	// parsing the transcript. File sources that record ResumeVia set it so
	// sync can backfill rows imported before the field existed.
	ResumeViaFn func(path string) (string, error)
}

// DefaultSources returns the standard import sources. Local optional providers
// are included when their data exists; Amp also requires explicit opt-in and
// its CLI on PATH.
func DefaultSources(ampEnabled bool) []Source {
	home, err := os.UserHomeDir()
	if err != nil {
		return []Source{}
	}

	var sources []Source

	// Claude Code is gated on its directory like every other provider, so a
	// machine that only runs OpenCode or Codex can still sync.
	claudePath := filepath.Join(home, ".claude", "projects")
	if _, err := os.Stat(claudePath); err == nil {
		sources = append(sources, Source{
			Path:          claudePath,
			ParseFn:       ccsessions.ParseFile,
			Provider:      "claude",
			SkipSubagents: true,
		})
	}

	codexPath := filepath.Join(home, ".codex", "sessions")
	if _, err := os.Stat(codexPath); err == nil {
		sources = append(sources, Source{
			Path:          codexPath,
			ParseFn:       codexsessions.ParseFile,
			Provider:      "codex",
			SkipSubagents: false,
			ResumeViaFn:   codexsessions.ReadResumeVia,
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

	for _, antigravityRoot := range antigravitysessions.DefaultRoots() {
		if _, err := os.Stat(filepath.Join(antigravityRoot.Path, "brain")); err == nil {
			root := antigravityRoot
			sources = append(sources, Source{
				Path:     root.Path,
				Provider: root.Provider,
				EnumerateFn: func() ([]*ccsessions.ParsedSession, error) {
					return antigravitysessions.ParseAll(root.Path)
				},
			})
		}
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
		// Remote clients bound each network command. The caller (normally
		// SyncSources) controls the source-wide lifetime.
		refs, err := src.Remote.List(ctx)
		if err != nil {
			// A source-wide deadline is an expected degraded outcome for an
			// optional remote provider. Parent cancellation is still propagated
			// by SyncSources after the callback returns.
			if src.Optional && errors.Is(err, context.DeadlineExceeded) {
				return skippedPreparedSource(src, err), nil
			}
			if ctxErr := ctx.Err(); ctxErr != nil {
				return PreparedSource{}, ctxErr
			}
			if src.Optional {
				return skippedPreparedSource(src, err), nil
			}
			return PreparedSource{}, err
		}
		return PreparedSource{
			Provider: src.Provider,
			Path:     src.Path,
			Total:    len(refs),
			run: func(ctx context.Context, progress ProgressCallback, force bool) (ImportResult, error) {
				result, err := i.ImportRemote(ctx, refs, src.Remote.Fetch, progress, force, src.Provider)
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
			result, err := i.ImportDirectory(ctx, src.Path, progress, force, src.SkipSubagents, src.ParseFn, src.Provider)
			if err != nil || src.ResumeViaFn == nil {
				return result, err
			}
			return result, i.BackfillResumeVia(ctx, src)
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

// SyncSources runs local sources first, then gives each remote source its own
// total budget. A zero timeout disables the remote budget. The callback owns
// source preparation, import, and interface-specific reporting.
func SyncSources(ctx context.Context, sources []Source, remoteTimeout time.Duration, syncSource func(context.Context, Source) error) error {
	if remoteTimeout < 0 {
		return errors.New("remote sync timeout must not be negative")
	}
	for _, remote := range []bool{false, true} {
		for _, src := range sources {
			if (src.Remote != nil) != remote {
				continue
			}
			if err := ctx.Err(); err != nil {
				return err
			}

			sourceCtx := ctx
			cancel := func() {}
			if remote && remoteTimeout > 0 {
				sourceCtx, cancel = context.WithTimeout(ctx, remoteTimeout)
			}
			err := syncSource(sourceCtx, src)
			cancel()
			if err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
		}
	}
	return nil
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
			failures = append(failures, err)
			return errors.Join(failures...)
		}
		if prepared.Warning != nil {
			failures = append(failures, fmt.Errorf("%s sync skipped: %w", prepared.Provider, prepared.Warning))
			continue
		}
		result, runErr := prepared.Run(ctx, nil, force)
		for _, failure := range result.Failures {
			failures = append(failures, fmt.Errorf("%s %s: %w", src.Provider, failure.ID, failure.Err))
		}
		if len(result.Deferred) > 0 {
			failures = append(failures, fmt.Errorf("%s sync incomplete: %d sessions deferred (%s)", src.Provider, len(result.Deferred), summarizeIDs(result.Deferred, 5)))
		}
		if runErr != nil {
			failures = append(failures, runErr)
			return errors.Join(failures...)
		}
	}
	return errors.Join(failures...)
}

func summarizeIDs(ids []string, limit int) string {
	if len(ids) <= limit {
		return strings.Join(ids, ", ")
	}
	return fmt.Sprintf("%s, and %d more", strings.Join(ids[:limit], ", "), len(ids)-limit)
}

// BackfillResumeVia records resume_via for src's sessions imported before the
// column existed (NULL rows), reading each file through src.ResumeViaFn
// instead of re-importing it. With no NULL rows it does not touch the
// filesystem. A row whose file is gone is recorded as "" so it is not looked
// up again; a file that fails to read stays NULL and is reported after the
// rest are recorded. Progress commits per batch, so a cancelled run resumes
// where it stopped on the next sync.
func (i *Importer) BackfillResumeVia(ctx context.Context, src Source) error {
	rows, err := i.db.Query(`SELECT session_id FROM sessions WHERE provider = ? AND resume_via IS NULL`, src.Provider)
	if err != nil {
		return fmt.Errorf("load sessions missing resume_via: %w", err)
	}
	pending := make(map[string]bool)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan session missing resume_via: %w", err)
		}
		pending[id] = true
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("load sessions missing resume_via: %w", err)
	}
	if len(pending) == 0 {
		return nil
	}

	paths := make(map[string]string, len(pending))
	err = filepath.WalkDir(src.Path, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".jsonl" {
			return nil
		}
		if id := strings.TrimSuffix(d.Name(), ".jsonl"); pending[id] {
			paths[id] = path
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("walk %s: %w", src.Path, err)
	}

	const batchSize = 500
	ids := make([]string, 0, len(pending))
	for id := range pending {
		ids = append(ids, id)
	}
	var readErrs []error
	for start := 0; start < len(ids); start += batchSize {
		end := min(start+batchSize, len(ids))
		if err := i.backfillResumeViaBatch(ctx, src, ids[start:end], paths, &readErrs); err != nil {
			return err
		}
	}
	return errors.Join(readErrs...)
}

func (i *Importer) backfillResumeViaBatch(ctx context.Context, src Source, ids []string, paths map[string]string, readErrs *[]error) error {
	tx, err := i.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			// Keep the rows already read; the next sync picks up the rest.
			if commitErr := tx.Commit(); commitErr != nil {
				return commitErr
			}
			return err
		}
		via := ""
		if path, ok := paths[id]; ok {
			if via, err = src.ResumeViaFn(path); err != nil {
				*readErrs = append(*readErrs, fmt.Errorf("read resume_via for %s: %w", id, err))
				continue
			}
		}
		if _, err := tx.Exec(`UPDATE sessions SET resume_via = ? WHERE session_id = ?`, via, id); err != nil {
			return fmt.Errorf("record resume_via for %s: %w", id, err)
		}
	}
	return tx.Commit()
}
