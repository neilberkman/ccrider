package ccsessions

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
)

// tailCheckBytes is how often ForEachCompleteLine checks its context while
// reading one long line.
const tailCheckBytes = 1 << 20

// CompleteLine is one newline-terminated line read by ForEachCompleteLine.
// Bytes excludes the trailing "\n" or "\r\n". Start is the offset of the
// line's first byte; End is the offset just past its "\n".
type CompleteLine struct {
	Bytes []byte
	Start int64
	End   int64
}

// ForEachCompleteLine reads r, which is positioned at byte offset start, up
// to but not past frontier, and calls fn for every newline-terminated line.
// Unlike ForEachLine, an unterminated final line is not delivered: it may
// still be being written, and its bytes are left for a later read. Empty
// lines are skipped but still advance the offset.
//
// It returns the offset just past the last complete line, which is where the
// next read should start. ctx is checked before each line and at least every
// tailCheckBytes within a long line; on cancellation it returns the offset
// reached so far along with ctx's error.
func ForEachCompleteLine(ctx context.Context, r io.Reader, start, frontier int64, fn func(CompleteLine) error) (int64, error) {
	if frontier <= start {
		return start, ctx.Err()
	}
	reader := bufio.NewReaderSize(io.LimitReader(r, frontier-start), 64*1024)
	next := start
	var line []byte
	for {
		if err := ctx.Err(); err != nil {
			return next, err
		}
		line = line[:0]
		for {
			chunk, err := reader.ReadSlice('\n')
			line = append(line, chunk...)
			if err == nil {
				break
			}
			if errors.Is(err, bufio.ErrBufferFull) {
				if len(line)%tailCheckBytes < len(chunk) {
					if err := ctx.Err(); err != nil {
						return next, err
					}
				}
				continue
			}
			if errors.Is(err, io.EOF) {
				// Unterminated tail, or nothing left before the frontier.
				return next, nil
			}
			return next, err
		}
		end := next + int64(len(line))
		text := bytes.TrimSuffix(bytes.TrimSuffix(line, []byte{'\n'}), []byte{'\r'})
		if len(text) > 0 {
			if err := fn(CompleteLine{Bytes: text, Start: next, End: end}); err != nil {
				return next, err
			}
		}
		next = end
	}
}
