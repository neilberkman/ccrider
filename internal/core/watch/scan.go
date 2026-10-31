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
	if len(carry) > 0 {
		// The remaining bytes form a complete line only if it starts exactly
		// at from: at the file start, or right after a newline.
		complete := from == 0
		if !complete {
			var prev [1]byte
			if _, err := f.ReadAt(prev[:], from-1); err != nil {
				return err
			}
			complete = prev[0] == '\n'
		}
		if complete {
			fn(trimEOL(carry), from, lineEnd)
		}
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
	// The reset record's state and hash come from the same read, so a
	// rewrite between reads can't pair one record's state with another's
	// hash. The final check below catches a rewrite after this read.
	from := int64(-1)
	var reset boundary
	err = scanLinesBackward(ctx, f, windowStart, end, func(line []byte, s, e int64) bool {
		if st, ok := resetState(mk, line); ok {
			start, from = st, e
			reset = boundary{Offset: e, Tail: hashLine(line), TailLen: e - s, State: st}
			return true
		}
		return false
	})
	if err != nil {
		return boundary{}, err
	}
	if afterBackwardScan != nil {
		afterBackwardScan()
	}
	if from < 0 {
		// No reset record in the window. Start at the window's first complete
		// line: at offset 0 that's the file start.
		from, err = lineStartAtOrAfter(f, windowStart, end)
		if err != nil {
			return boundary{}, err
		}
		reset = boundary{Offset: from, State: start}
		if from > 0 {
			line, s, e, err := lastCompleteLine(ctx, f, from)
			if err != nil {
				return boundary{}, err
			}
			if line != nil && e == from {
				reset.Tail, reset.TailLen = hashLine(line), e-s
			}
		}
	}
	b, err := decodeForward(ctx, f, reset, end, mk(start))
	if err != nil {
		return boundary{}, err
	}
	for _, check := range []boundary{reset, b} {
		ok, err := verifyBoundary(f, check)
		if err != nil {
			return boundary{}, err
		}
		if !ok {
			return boundary{}, errSourceChanged
		}
	}
	return b, nil
}

// afterBackwardScan runs between the backward scan and the forward decode.
// Tests use it to rewrite the file at that moment.
var afterBackwardScan func()

// errSourceChanged means a transcript changed while it was being scanned.
// Callers scan again.
var errSourceChanged = errors.New("transcript changed during scan")

// lineStartAtOrAfter returns the first offset at or after pos where a line
// starts: pos itself if it's the file start or follows a newline, otherwise
// just past the next newline (limit if there is none).
func lineStartAtOrAfter(f *os.File, pos, limit int64) (int64, error) {
	if pos == 0 {
		return 0, nil
	}
	var prev [1]byte
	if _, err := f.ReadAt(prev[:], pos-1); err != nil {
		return 0, err
	}
	if prev[0] == '\n' {
		return pos, nil
	}
	r := bufio.NewReader(io.NewSectionReader(f, pos, limit-pos))
	skipped, err := r.ReadBytes('\n')
	if err != nil {
		return limit, nil
	}
	return pos + int64(len(skipped)), nil
}

// decodeForward decodes complete records after start, up to to, with d and
// returns the boundary after the last one. A record that fails to decode
// leaves the turn state unknown, since it could have changed the turn.
func decodeForward(ctx context.Context, f *os.File, start boundary, to int64, d sessionevents.Decoder) (boundary, error) {
	b := start
	b.State = d.State()
	_, err := ccsessions.ForEachCompleteLine(ctx, io.NewSectionReader(f, start.Offset, to-start.Offset), start.Offset, to, func(l ccsessions.CompleteLine) error {
		_, err := d.Decode(l.Bytes)
		if errors.Is(err, sessionevents.ErrUnsupportedFormat) {
			return err
		}
		if err != nil {
			d.SetState(uncertain(d.State()))
		}
		b = boundary{Offset: l.End, Tail: hashLine(l.Bytes), TailLen: l.End - l.Start, State: d.State()}
		return nil
	})
	return b, err
}

// uncertain is the state after a record the decoder couldn't read: the turn
// is unknown until a reset record fixes it, and anything known about the
// current turn is kept for the partial event that will report it.
func uncertain(s sessionevents.State) sessionevents.State {
	s.Turn = sessionevents.TurnUnknown
	return s
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
