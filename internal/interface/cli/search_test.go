package cli

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// A message with no whitespace in the first maxLen bytes used to panic: the
// word-break guard compared against maxLen-50, which is negative for any
// column narrower than 50, so LastIndexAny's -1 "not found" sentinel passed it
// and produced a negative slice bound.
func TestTruncateMessageWithoutWhitespaceInWindow(t *testing.T) {
	msg := "/Users/neil/mirala/firmware/tardigrade/deep/path/notes.go"

	got := truncateMessage(msg, 44)

	if !strings.HasSuffix(got, "...") {
		t.Errorf("truncateMessage() = %q, want an ellipsis suffix", got)
	}
	if len(got) != 44+len("...") {
		t.Errorf("truncateMessage() length = %d, want the full %d-byte budget plus the ellipsis", len(got), 44)
	}
	if !strings.HasPrefix(msg, strings.TrimSuffix(got, "...")) {
		t.Errorf("truncateMessage() = %q, want a prefix of the input", got)
	}
}

// A caller sizing a column from a narrow terminal can arrive at zero or a
// negative width; neither may panic.
func TestTruncateMessageNonPositiveWidth(t *testing.T) {
	for _, maxLen := range []int{0, -1, -50, -1000} {
		t.Run(strings.TrimSpace(strings.Repeat(" ", 0)+itoa(maxLen)), func(t *testing.T) {
			if got := truncateMessage("some message worth showing", maxLen); got != "" {
				t.Errorf("truncateMessage(_, %d) = %q, want empty", maxLen, got)
			}
		})
	}
}

// The old code sliced raw bytes, so a cut landing inside a multi-byte rune
// emitted invalid UTF-8.
func TestTruncateMessageKeepsValidUTF8(t *testing.T) {
	// Every rune is 4 bytes, so most cut positions fall mid-rune.
	msg := strings.Repeat("🚀", 20)

	for maxLen := 1; maxLen <= len(msg); maxLen++ {
		got := truncateMessage(msg, maxLen)
		if !utf8.ValidString(got) {
			t.Fatalf("truncateMessage(_, %d) = %q, which is not valid UTF-8", maxLen, got)
		}
		if len(got) > maxLen+len("...") {
			t.Fatalf("truncateMessage(_, %d) returned %d bytes, over budget", maxLen, len(got))
		}
	}
}

func TestTruncateMessageBreaksOnWordBoundary(t *testing.T) {
	// The space sits inside the look-back window, so the cut snaps to it
	// rather than splitting the final word.
	msg := "the quick brown fox jumps over the lazy dog and keeps running onward"

	got := truncateMessage(msg, 40)

	if strings.HasSuffix(strings.TrimSuffix(got, "..."), " ") {
		t.Errorf("truncateMessage() = %q, want no trailing space before the ellipsis", got)
	}
	if !strings.HasPrefix(msg, strings.TrimSuffix(got, "...")) {
		t.Errorf("truncateMessage() = %q, want a prefix of the input", got)
	}
	if strings.Count(strings.TrimSuffix(got, "..."), " ") == 0 {
		t.Errorf("truncateMessage() = %q, want a word-boundary break", got)
	}
}

func TestTruncateMessageShortInputUnchanged(t *testing.T) {
	msg := "short enough"
	if got := truncateMessage(msg, 44); got != msg {
		t.Errorf("truncateMessage() = %q, want the input unchanged", got)
	}
}

// A message whose only whitespace is its first byte must not truncate to bare
// punctuation.
func TestTruncateMessageLeadingSpaceOnly(t *testing.T) {
	msg := " " + strings.Repeat("x", 80)

	got := truncateMessage(msg, 44)

	if got == "..." {
		t.Error("truncateMessage() discarded the whole message")
	}
	if !strings.HasPrefix(msg, strings.TrimSuffix(got, "...")) {
		t.Errorf("truncateMessage() = %q, want a prefix of the input", got)
	}
}

// itoa avoids pulling strconv in purely for subtest names.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		return "-" + string(digits)
	}
	return string(digits)
}
