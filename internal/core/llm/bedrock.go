package llm

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/llms/bedrock"
)

// BedrockProvider implements Provider using AWS Bedrock
type BedrockProvider struct {
	llm     *bedrock.LLM
	modelID string
}

// BedrockConfig holds configuration for Bedrock provider
type BedrockConfig struct {
	Region          string // AWS region, defaults to us-east-1
	ModelID         string // Model ID, defaults to anthropic.claude-3-haiku-20240307-v1:0
	Profile         string // AWS profile name (optional)
	AccessKeyID     string // AWS access key ID (optional, for explicit creds)
	SecretAccessKey string // AWS secret access key (optional, for explicit creds)
}

// NewBedrockProvider creates a new Bedrock provider
func NewBedrockProvider(ctx context.Context, cfg BedrockConfig) (*BedrockProvider, error) {
	if cfg.Region == "" {
		cfg.Region = "us-east-1"
	}
	if cfg.ModelID == "" {
		// Default to Haiku for cost-effective summarization
		cfg.ModelID = "anthropic.claude-3-haiku-20240307-v1:0"
	}

	// Load AWS config
	var opts []func(*config.LoadOptions) error
	opts = append(opts, config.WithRegion(cfg.Region))
	if cfg.Profile != "" {
		opts = append(opts, config.WithSharedConfigProfile(cfg.Profile))
	}
	if cfg.AccessKeyID != "" && cfg.SecretAccessKey != "" {
		opts = append(opts, config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		))
	}
	awsCfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	// Create Bedrock runtime client
	client := bedrockruntime.NewFromConfig(awsCfg)

	// Create Bedrock LLM
	llm, err := bedrock.New(
		bedrock.WithModel(cfg.ModelID),
		bedrock.WithClient(client),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create Bedrock LLM: %w", err)
	}

	return &BedrockProvider{
		llm:     llm,
		modelID: cfg.ModelID,
	}, nil
}

// GenerateText implements Provider
func (p *BedrockProvider) GenerateText(ctx context.Context, prompt string) (string, error) {
	response, err := llms.GenerateFromSinglePrompt(ctx, p.llm, prompt,
		llms.WithMaxTokens(1024),
		llms.WithTemperature(0.3),
	)
	if err != nil {
		return "", fmt.Errorf("bedrock generation failed: %w", err)
	}
	return response, nil
}

// Name implements Provider
func (p *BedrockProvider) Name() string {
	return "bedrock"
}
