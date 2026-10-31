package watch

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/neilberkman/ccrider/internal/core/db"
	"github.com/neilberkman/ccrider/pkg/ccsessions"
	"github.com/neilberkman/ccrider/pkg/codexsessions"
	"github.com/neilberkman/ccrider/pkg/sessionevents"
)

// Gap reasons.
const (
	GapReplaced       = "replaced"
	GapTruncated      = "truncated"
	GapRewritten      = "rewritten"
	GapCorrupt        = "corrupt"
	GapRolloutChanged = "rollout_changed"
)

// MaxPhraseRunes caps a phrase.
const MaxPhraseRunes = 200

// codexRelocateInterval is how often a Codex watch checks whether another
// rollout of the thread has become active.
const codexRelocateInterval = 5 * time.Second

// entrypointScanBytes bounds the search for a Claude Code record that names
// the session's entrypoint.
const entrypointScanBytes = 4 << 20

func validatePhrase(p string) error {
	if p == "" {
		return errors.New("phrase is empty")
	}
	if !utf8.ValidString(p) || utf8.RuneCountInString(p) > MaxPhraseRunes {
		return fmt.Errorf("phrase must be valid UTF-8 and at most %d characters", MaxPhraseRunes)
	}
	for _, r := range p {
		if unicode.IsControl(r) {
			return errors.New("phrase must not contain control characters")
		}
	}
	return nil
}

func factoryFor(provider string, src source) decoderFactory {
	if provider == providerCodex {
		meta := src.meta
		return func(s sessionevents.State) sessionevents.Decoder { return codexsessions.NewCodexDecoder(meta, s) }
	}
	return func(s sessionevents.State) sessionevents.Decoder { return ccsessions.NewClaudeDecoder(s) }
}

func (w *Watcher) armTranscript(ctx context.Context, spec Spec, conds []string) (Result, error) {
	if spec.Channel != "" || spec.MintChannel {
		return Result{}, newError(CodeInvalidArgument, "channel applies only to the signal condition")
	}
	wantsPhrase := slices.Contains(conds, CondPhrase)
	phrase := spec.Phrase
	switch {
	case !wantsPhrase && (phrase != "" || spec.MintPhrase):
		return Result{}, newError(CodeInvalidArgument, "phrase applies only to the phrase condition")
	case wantsPhrase && phrase != "" && spec.MintPhrase:
		return Result{}, newError(CodeInvalidArgument, "give a phrase or ask for one to be minted, not both")
	case wantsPhrase && phrase == "" && !spec.MintPhrase:
		return Result{}, newError(CodeInvalidArgument, "the phrase condition needs a phrase; pass one or ask for one to be minted")
	case wantsPhrase && phrase == "":
		p, err := db.RandomPhrase()
		if err != nil {
			return Result{}, fmt.Errorf("mint phrase: %w", err)
		}
		phrase = p
	}
	if wantsPhrase {
		if err := validatePhrase(phrase); err != nil {
			return Result{}, newError(CodeInvalidArgument, err.Error())
		}
	}

	t, err := w.resolveTarget(ctx, spec.SessionID)
	if err != nil {
		return Result{}, err
	}
	src, err := locate(ctx, t, "")
	if err != nil {
		return Result{}, err
	}
	if t.provider == providerClaude && slices.Contains(conds, CondTurnEnd) {
		ep, err := claudeEntrypoint(ctx, src.path)
		if err != nil {
			return Result{}, err
		}
		switch ep {
		case "cli":
		case "":
			return Result{}, newError(CodeUnsupportedFormat, "this Claude Code session has no records naming its entrypoint yet; arm again once it has started")
		default:
			return Result{}, newError(CodeUnsupportedFormat, fmt.Sprintf("turn_end needs an interactive Claude Code session; this one's entrypoint is %q, which doesn't record turn ends. message and phrase still work", ep))
		}
	}
	dev, ino, b, err := snapshot(ctx, src.path, factoryFor(t.provider, src))
	if err != nil {
		return Result{}, err
	}
	c := cursor{
		V: cursorVersion, DV: decoderVersion, Until: conds, Phrase: phrase,
		SessionID: t.id, Provider: t.provider, Source: src.src, Dev: dev, Ino: ino,
	}
	c.setBoundary(b)
	return Result{
		Status: StatusArmed, Events: []Event{}, Cursor: c.encode(), Phrase: phrase,
		TargetState: b.State.Turn,
	}, nil
}

func decodeError(err error) error {
	if errors.Is(err, errSourceChanged) {
		return err
	}
	if errors.Is(err, sessionevents.ErrUnsupportedFormat) {
		return newError(CodeUnsupportedFormat, "this transcript uses a format session watches don't support (Codex rollouts without record ordinals)")
	}
	return err
}

func (c *cursor) setBoundary(b boundary) {
	c.Offset, c.Tail, c.TailLen = b.Offset, b.Tail, b.TailLen
	s := b.State
	c.State = &s
}

func (c cursor) boundary() boundary {
	return boundary{Offset: c.Offset, Tail: c.Tail, TailLen: c.TailLen, State: *c.State}
}

// claudeEntrypoint returns the entrypoint named by the first record that has
// one, reading at most entrypointScanBytes.
func claudeEntrypoint(ctx context.Context, path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", newError(CodeNotFound, err.Error())
	}
	defer func() { _ = f.Close() }()
	r := bufio.NewReader(io.NewSectionReader(f, 0, entrypointScanBytes))
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		line, err := r.ReadBytes('\n')
		if len(line) > 0 && line[len(line)-1] == '\n' {
			if ep := ccsessions.ClaudeEntrypoint(line); ep != "" {
				return ep, nil
			}
		}
		if err != nil {
			return "", nil
		}
	}
}

// snapshotAttempts bounds how many times snapshot rescans a file that keeps
// changing under it.
const snapshotAttempts = 3

// snapshot opens path and returns its identity and the boundary at its end,
// with decoder state from a backward scan. If the file changes or is
// replaced during the scan, it scans again.
func snapshot(ctx context.Context, path string, mk decoderFactory) (dev, ino uint64, b boundary, err error) {
	for attempt := 0; attempt < snapshotAttempts; attempt++ {
		dev, ino, b, err = snapshotOnce(ctx, path, mk)
		if !errors.Is(err, errSourceChanged) {
			return dev, ino, b, err
		}
	}
	return 0, 0, boundary{}, fmt.Errorf("%s kept changing while being scanned; try again", path)
}

func snapshotOnce(ctx context.Context, path string, mk decoderFactory) (uint64, uint64, boundary, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, boundary{}, newError(CodeNotFound, err.Error())
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return 0, 0, boundary{}, err
	}
	dev, ino, err := fileIdentity(f)
	if err != nil {
		return 0, 0, boundary{}, err
	}
	b, err := scanState(ctx, f, info.Size(), mk)
	if err != nil {
		return 0, 0, boundary{}, decodeError(err)
	}
	if pinfo, err := os.Stat(path); err != nil || !os.SameFile(info, pinfo) {
		return 0, 0, boundary{}, errSourceChanged
	}
	return dev, ino, b, nil
}

func (w *Watcher) waitTranscript(ctx context.Context, c cursor, limits Limits) (Result, error) {
	t := target{provider: c.Provider, id: c.SessionID}
	var src source
	var located time.Time
	ticker := time.NewTicker(w.pollInterval())
	defer ticker.Stop()
	for {
		if src.path == "" || (t.provider == providerCodex && time.Since(located) >= codexRelocateInterval) {
			active, err := locate(ctx, t, c.Source)
			if err != nil {
				if ctx.Err() != nil {
					return w.waitEnded(ctx, c)
				}
				return Result{}, err
			}
			located = time.Now()
			if active.src != c.Source {
				return w.gap(ctx, c, t, active, GapRolloutChanged)
			}
			src = active
		}
		res, done, err := w.pollTranscript(ctx, &c, t, src, limits)
		if err != nil {
			if ctx.Err() != nil {
				return w.waitEnded(ctx, c)
			}
			return Result{}, err
		}
		if done {
			return res, nil
		}
		select {
		case <-ctx.Done():
			return w.waitEnded(ctx, c)
		case <-ticker.C:
		}
	}
}

// afterFirstCheck runs between the first continuity check and the read.
// Tests use it to rewrite the file at that moment.
var afterFirstCheck func(path string)

// afterRecord runs after each record a poll reads. Tests use it to cancel
// partway through a read.
var afterRecord func()

// errStopBatch ends a read early once the batch is full.
var errStopBatch = errors.New("batch full")

// pollTranscript reads records after the cursor once. It returns done with
// a result when it has events or lost continuity. Otherwise it advances c
// past the records it read, none of which matched.
func (w *Watcher) pollTranscript(ctx context.Context, c *cursor, t target, src source, limits Limits) (Result, bool, error) {
	f, err := os.Open(src.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return w.gapResult(ctx, *c, t, src, GapReplaced)
		}
		return Result{}, false, err
	}
	defer func() { _ = f.Close() }()

	start := c.boundary()
	size, reason, err := checkContinuity(f, src.path, *c, start)
	if err != nil {
		return Result{}, false, err
	}
	if reason != "" {
		return w.gapResult(ctx, *c, t, src, reason)
	}
	if size == start.Offset {
		return Result{}, false, nil
	}
	if afterFirstCheck != nil {
		afterFirstCheck(src.path)
	}

	mk := factoryFor(t.provider, src)
	d := mk(start.State)
	commit := start
	var batch []Event
	batchBytes := 0
	corrupt := false
	_, err = ccsessions.ForEachCompleteLine(ctx, io.NewSectionReader(f, start.Offset, size-start.Offset), start.Offset, size, func(l ccsessions.CompleteLine) error {
		if afterRecord != nil {
			defer afterRecord()
		}
		before := d.State()
		after := boundary{Offset: l.End, Tail: hashLine(l.Bytes), TailLen: l.End - l.Start}
		decoded, err := d.Decode(l.Bytes)
		if errors.Is(err, sessionevents.ErrUnsupportedFormat) {
			return err
		}
		if err != nil {
			if len(batch) == 0 {
				// The record could have changed the turn, so the state after
				// it is uncertain until a reset record fixes it.
				corrupt = true
				after.State = uncertain(before)
				commit = after
			}
			return errStopBatch
		}
		after.State = d.State()
		events := w.matchEvents(*c, l, decoded)
		if len(events) == 0 {
			if len(batch) == 0 {
				commit = after
			}
			return nil
		}
		n := 0
		for _, ev := range events {
			n += eventSize(ev)
		}
		if len(batch) > 0 && (len(batch)+len(events) > limits.MaxEvents || batchBytes+n > limits.MaxBytes) {
			return errStopBatch
		}
		batch = append(batch, events...)
		batchBytes += n
		commit = after
		if len(batch) >= limits.MaxEvents {
			return errStopBatch
		}
		return nil
	})
	switch {
	case err == nil, errors.Is(err, errStopBatch):
	case errors.Is(err, context.DeadlineExceeded) && errors.Is(ctx.Err(), context.DeadlineExceeded):
		// The deadline cut the read short. Keep what was read: the events
		// collected so far, or the progress past records that didn't match.
		// Cancellation is different: the caller has gone, so it's an error.
	case errors.Is(err, sessionevents.ErrUnsupportedFormat):
		return Result{}, false, decodeError(err)
	default:
		return Result{}, false, err
	}

	// Check again after reading: a rewrite that landed between the first
	// check and the read would otherwise go unnoticed.
	if _, reason, err := checkContinuity(f, src.path, *c, start); err != nil {
		return Result{}, false, err
	} else if reason != "" {
		return w.gapResult(ctx, *c, t, src, reason)
	}

	c.setBoundary(commit)
	if corrupt {
		next := *c
		next.Gen++
		return Result{
			Status: StatusGap, Events: []Event{}, Cursor: next.encode(), Phrase: c.Phrase,
			TargetState: commit.State.Turn, GapReason: GapCorrupt,
		}, true, nil
	}
	if len(batch) == 0 {
		return Result{}, false, nil
	}
	return Result{
		Status: StatusEvent, Events: batch, Cursor: c.encode(), Phrase: c.Phrase,
		TargetState: commit.State.Turn,
	}, true, nil
}

// checkContinuity compares the open file and the path with the cursor, and
// the record ending at the cursor with its recorded hash. It returns the
// file's size and a gap reason, empty when continuity holds.
func checkContinuity(f *os.File, path string, c cursor, b boundary) (int64, string, error) {
	info, err := f.Stat()
	if err != nil {
		return 0, "", err
	}
	dev, ino, err := fileIdentity(f)
	if err != nil {
		return 0, "", err
	}
	if dev != c.Dev || ino != c.Ino {
		return 0, GapReplaced, nil
	}
	if pinfo, err := os.Stat(path); err != nil || !os.SameFile(info, pinfo) {
		return 0, GapReplaced, nil
	}
	if info.Size() < b.Offset {
		return 0, GapTruncated, nil
	}
	ok, err := verifyBoundary(f, b)
	if err != nil {
		return 0, "", err
	}
	if !ok {
		return 0, GapRewritten, nil
	}
	return info.Size(), "", nil
}

// matchEvents converts decoded events into watch events that satisfy the
// cursor's conditions.
func (w *Watcher) matchEvents(c cursor, l ccsessions.CompleteLine, decoded []sessionevents.Event) []Event {
	var out []Event
	for i, e := range decoded {
		var matched []string
		switch e.Kind {
		case sessionevents.KindMessage:
			if slices.Contains(c.Until, CondMessage) {
				matched = append(matched, CondMessage)
			}
			if slices.Contains(c.Until, CondPhrase) && strings.Contains(e.Text, c.Phrase) {
				matched = append(matched, CondPhrase)
			}
		case sessionevents.KindTurnEnded:
			if slices.Contains(c.Until, CondTurnEnd) {
				matched = append(matched, CondTurnEnd)
			}
		}
		if len(matched) == 0 {
			continue
		}
		ev := Event{
			EventID: eventID(c, e, l, i), Kind: e.Kind, Matched: matched,
			SessionID: c.SessionID, Provider: c.Provider, At: e.At,
			TurnID: e.TurnID, Outcome: e.Outcome, Reason: e.Reason,
			DurationMS: e.DurationMS, Partial: e.Partial,
		}
		switch e.Kind {
		case sessionevents.KindMessage:
			ev.Excerpt = sessionevents.TruncateUTF8(e.Text, MaxExcerptBytes)
		case sessionevents.KindTurnEnded:
			ev.Excerpt = sessionevents.TruncateUTF8(e.Excerpt, MaxExcerptBytes)
		}
		out = append(out, ev)
	}
	return out
}

// eventID identifies an event within a watch. Claude Code record uuids are
// unique per record. Codex ordinals and byte offsets can be reused after a
// truncation, so they're qualified by the cursor's generation.
func eventID(c cursor, e sessionevents.Event, l ccsessions.CompleteLine, index int) string {
	idx := strconv.Itoa(index)
	if c.Provider == providerClaude && e.RecordKey != "" {
		return "claude:" + c.SessionID + ":" + e.RecordKey + ":" + idx
	}
	key := e.RecordKey
	if key == "" {
		key = "@" + strconv.FormatInt(l.Start, 10)
	}
	return c.Provider + ":" + c.Source + ":" + strconv.FormatInt(c.Gen, 10) + ":" + key + ":" + idx
}

// gap returns a gap result that continues in src: the current end of the
// file, with decoder state found by scanning back, and the next generation.
func (w *Watcher) gap(ctx context.Context, c cursor, t target, src source, reason string) (Result, error) {
	res, _, err := w.gapResult(ctx, c, t, src, reason)
	return res, err
}

func (w *Watcher) gapResult(ctx context.Context, c cursor, t target, src source, reason string) (Result, bool, error) {
	if reason == GapReplaced || src.path == "" {
		fresh, err := locate(ctx, t, c.Source)
		if err != nil {
			return Result{}, false, err
		}
		if fresh.src != src.src {
			reason = GapRolloutChanged
		}
		src = fresh
	}
	dev, ino, b, err := snapshot(ctx, src.path, factoryFor(t.provider, src))
	if err != nil {
		return Result{}, false, err
	}
	next := c
	next.Source = src.src
	next.Dev, next.Ino = dev, ino
	next.Gen++
	next.setBoundary(b)
	res := Result{
		Status: StatusGap, Events: []Event{}, Cursor: next.encode(), Phrase: c.Phrase,
		TargetState: b.State.Turn, GapReason: reason,
	}
	if reason == GapRolloutChanged {
		res.Source = src.src
	}
	return res, true, nil
}
