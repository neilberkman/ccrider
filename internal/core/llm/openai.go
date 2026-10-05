package llm

import (
	"context"
	"fmt"
	"strings"

	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/llms/openai"
)

// DefaultOpenAIBaseURL is OpenAI's own API. Any other base URL is treated
// as a self-hosted OpenAI-compatible server (Ollama, LM Studio, llama.cpp,
// vLLM, LocalAI).
const DefaultOpenAIBaseURL = "https://api.openai.com/v1"

// OpenAIProvider implements Provider for OpenAI and any server with an
// OpenAI-compatible /v1/chat/completions endpoint.
type OpenAIProvider struct {
	llm     *openai.LLM
	modelID string
	baseURL string
}

// OpenAIConfig holds configuration for the OpenAI-compatible provider
type OpenAIConfig struct {
	APIKey  string // API key (required for OpenAI itself, optional for a custom BaseURL)
	ModelID string // Model ID (required; there is no default that every server has)
	BaseURL string // Endpoint including /v1, e.g. http://localhost:11434/v1 for Ollama; defaults to DefaultOpenAIBaseURL
}

// NewOpenAIProvider creates a new OpenAI-compatible provider
func NewOpenAIProvider(cfg OpenAIConfig) (*OpenAIProvider, error) {
	if cfg.ModelID == "" {
		return nil, fmt.Errorf("the openai provider needs a model ID")
	}
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		baseURL = DefaultOpenAIBaseURL
	}
	if cfg.APIKey == "" {
		if baseURL == DefaultOpenAIBaseURL {
			return nil, fmt.Errorf("OpenAI API key is required")
		}
		cfg.APIKey = localAPIKey
	}

	llm, err := openai.New(
		openai.WithToken(cfg.APIKey),
		openai.WithModel(cfg.ModelID),
		openai.WithBaseURL(baseURL),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create OpenAI LLM: %w", err)
	}

	return &OpenAIProvider{
		llm:     llm,
		modelID: cfg.ModelID,
		baseURL: baseURL,
	}, nil
}

// GenerateText implements Provider
func (p *OpenAIProvider) GenerateText(ctx context.Context, prompt string) (string, error) {
	opts := []llms.CallOption{
		llms.WithMaxTokens(1024),
		llms.WithTemperature(0.3),
	}
	// OpenAI deprecated max_tokens in favor of max_completion_tokens, but
	// self-hosted servers read max_tokens and ignore the newer field.
	if p.baseURL != DefaultOpenAIBaseURL {
		opts = append(opts, openai.WithLegacyMaxTokensField())
	}
	response, err := llms.GenerateFromSinglePrompt(ctx, p.llm, prompt, opts...)
	if err != nil {
		return "", fmt.Errorf("openai generation failed: %w", err)
	}
	return response, nil
}

// Name implements Provider
func (p *OpenAIProvider) Name() string {
	return "openai"
}

// ModelID returns the model being used
func (p *OpenAIProvider) ModelID() string {
	return p.modelID
}

// BaseURL returns the endpoint being used
func (p *OpenAIProvider) BaseURL() string {
	return p.baseURL
}
