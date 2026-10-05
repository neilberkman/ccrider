package llm

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestCodexExecArgs(t *testing.T) {
	args := codexExecArgs("gpt-x", "/w", "/w/out.txt")
	for _, want := range []string{"--ephemeral", "--ignore-user-config", "--ignore-rules", "--skip-git-repo-check"} {
		if !slices.Contains(args, want) {
			t.Errorf("args missing %s: %v", want, args)
		}
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{"--disable hooks", "--sandbox read-only", "-C /w", "-o /w/out.txt", "-m gpt-x"} {
		if !strings.Contains(joined, want) {
			t.Errorf("args missing %q: %s", want, joined)
		}
	}
	if args[0] != "exec" || args[len(args)-1] != "-" {
		t.Errorf("want `exec ... -` (prompt on stdin), got %v", args)
	}
	if slices.Contains(codexExecArgs("", "/w", "/w/o"), "-m") {
		t.Error("empty model should leave the Codex default in place")
	}
}

// A fake codex binary checks the provider end to end: prompt on stdin,
// answer read from the -o file.
func TestCodexProviderGenerateText(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script fake")
	}
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	fake := filepath.Join(dir, "codex")
	script := `#!/bin/sh
echo "$@" > ` + argsFile + `
out=""
while [ $# -gt 0 ]; do
  if [ "$1" = "-o" ]; then out="$2"; fi
  shift
done
prompt=$(cat)
printf 'ONE_LINE: got %s' "$prompt" > "$out"
`
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	p, err := NewCodexProvider(CodexConfig{Binary: fake})
	if err != nil {
		t.Fatal(err)
	}
	out, err := p.GenerateText(context.Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}
	if out != "ONE_LINE: got hello" {
		t.Errorf("GenerateText() = %q", out)
	}
	args, _ := os.ReadFile(argsFile)
	if !strings.Contains(string(args), "--ephemeral") {
		t.Errorf("codex ran without --ephemeral: %s", args)
	}
}

func TestCodexProviderFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script fake")
	}
	fake := filepath.Join(t.TempDir(), "codex")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho 'not logged in' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	p, err := NewCodexProvider(CodexConfig{Binary: fake})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.GenerateText(context.Background(), "hello")
	if err == nil || !strings.Contains(err.Error(), "not logged in") {
		t.Fatalf("want the codex stderr in the error, got %v", err)
	}
}

func TestNewCodexProviderMissingBinary(t *testing.T) {
	if _, err := NewCodexProvider(CodexConfig{Binary: "/nonexistent/codex"}); err == nil {
		t.Fatal("want an error for a missing codex binary")
	}
}
