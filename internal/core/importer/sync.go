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
	// EntrypointFn reads a session file's ParsedSession.Entrypoint without
	// parsing the transcript. File sources that record Entrypoint set it so
	// sync can backfill rows imported before the field existed.
	EntrypointFn func(ctx context.Context, path string) (string, error)
	// FingerprintFn returns a cheap digest of everything EnumerateFn reads.
	// When it matches the digest recorded after the source's last complete
	// import, an unforced sync skips the source without enumerating it.
	FingerprintFn func() (string, error)
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
			EntrypointFn:  ccsessions.ReadEntrypoint,
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
				FingerprintFn: func() (string, error) {
					return antigravitysessions.Fingerprint(root.Path)
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
	// Unchanged reports that the source's fingerprint matches its last
	// complete import, so there is nothing to import and Total is 0.
	Unchanged bool
	run       func(context.Context, ProgressCallback) (ImportResult, error)
}

// Run imports the prepared source, reporting per-unit outcomes.
func (p PreparedSource) Run(ctx context.Context, progress ProgressCallback) (ImportResult, error) {
	return p.run(ctx, progress)
}

// PrepareSource resolves a Source into its work count and import action.
// PrepareSource lists what src has to import. force re-imports every session
// whether or not it changed, and reads a fingerprinted source even when its
// fingerprint matches.
func (i *Importer) PrepareSource(ctx context.Context, src Source, force bool) (PreparedSource, error) {
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
			run: func(ctx context.Context, progress ProgressCallback) (ImportResult, error) {
				result, err := i.ImportRemote(ctx, refs, src.Remote.Fetch, progress, force, src.Provider)
				if src.Optional && errors.Is(err, context.DeadlineExceeded) {
					return result, nil
				}
				return result, err
			},
		}, nil
	}

	if src.EnumerateFn != nil {
		// The fingerprint is taken before enumerating, so a change made while
		// enumerating shows up as a mismatch on the next sync.
		fingerprint := i.sourceFingerprint(src)
		if fingerprint != "" && !force {
			recorded, err := i.db.SourceFingerprint(src.Provider, src.Path)
			if err != nil {
				return PreparedSource{}, fmt.Errorf("read %s fingerprint: %w", src.Provider, err)
			}
			if recorded == fingerprint {
				return unchangedPreparedSource(src), nil
			}
		}
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
			run: func(ctx context.Context, progress ProgressCallback) (ImportResult, error) {
				if err := ctx.Err(); err != nil {
					return ImportResult{}, err
				}
				result, err := i.ImportEnumerated(ctx, sessions, progress, force, src.Provider)
				// Only a complete import vouches for the fingerprint: a failed
				// or deferred session must be read again next time.
				if err == nil && fingerprint != "" && len(result.Failures) == 0 && len(result.Deferred) == 0 {
					if err := i.db.SetSourceFingerprint(src.Provider, src.Path, fingerprint); err != nil {
						return result, fmt.Errorf("record %s fingerprint: %w", src.Provider, err)
					}
				}
				return result, err
			},
		}, nil
	}

	files, err := sessionFiles(src.Path, src.SkipSubagents, src.Provider)
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
		Total:    len(files),
		run: func(ctx context.Context, progress ProgressCallback) (ImportResult, error) {
			if err := ctx.Err(); err != nil {
				return ImportResult{}, err
			}
			result, err := i.ImportDirectory(ctx, src.Path, progress, force, src.SkipSubagents, src.ParseFn, src.Provider)
			if err != nil {
				return result, err
			}
			if src.ResumeViaFn != nil {
				if err := i.BackfillResumeVia(ctx, src); err != nil {
					return result, err
				}
			}
			if src.EntrypointFn != nil {
				if err := i.BackfillEntrypoint(ctx, src); err != nil {
					return result, err
				}
			}
			return result, nil
		},
	}, nil
}

func skippedPreparedSource(src Source, warning error) PreparedSource {
	return PreparedSource{
		Provider: src.Provider,
		Path:     src.Path,
		Warning:  warning,
		run: func(context.Context, ProgressCallback) (ImportResult, error) {
			return ImportResult{}, nil
		},
	}
}

func unchangedPreparedSource(src Source) PreparedSource {
	return PreparedSource{
		Provider:  src.Provider,
		Path:      src.Path,
		Unchanged: true,
		run: func(context.Context, ProgressCallback) (ImportResult, error) {
			return ImportResult{}, nil
		},
	}
}

// sourceFingerprint returns src's current fingerprint, or "" when it has none
// or it cannot be computed; either way the source is enumerated as usual.
func (i *Importer) sourceFingerprint(src Source) string {
	if src.FingerprintFn == nil {
		return ""
	}
	fingerprint, err := src.FingerprintFn()
	if err != nil {
		return ""
	}
	return fingerprint
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

// CountJSONLFiles counts plain JSONL files using the importer's exclusions.
func CountJSONLFiles(dirPath string, skipSubagents bool) (int, error) {
	files, err := sessionFiles(dirPath, skipSubagents, "")
	return len(files), err
}

// SyncAll imports all supplied sources for background consumers and aggregates
// individual failures for the interface to report. Callers that can serve
// cached data should treat the returned error as a degraded refresh, not a
// failed query.
func (i *Importer) SyncAll(ctx context.Context, sources []Source, force bool) error {
	var failures []error
	for _, src := range sources {
		prepared, err := i.PrepareSource(ctx, src, force)
		if err != nil {
			failures = append(failures, err)
			return errors.Join(failures...)
		}
		if prepared.Warning != nil {
			failures = append(failures, fmt.Errorf("%s sync skipped: %w", prepared.Provider, prepared.Warning))
			continue
		}
		result, runErr := prepared.Run(ctx, nil)
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
	return i.backfillColumn(ctx, src, "resume_via", src.ResumeViaFn)
}

// BackfillEntrypoint records entrypoint for src's sessions imported before
// the column existed (NULL rows), reading each file through src.EntrypointFn
// instead of re-importing it. It follows the same rules as BackfillResumeVia.
func (i *Importer) BackfillEntrypoint(ctx context.Context, src Source) error {
	return i.backfillColumn(ctx, src, "entrypoint", func(path string) (string, error) {
		return src.EntrypointFn(ctx, path)
	})
}

// backfillColumn fills the sessions column named column, for src's rows where
// it is NULL, with read(path) of each session's file. column is always a
// constant from this package, never user input.
func (i *Importer) backfillColumn(ctx context.Context, src Source, column string, read func(path string) (string, error)) error {
	rows, err := i.db.Query(`SELECT session_id FROM sessions WHERE provider = ? AND `+column+` IS NULL`, src.Provider)
	if err != nil {
		return fmt.Errorf("load sessions missing %s: %w", column, err)
	}
	pending := make(map[string]bool)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan session missing %s: %w", column, err)
		}
		pending[id] = true
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("load sessions missing %s: %w", column, err)
	}
	if len(pending) == 0 {
		return nil
	}

	paths := make(map[string]string, len(pending))
	files, err := sessionFiles(src.Path, src.SkipSubagents, src.Provider)
	for _, path := range files {
		if id := sessionFileID(path, src.Provider); pending[id] {
			paths[id] = path
		}
	}
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
		if err := i.backfillColumnBatch(ctx, column, read, ids[start:end], paths, &readErrs); err != nil {
			return err
		}
	}
	return errors.Join(readErrs...)
}

func (i *Importer) backfillColumnBatch(ctx context.Context, column string, read func(path string) (string, error), ids []string, paths map[string]string, readErrs *[]error) error {
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
		value := ""
		if path, ok := paths[id]; ok {
			if value, err = read(path); errors.Is(err, fs.ErrNotExist) {
				value = "" // listed but unopenable, e.g. a dangling symlink
			} else if err != nil {
				*readErrs = append(*readErrs, fmt.Errorf("read %s for %s: %w", column, id, err))
				continue
			}
		}
		if _, err := tx.Exec(`UPDATE sessions SET `+column+` = ? WHERE session_id = ?`, value, id); err != nil {
			return fmt.Errorf("record %s for %s: %w", column, id, err)
		}
	}
	return tx.Commit()
}
