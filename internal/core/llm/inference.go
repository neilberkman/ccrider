package llm

import (
	"context"
	"fmt"
	"os"
	"strings"

	llama "github.com/tcpipuk/llama-go"
)

// LLM wraps llama-go (tcpipuk) bindings for inference
type LLM struct {
	model       *llama.Model
	ctx         *llama.Context
	modelPath   string
	modelName   string
	contextSize int
}

// NewLLM creates a new LLM instance using llama-go CGO bindings
func NewLLM(modelPath string, modelName string) (*LLM, error) {
	// Verify model file exists
	if _, err := os.Stat(modelPath); err != nil {
		return nil, fmt.Errorf("model file not found: %s", modelPath)
	}

	// Load model with GPU acceleration enabled by default
	// The library automatically uses all available GPU layers
	model, err := llama.LoadModel(modelPath,
		// GPU layers are enabled by default, no need to specify
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load model: %w", err)
	}

	// Create execution context from the model
	// Auto-detect optimal context size based on system memory
	contextSize := GetOptimalContextSize()

	memGB, _ := GetSystemMemoryGB()
	fmt.Printf("System Memory: %d GB → Context Size: %dK tokens\n", memGB, contextSize/1024)

	ctx, err := model.NewContext(
		llama.WithContext(contextSize),
	)
	if err != nil {
		model.Close()
		return nil, fmt.Errorf("failed to create context: %w", err)
	}

	return &LLM{
		model:       model,
		ctx:         ctx,
		modelPath:   modelPath,
		modelName:   modelName,
		contextSize: contextSize,
	}, nil
}

// Generate produces text based on a prompt using llama-go
func (l *LLM) Generate(ctx context.Context, prompt string, maxTokens int) (string, error) {
	// Generate text using the context with options
	result, err := l.ctx.Generate(prompt,
		llama.WithMaxTokens(maxTokens),
		llama.WithTemperature(0.7),    // Higher temp to reduce repetition
		llama.WithTopP(0.95),
		llama.WithRepeatPenalty(1.1),  // Penalize repetition
		llama.WithStopWords("</s>", "<|endoftext|>", "<|eot_id|>"),
	)
	if err != nil {
		return "", fmt.Errorf("llama-go inference failed: %w", err)
	}

	// Clean up the response
	result = strings.TrimSpace(result)
	result = strings.TrimSuffix(result, "[end of text]")
	result = strings.TrimSpace(result)

	return result, nil
}

// Close releases model resources
func (l *LLM) Close() {
	if l.ctx != nil {
		l.ctx.Close()
	}
	if l.model != nil {
		l.model.Close()
	}
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
