package llm

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// CodexProvider implements Provider by running `codex exec`, so summaries
// use whatever the Codex CLI is logged in with. With a ChatGPT login that is
// the plan's included usage rather than per-token API billing.
type CodexProvider struct {
	binary  string
	modelID string
}

// CodexConfig holds configuration for the Codex CLI provider
type CodexConfig struct {
	Binary  string // Path to the codex binary, defaults to "codex" on PATH
	ModelID string // Model ID; empty uses the Codex CLI's built-in default
}

// NewCodexProvider creates a provider backed by the Codex CLI
func NewCodexProvider(cfg CodexConfig) (*CodexProvider, error) {
	binary := cfg.Binary
	if binary == "" {
		binary = "codex"
	}
	path, err := exec.LookPath(binary)
	if err != nil {
		return nil, fmt.Errorf("codex CLI not found (%s): install it and run `codex login`", binary)
	}
	return &CodexProvider{binary: path, modelID: cfg.ModelID}, nil
}

// codexExecArgs builds the `codex exec` arguments for one summary call.
//
//   - --ephemeral: Codex writes no session file. Without it every summary
//     call would create a Codex session that ccrider then indexes and
//     summarizes on the next run.
//   - --ignore-user-config, --ignore-rules, --disable hooks: the user's MCP
//     servers, hooks, exec rules and reasoning settings are for interactive
//     work; loading them would slow every call and run hook side effects.
//     Auth still comes from CODEX_HOME.
//   - --sandbox read-only in an empty directory: the prompt is plain text
//     to summarize, and nothing in it should be able to touch files.
func codexExecArgs(modelID, workDir, outFile string) []string {
	args := []string{
		"exec",
		"--ephemeral",
		"--ignore-user-config",
		"--ignore-rules",
		"--disable", "hooks",
		"--skip-git-repo-check",
		"--sandbox", "read-only",
		"--color", "never",
		"-c", `model_reasoning_effort="low"`,
		"-C", workDir,
		"-o", outFile,
	}
	if modelID != "" {
		args = append(args, "-m", modelID)
	}
	// Read the prompt from stdin; session transcripts are too large for argv.
	return append(args, "-")
}

// GenerateText implements Provider
func (p *CodexProvider) GenerateText(ctx context.Context, prompt string) (string, error) {
	workDir, err := os.MkdirTemp("", "ccrider-codex-")
	if err != nil {
		return "", fmt.Errorf("codex: failed to create work dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(workDir) }()
	outFile := filepath.Join(workDir, "last-message.txt")

	cmd := exec.CommandContext(ctx, p.binary, codexExecArgs(p.modelID, workDir, outFile)...)
	cmd.Dir = workDir
	cmd.Stdin = strings.NewReader(prompt)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("codex exec failed: %w: %s", err, lastLines(stderr.String(), 5))
	}

	out, err := os.ReadFile(outFile)
	if err != nil {
		return "", fmt.Errorf("codex exec wrote no final message: %w", err)
	}
	response := strings.TrimSpace(string(out))
	if response == "" {
		return "", fmt.Errorf("codex exec returned an empty message")
	}
	return response, nil
}

// Name implements Provider
func (p *CodexProvider) Name() string {
	return "codex"
}

// ModelID returns the model being used, or "" for the Codex CLI default
func (p *CodexProvider) ModelID() string {
	return p.modelID
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
