package watch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/neilberkman/ccrider/internal/core/db"
	"github.com/neilberkman/ccrider/pkg/codexsessions"
	"github.com/neilberkman/ccrider/pkg/sessionevents"
)

const (
	providerClaude = "claude"
	providerCodex  = "codex"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// codexIDPattern splits a Codex session id (a rollout file name without
// .jsonl) into its thread id and optional replacement rollout suffix.
var codexIDPattern = regexp.MustCompile(`^rollout-\d{4}-\d{2}-\d{2}T\d{2}-\d{2}-\d{2}-([0-9a-fA-F-]{36})(?:_[0-9a-fA-F-]{36})?$`)

// target is the logical session a transcript watch follows: a Claude Code
// session id, or a Codex thread id.
type target struct {
	provider string
	id       string
}

// source is one transcript file of a target.
type source struct {
	path string
	// src names the file in event ids and cursors: the session id for Claude
	// Code, the rollout file name for Codex.
	src  string
	meta codexsessions.RolloutMeta
	// last is the timestamp of the file's last record with a valid one.
	// Codex uses it to choose the active rollout. lastKnown is false when no
	// such record could be found, which is not evidence that the rollout is
	// old.
	last      time.Time
	lastKnown bool
}

// resolveTarget maps a caller's session id to a watch target. The database
// resolves known ids and supplies the provider. An id the database doesn't
// have yet is looked up on disk when it is a full UUID or Codex rollout name,
// so a session started since the last sync can still be watched.
func (w *Watcher) resolveTarget(ctx context.Context, input string) (target, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return target{}, newError(CodeInvalidArgument, "session_id is required for transcript conditions")
	}
	id, err := w.DB.ResolveSessionIDContext(ctx, input)
	switch {
	case err == nil:
		provider, err := w.DB.SessionProvider(ctx, id)
		if err != nil {
			return target{}, err
		}
		return targetFor(provider, id)
	case errors.Is(err, db.ErrSessionNotFound):
	case ctx.Err() != nil:
		return target{}, ctx.Err()
	default:
		return target{}, newError(CodeInvalidArgument, err.Error())
	}

	if m := codexIDPattern.FindStringSubmatch(input); m != nil {
		return target{provider: providerCodex, id: strings.ToLower(m[1])}, nil
	}
	if uuidPattern.MatchString(input) {
		if _, err := locateClaude(ctx, input); err == nil {
			return target{provider: providerClaude, id: input}, nil
		}
		if cands := codexCandidates(ctx, strings.ToLower(input)); len(cands) > 0 {
			return target{provider: providerCodex, id: strings.ToLower(input)}, nil
		}
	}
	return target{}, newError(CodeNotFound, "no session found with id "+input)
}

func targetFor(provider, id string) (target, error) {
	switch provider {
	case providerClaude:
		return target{provider: providerClaude, id: id}, nil
	case providerCodex:
		m := codexIDPattern.FindStringSubmatch(id)
		if m == nil {
			return target{}, newError(CodeUnsupportedFormat, "can't read a Codex thread id from session id "+id)
		}
		return target{provider: providerCodex, id: strings.ToLower(m[1])}, nil
	default:
		return target{}, newError(CodeUnsupportedCondition, fmt.Sprintf("transcript conditions aren't supported for %s sessions; use a signal watch", provider))
	}
}

// claudeProjectDirs lists Claude Code projects directories:
// $CLAUDE_CONFIG_DIR, ~/.claude and ~/.claude-<name>, each once even when
// several are links to the same directory.
func claudeProjectDirs() []string {
	var roots []string
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		roots = append(roots, dir)
	}
	if home, err := os.UserHomeDir(); err == nil {
		roots = append(roots, filepath.Join(home, ".claude"))
		more, _ := filepath.Glob(filepath.Join(home, ".claude-*"))
		roots = append(roots, more...)
	}
	return uniqueDirs(roots, "projects")
}

// codexSessionDirs lists Codex sessions directories: $CODEX_HOME and
// ~/.codex, each once.
func codexSessionDirs() []string {
	var roots []string
	if dir := os.Getenv("CODEX_HOME"); dir != "" {
		roots = append(roots, dir)
	}
	if home, err := os.UserHomeDir(); err == nil {
		roots = append(roots, filepath.Join(home, ".codex"))
	}
	return uniqueDirs(roots, "sessions")
}

func uniqueDirs(roots []string, sub string) []string {
	seen := map[string]bool{}
	var out []string
	for _, root := range roots {
		dir := filepath.Join(root, sub)
		real, err := filepath.EvalSymlinks(dir)
		if err != nil {
			continue
		}
		if info, err := os.Stat(real); err != nil || !info.IsDir() || seen[real] {
			continue
		}
		seen[real] = true
		out = append(out, dir)
	}
	return out
}

// locateClaude finds a Claude Code session's transcript. If several config
// directories hold one, the most recently modified wins.
func locateClaude(ctx context.Context, sessionID string) (string, error) {
	var best string
	var bestMod time.Time
	seen := map[string]bool{}
	for _, dir := range claudeProjectDirs() {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		matches, _ := filepath.Glob(filepath.Join(dir, "*", sessionID+".jsonl"))
		for _, m := range matches {
			real, err := filepath.EvalSymlinks(m)
			if err != nil || seen[real] {
				continue
			}
			seen[real] = true
			info, err := os.Stat(real)
			if err != nil {
				continue
			}
			if best == "" || info.ModTime().After(bestMod) {
				best, bestMod = m, info.ModTime()
			}
		}
	}
	if best == "" {
		return "", newError(CodeNotFound, "no transcript found for Claude Code session "+sessionID)
	}
	return best, nil
}

// codexCandidates returns every rollout of a Codex thread: files whose name
// mentions the thread id and whose session_meta names it. Each candidate's
// last complete record timestamp is filled in.
func codexCandidates(ctx context.Context, threadID string) []source {
	var out []source
	seen := map[string]bool{}
	for _, dir := range codexSessionDirs() {
		matches, _ := filepath.Glob(filepath.Join(dir, "*", "*", "*", "rollout-*"+threadID+"*.jsonl"))
		for _, m := range matches {
			if ctx.Err() != nil {
				return out
			}
			real, err := filepath.EvalSymlinks(m)
			if err != nil || seen[real] {
				continue
			}
			seen[real] = true
			s, ok := readCodexSource(ctx, m)
			if ok && strings.EqualFold(s.meta.ThreadID, threadID) {
				out = append(out, s)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].src < out[j].src })
	return out
}

func readCodexSource(ctx context.Context, path string) (source, bool) {
	f, err := os.Open(path)
	if err != nil {
		return source{}, false
	}
	defer func() { _ = f.Close() }()
	first, err := firstLine(f, 1<<20)
	if err != nil {
		return source{}, false
	}
	meta, ok := codexsessions.ParseRolloutMeta(first)
	if !ok {
		return source{}, false
	}
	info, err := f.Stat()
	if err != nil {
		return source{}, false
	}
	s := source{path: path, src: strings.TrimSuffix(filepath.Base(path), ".jsonl"), meta: meta}
	s.last, s.lastKnown = lastRecordTime(ctx, f, info.Size())
	return s, true
}

// lastRecordTime returns the timestamp of the last record that has a valid
// one. A corrupt last line doesn't make an active rollout look idle: the
// record before it still shows when the rollout was last written, and the
// watch reports the corrupt line itself when it reads it.
//
// The search walks back record by record, reading each whole, until one has
// a timestamp. That's normally the last record; compacted records reach
// several megabytes, and corrupt lines after them are skipped. It is
// bounded by ctx, not by size.
func lastRecordTime(ctx context.Context, f *os.File, size int64) (time.Time, bool) {
	var last time.Time
	_ = scanLinesBackward(ctx, f, 0, size, func(line []byte, _, _ int64) bool {
		last = codexsessions.RecordTimestamp(line)
		return !last.IsZero()
	})
	return last, !last.IsZero()
}

// activeCodex picks the rollout with the latest last record. A tie keeps
// current, so two rollouts written in the same instant don't flap. If the
// current rollout's activity can't be established, it stays current: the
// watch reports whatever is wrong with it when it reads it. Rollouts whose
// activity can't be established are chosen only if no other rollout's can.
func activeCodex(cands []source, current string) (source, bool) {
	for _, c := range cands {
		if c.src == current && !c.lastKnown {
			return c, true
		}
	}
	var best source
	found := false
	for _, c := range cands {
		switch {
		case !found:
			best, found = c, true
		case c.lastKnown && !best.lastKnown:
			best = c
		case c.lastKnown != best.lastKnown:
		case c.last.After(best.last):
			best = c
		case c.last.Equal(best.last) && c.src == current:
			best = c
		}
	}
	return best, found
}

// locate finds the file a cursor or arm should read. For Codex it returns
// the active rollout.
func locate(ctx context.Context, t target, currentSrc string) (source, error) {
	switch t.provider {
	case providerClaude:
		path, err := locateClaude(ctx, t.id)
		if err != nil {
			return source{}, err
		}
		return source{path: path, src: t.id}, nil
	case providerCodex:
		cands := codexCandidates(ctx, t.id)
		if err := ctx.Err(); err != nil {
			return source{}, err
		}
		s, ok := activeCodex(cands, currentSrc)
		if !ok {
			return source{}, newError(CodeNotFound, "no rollout found for Codex thread "+t.id)
		}
		return s, nil
	}
	return source{}, newError(CodeUnsupportedCondition, "unsupported provider "+t.provider)
}

// decoderFactory builds a decoder for a source starting in a given state.
type decoderFactory func(sessionevents.State) sessionevents.Decoder
