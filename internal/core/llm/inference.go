package llm

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// LLM wraps llama.cpp CLI for inference
type LLM struct {
	modelPath string
	modelName string
	llamaCLI  string // Path to llama-cli binary
}

// NewLLM creates a new LLM instance
func NewLLM(modelPath string, modelName string) (*LLM, error) {
	// Find llama-cli binary
	llamaCLI, err := exec.LookPath("llama-cli")
	if err != nil {
		return nil, fmt.Errorf("llama-cli not found (install with: brew install llama.cpp): %w", err)
	}

	// Verify model file exists
	if _, err := os.Stat(modelPath); err != nil {
		return nil, fmt.Errorf("model file not found: %s", modelPath)
	}

	return &LLM{
		modelPath: modelPath,
		modelName: modelName,
		llamaCLI:  llamaCLI,
	}, nil
}

// Generate produces text based on a prompt
func (l *LLM) Generate(ctx context.Context, prompt string, maxTokens int) (string, error) {
	// Build llama-cli command
	// Using --no-display-prompt to avoid echoing the prompt in output
	cmd := exec.CommandContext(ctx, l.llamaCLI,
		"--model", l.modelPath,
		"--prompt", prompt,
		"--n-predict", fmt.Sprintf("%d", maxTokens),
		"--temp", "0.1",              // Low temperature for deterministic output
		"--top-p", "0.9",
		"--ctx-size", "4096",         // Context window
		"--threads", "4",             // CPU threads
		"--no-display-prompt",        // Don't echo prompt
		"--log-disable",              // Disable logging to stderr
		"--simple-io",                // Simplified I/O
		"--flash-attn",               // Enable flash attention for speed
	)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Run inference
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("llama-cli failed: %w\nstderr: %s", err, stderr.String())
	}

	// Parse output
	output := stdout.String()

	// Clean up output (remove any trailing whitespace or special tokens)
	output = strings.TrimSpace(output)
	output = strings.TrimSuffix(output, "</s>")
	output = strings.TrimSuffix(output, "<|endoftext|>")
	output = strings.TrimSpace(output)

	return output, nil
}

// Close releases model resources (no-op for CLI-based inference)
func (l *LLM) Close() {
	// Nothing to clean up for CLI-based inference
}

// GenerateOptions provides options for generation
type GenerateOptions struct {
	MaxTokens   int
	Temperature float64
	TopP        float64
	StopWords   []string
}

// DefaultGenerateOptions returns sensible defaults
func DefaultGenerateOptions() GenerateOptions {
	return GenerateOptions{
		MaxTokens:   512,
		Temperature: 0.1,  // Low temperature for deterministic output
		TopP:        0.9,
		StopWords:   []string{"</s>", "<|endoftext|>"},
	}
}
