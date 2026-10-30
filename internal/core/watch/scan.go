package watch

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"

	"github.com/neilberkman/ccrider/pkg/ccsessions"
	"github.com/neilberkman/ccrider/pkg/sessionevents"
)

const scanChunk = 1 << 20

// scanLimit bounds the backward scan for the last turn boundary at arm time.
// Codex rollouts exceed 700 MB, so the scan never starts at the beginning of
// a large file. A variable so tests can shrink it.
var scanLimit int64 = 64 << 20

// firstLine returns the file's first newline-terminated line, without the
// newline, reading at most max bytes.
func firstLine(f *os.File, max int64) ([]byte, error) {
	r := bufio.NewReader(io.NewSectionReader(f, 0, max))
	line, err := r.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	return bytes.TrimRight(line, "\r\n"), nil
}

// lastCompleteLine returns the last newline-terminated line before size,
// without its line ending, and its byte range [start, end). With no complete
// line it returns nil and end 0.
func lastCompleteLine(ctx context.Context, f *os.File, size int64) (line []byte, start, end int64, err error) {
	var found []byte
	var foundStart, foundEnd int64
	err = scanLinesBackward(ctx, f, 0, size, func(l []byte, s, e int64) bool {
		found, foundStart, foundEnd = append([]byte(nil), l...), s, e
		return true
	})
	if err != nil {
		return nil, 0, 0, err
	}
	return found, foundStart, foundEnd, nil
}

// scanLinesBackward calls fn for each complete line in [from, to), last line
// first, until fn returns true. to must be 0 or just past a "\n" for the last
// line to be complete; bytes after the last "\n" are ignored. A line that
// starts before from is not reported unless from is 0.
func scanLinesBackward(ctx context.Context, f *os.File, from, to int64, fn func(line []byte, start, end int64) bool) error {
	// Trim an unterminated tail.
	end, err := lastNewlineEnd(ctx, f, from, to)
	if err != nil || end <= from {
		return err
	}
	var carry []byte // bytes of [pos+len(chunk), lineEnd) not yet split
	lineEnd := end
	pos := end
	for pos > from {
		if err := ctx.Err(); err != nil {
			return err
		}
		n := min(int64(scanChunk), pos-from)
		pos -= n
		chunk := make([]byte, n, int(n)+len(carry))
		if _, err := f.ReadAt(chunk, pos); err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		data := append(chunk, carry...)
		// data covers [pos, lineEnd); data ends with "\n".
		j := len(data) - 1
		for {
			i := bytes.LastIndexByte(data[:j], '\n')
			if i < 0 {
				break
			}
			if fn(trimEOL(data[i+1:j+1]), pos+int64(i+1), pos+int64(j+1)) {
				return nil
			}
			j = i
		}
		carry = data[:j+1]
		lineEnd = pos + int64(j+1)
	}
	if from == 0 && len(carry) > 0 {
		fn(trimEOL(carry), 0, lineEnd)
	}
	return nil
}

// lastNewlineEnd returns the offset just past the last "\n" in [from, to),
// or from if there is none.
func lastNewlineEnd(ctx context.Context, f *os.File, from, to int64) (int64, error) {
	pos := to
	buf := make([]byte, 64<<10)
	for pos > from {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		n := min(int64(len(buf)), pos-from)
		pos -= n
		if _, err := f.ReadAt(buf[:n], pos); err != nil && !errors.Is(err, io.EOF) {
			return 0, err
		}
		if i := bytes.LastIndexByte(buf[:n], '\n'); i >= 0 {
			return pos + int64(i) + 1, nil
		}
	}
	return from, nil
}

func trimEOL(b []byte) []byte {
	b = bytes.TrimSuffix(b, []byte{'\n'})
	return bytes.TrimSuffix(b, []byte{'\r'})
}

func hashLine(line []byte) string {
	sum := sha256.Sum256(line)
	return hex.EncodeToString(sum[:])
}

// boundary is a committed read position: the end of a complete record, the
// hash and length of that record, and the decoder state after it.
type boundary struct {
	Offset  int64
	Tail    string
	TailLen int64
	State   sessionevents.State
}

// resetState reports whether a record leaves the decoder in the same state
// whatever state it started in, and that state. Such a record is a safe place
// to start decoding: a turn end, or a Codex turn start.
func resetState(mk decoderFactory, line []byte) (sessionevents.State, bool) {
	a := mk(sessionevents.State{Turn: sessionevents.TurnIdle})
	b := mk(sessionevents.State{Turn: sessionevents.TurnRunning, TurnID: "\x00probe", Refused: true, LastAssistantError: true})
	if _, err := a.Decode(line); err != nil {
		return sessionevents.State{}, false
	}
	if _, err := b.Decode(line); err != nil {
		return sessionevents.State{}, false
	}
	if a.State() != b.State() {
		return sessionevents.State{}, false
	}
	return a.State(), true
}

// scanState finds the decoder state at the end of the last complete record
// before size. It scans backward, at most scanLimit bytes, for the last
// record that fixes the state on its own, then decodes forward from there.
// Without one, it decodes the scanned window from the unknown state, or from
// idle if the window reached the start of the file.
func scanState(ctx context.Context, f *os.File, size int64, mk decoderFactory) (boundary, error) {
	end, err := lastNewlineEnd(ctx, f, 0, size)
	if err != nil {
		return boundary{}, err
	}
	if end == 0 {
		return boundary{State: sessionevents.State{Turn: sessionevents.TurnIdle}}, nil
	}
	windowStart := max(0, end-scanLimit)

	start := sessionevents.State{Turn: sessionevents.TurnUnknown}
	if windowStart == 0 {
		start = sessionevents.State{Turn: sessionevents.TurnIdle}
	}
	from := int64(-1)
	err = scanLinesBackward(ctx, f, windowStart, end, func(line []byte, s, e int64) bool {
		if st, ok := resetState(mk, line); ok {
			start, from = st, e
			return true
		}
		return false
	})
	if err != nil {
		return boundary{}, err
	}
	if from < 0 {
		// No reset record in the window. Start at the window's first complete
		// line; at offset 0 that's the file start.
		from = windowStart
		if windowStart > 0 {
			from, err = nextLineStart(f, windowStart, end)
			if err != nil {
				return boundary{}, err
			}
		}
	}
	return decodeForward(ctx, f, from, end, mk(start))
}

// nextLineStart returns the offset just past the first "\n" at or after pos.
func nextLineStart(f *os.File, pos, limit int64) (int64, error) {
	r := bufio.NewReader(io.NewSectionReader(f, pos, limit-pos))
	skipped, err := r.ReadBytes('\n')
	if err != nil {
		return limit, nil
	}
	return pos + int64(len(skipped)), nil
}

// decodeForward decodes complete records in [from, to) with d and returns
// the boundary after the last one. Records that don't decode are skipped.
func decodeForward(ctx context.Context, f *os.File, from, to int64, d sessionevents.Decoder) (boundary, error) {
	b := boundary{Offset: from, State: d.State()}
	if from > 0 {
		line, s, e, err := lastCompleteLine(ctx, f, from)
		if err != nil {
			return boundary{}, err
		}
		if line != nil && e == from {
			b.Tail, b.TailLen = hashLine(line), e-s
		}
	}
	_, err := ccsessions.ForEachCompleteLine(ctx, io.NewSectionReader(f, from, to-from), from, to, func(l ccsessions.CompleteLine) error {
		if _, err := d.Decode(l.Bytes); errors.Is(err, sessionevents.ErrUnsupportedFormat) {
			return err
		}
		b = boundary{Offset: l.End, Tail: hashLine(l.Bytes), TailLen: l.End - l.Start, State: d.State()}
		return nil
	})
	return b, err
}

// verifyBoundary reports whether the record ending at b.Offset still has the
// recorded hash.
func verifyBoundary(f *os.File, b boundary) (bool, error) {
	if b.Offset == 0 {
		return true, nil
	}
	if b.TailLen <= 0 || b.TailLen > b.Offset {
		return false, nil
	}
	buf := make([]byte, b.TailLen)
	if _, err := f.ReadAt(buf, b.Offset-b.TailLen); err != nil {
		if errors.Is(err, io.EOF) {
			return false, nil
		}
		return false, err
	}
	if len(buf) == 0 || buf[len(buf)-1] != '\n' {
		return false, nil
	}
	return hashLine(trimEOL(buf)) == b.Tail, nil
}
