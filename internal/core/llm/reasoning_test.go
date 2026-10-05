package llm

import "testing"

func TestStripReasoning(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"none", "ONE_LINE: a\nFULL: b", "ONE_LINE: a\nFULL: b"},
		{"think block", "<think>\nhmm ONE_LINE: wrong\n</think>\nONE_LINE: right", "ONE_LINE: right"},
		{"thinking tag, mixed case", "<Thinking>x</Thinking>ONE_LINE: right", "ONE_LINE: right"},
		{"opened in the prompt", "reasoning only\n</think>\n\nONE_LINE: right", "ONE_LINE: right"},
		{"two blocks", "<think>a</think>ONE_LINE: r<think>b</think>", "ONE_LINE: r"},
	}
	for _, tt := range tests {
		if got := stripReasoning(tt.in); got != tt.want {
			t.Errorf("%s: stripReasoning() = %q, want %q", tt.name, got, tt.want)
		}
	}
}
