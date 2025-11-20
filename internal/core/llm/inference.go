package llm

import (
	"context"
	"fmt"
)

// LLM wraps a llama.cpp model for inference
// NOTE: Actual llama.cpp integration requires C++ build setup with Metal support.
// For Phase 1, this is a stub that allows the framework to compile and run.
// The metadata extraction and exact/FTS5 search work perfectly without LLM.
type LLM struct {
	modelPath string
	modelName string
}

// NewLLM creates a new LLM instance
func NewLLM(modelPath string, modelName string) (*LLM, error) {
	// Stub for Phase 1 - actual llama.cpp integration requires:
	// 1. go-llama.cpp Go bindings
	// 2. llama.cpp C++ library compiled with Metal support
	// 3. Model files downloaded from HuggingFace

	return &LLM{
		modelPath: modelPath,
		modelName: modelName,
	}, nil
}

// Generate produces text based on a prompt
func (l *LLM) Generate(ctx context.Context, prompt string, maxTokens int) (string, error) {
	// Stub for Phase 1
	// When implemented, this will use llama.cpp for local inference
	return "", fmt.Errorf("LLM inference not yet integrated - requires llama.cpp C++ library setup (Phase 2)")
}

// Close releases model resources
func (l *LLM) Close() {
	// Stub for Phase 1
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
