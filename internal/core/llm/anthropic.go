package llm

import (
	"context"
	"fmt"
	"strings"

	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/llms/anthropic"
)

// DefaultAnthropicModel is the newest Haiku, used when no model is given.
const DefaultAnthropicModel = "claude-haiku-4-5-20251001"

// localAPIKey is sent when a custom base URL is set without a key. Local
// servers such as Ollama and LM Studio ignore it, but langchaingo refuses
// to build a client with an empty token.
const localAPIKey = "local"

// AnthropicProvider implements Provider using Anthropic's API directly
type AnthropicProvider struct {
	llm     *anthropic.LLM
	modelID string
}

// AnthropicConfig holds configuration for Anthropic provider
type AnthropicConfig struct {
	APIKey  string // Anthropic API key (required unless BaseURL is set)
	ModelID string // Model ID, defaults to DefaultAnthropicModel
	BaseURL string // Anthropic-compatible endpoint, e.g. http://localhost:11434 for Ollama (optional)
}

// NormalizeAnthropicBaseURL converts a base URL in the Anthropic SDK form
// (ANTHROPIC_BASE_URL=http://localhost:11434) to the form langchaingo uses,
// which includes the /v1 prefix. A URL already ending in /v1 is kept.
func NormalizeAnthropicBaseURL(baseURL string) string {
	u := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if u == "" || strings.HasSuffix(u, "/v1") {
		return u
	}
	return u + "/v1"
}

// NewAnthropicProvider creates a new Anthropic API provider
func NewAnthropicProvider(cfg AnthropicConfig) (*AnthropicProvider, error) {
	baseURL := NormalizeAnthropicBaseURL(cfg.BaseURL)
	if cfg.APIKey == "" {
		if baseURL == "" {
			return nil, fmt.Errorf("anthropic API key is required")
		}
		cfg.APIKey = localAPIKey
	}
	if cfg.ModelID == "" {
		cfg.ModelID = DefaultAnthropicModel
	}

	opts := []anthropic.Option{
		anthropic.WithToken(cfg.APIKey),
		anthropic.WithModel(cfg.ModelID),
	}
	if baseURL != "" {
		opts = append(opts, anthropic.WithBaseURL(baseURL))
	}

	llm, err := anthropic.New(opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create Anthropic LLM: %w", err)
	}

	return &AnthropicProvider{
		llm:     llm,
		modelID: cfg.ModelID,
	}, nil
}

// GenerateText implements Provider
func (p *AnthropicProvider) GenerateText(ctx context.Context, prompt string) (string, error) {
	response, err := llms.GenerateFromSinglePrompt(ctx, p.llm, prompt,
		llms.WithMaxTokens(1024),
		llms.WithTemperature(0.3),
	)
	if err != nil {
		return "", fmt.Errorf("anthropic generation failed: %w", err)
	}
	return response, nil
}

// Name implements Provider
func (p *AnthropicProvider) Name() string {
	return "anthropic"
}

// ModelID returns the model being used
func (p *AnthropicProvider) ModelID() string {
	return p.modelID
}
