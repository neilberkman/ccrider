package ccsessions

import (
	"bufio"
	"io"
)

// ForEachLine calls fn once per line of r, with the trailing "\n" / "\r\n"
// stripped and empty lines skipped. Lines may be arbitrarily long:
// bufio.Reader.ReadBytes grows to fit the line (unlike bufio.Scanner, which
// has a hard buffer cap), which matters for JSONL session files whose
// single-line messages can embed entire files. A final line without a
// trailing newline is still delivered. An error from fn aborts the walk and
// is returned as-is; read errors other than io.EOF are returned as-is.
//
// This is the one shared line-reading loop for every JSONL-based session
// parser — EOF and long-line edge cases get fixed here, once.
func ForEachLine(r io.Reader, fn func(line []byte) error) error {
	reader := bufio.NewReaderSize(r, 1024*1024)
	for {
		line, err := reader.ReadBytes('\n')
		if n := len(line); n > 0 && line[n-1] == '\n' {
			line = line[:n-1]
		}
		if n := len(line); n > 0 && line[n-1] == '\r' {
			line = line[:n-1]
		}
		if len(line) > 0 {
			if fnErr := fn(line); fnErr != nil {
				return fnErr
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
