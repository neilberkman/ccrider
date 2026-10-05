package llm

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

// Provider names accepted by Resolve
const (
	ProviderAnthropic = "anthropic"
	ProviderBedrock   = "bedrock"
	ProviderOpenAI    = "openai"
	ProviderCodex     = "codex"
)

// ProviderNames lists every supported provider, for help and error text
var ProviderNames = []string{ProviderAnthropic, ProviderBedrock, ProviderOpenAI, ProviderCodex}

// Settings is one layer of user choices: command-line flags or config.toml.
type Settings struct {
	Provider string
	Model    string
	BaseURL  string
	Region   string
}

// Resolved is a fully determined provider configuration. Exactly one of the
// provider-specific configs is set, matching Provider.
type Resolved struct {
	Provider     string
	AutoDetected bool // Provider was chosen from available credentials
	Anthropic    *AnthropicConfig
	Bedrock      *BedrockConfig
	OpenAI       *OpenAIConfig
	Codex        *CodexConfig
}

// Resolve picks the provider and its settings. Flags win over config.toml.
// Model and base URL from config.toml only apply when config.toml names no
// provider or names the one being used, so a config written for one provider
// never leaks into another chosen with --provider.
//
// Auto-detection (no provider in flags or config) checks, in order:
// Anthropic (ANTHROPIC_API_KEY or ANTHROPIC_BASE_URL), AWS credentials for
// Bedrock, then OpenAI (OPENAI_API_KEY or OPENAI_BASE_URL). The codex
// provider is never auto-detected: it spends the user's ChatGPT plan usage,
// so it has to be chosen explicitly.
func Resolve(flags, file Settings, getenv func(string) string) (*Resolved, error) {
	provider := strings.ToLower(strings.TrimSpace(flags.Provider))
	if provider == "" {
		provider = strings.ToLower(strings.TrimSpace(file.Provider))
	}

	fileApplies := func(p string) bool {
		fp := strings.ToLower(strings.TrimSpace(file.Provider))
		return fp == "" || fp == p
	}
	pick := func(flag, fromFile string, p string) string {
		if flag != "" {
			return flag
		}
		if fileApplies(p) {
			return fromFile
		}
		return ""
	}

	r := &Resolved{}
	if provider == "" {
		if flags.BaseURL != "" || file.BaseURL != "" {
			return nil, fmt.Errorf("a base URL needs a provider: set --provider (or llm_provider) to %s or %s", ProviderOpenAI, ProviderAnthropic)
		}
		r.AutoDetected = true
		switch {
		case getenv("ANTHROPIC_API_KEY") != "" || getenv("ANTHROPIC_BASE_URL") != "":
			provider = ProviderAnthropic
		case hasAWSCredentials(getenv):
			provider = ProviderBedrock
		case getenv("OPENAI_API_KEY") != "" || getenv("OPENAI_BASE_URL") != "" || getenv("OPENAI_API_BASE") != "":
			provider = ProviderOpenAI
		default:
			return nil, fmt.Errorf("no LLM credentials found. Set ANTHROPIC_API_KEY or OPENAI_API_KEY, configure AWS credentials, " +
				"use --provider codex for your ChatGPT plan, or --provider openai --base-url for a local server")
		}
	}
	r.Provider = provider

	model := pick(flags.Model, file.Model, provider)
	baseURL := pick(flags.BaseURL, file.BaseURL, provider)

	switch provider {
	case ProviderAnthropic:
		// flag > env > config
		if flags.BaseURL == "" && getenv("ANTHROPIC_BASE_URL") != "" {
			baseURL = getenv("ANTHROPIC_BASE_URL")
		}
		key := getenv("ANTHROPIC_API_KEY")
		if key == "" && baseURL == "" {
			return nil, fmt.Errorf("ANTHROPIC_API_KEY not set (or set a base URL for an Anthropic-compatible local server)")
		}
		if model == "" {
			model = DefaultAnthropicModel
		}
		r.Anthropic = &AnthropicConfig{APIKey: key, ModelID: model, BaseURL: baseURL}

	case ProviderOpenAI:
		// flag > env > config
		if flags.BaseURL == "" {
			if v := firstNonEmpty(getenv("OPENAI_BASE_URL"), getenv("OPENAI_API_BASE")); v != "" {
				baseURL = v
			}
		}
		baseURL = NormalizeOpenAIBaseURL(baseURL)
		key := getenv("OPENAI_API_KEY")
		if key == "" && (baseURL == "" || baseURL == DefaultOpenAIBaseURL) {
			return nil, fmt.Errorf("OPENAI_API_KEY not set. For a local server pass --base-url (e.g. http://localhost:11434/v1 for Ollama); " +
				"for your ChatGPT plan use --provider codex")
		}
		if model == "" {
			return nil, fmt.Errorf("the openai provider has no default model: pass --model or set llm_model in config.toml")
		}
		r.OpenAI = &OpenAIConfig{APIKey: key, ModelID: model, BaseURL: baseURL}

	case ProviderBedrock:
		if baseURL != "" {
			return nil, fmt.Errorf("a base URL applies to the %s and %s providers, not bedrock", ProviderOpenAI, ProviderAnthropic)
		}
		region := firstNonEmpty(flags.Region, getenv("CCRIDER_AWS_REGION"), getenv("AWS_REGION"), getenv("AWS_DEFAULT_REGION"), "us-east-1")
		if model == "" {
			model = DefaultBedrockModel
		}
		r.Bedrock = &BedrockConfig{
			Region:          region,
			ModelID:         model,
			Profile:         firstNonEmpty(getenv("CCRIDER_AWS_PROFILE"), getenv("AWS_PROFILE")),
			AccessKeyID:     firstNonEmpty(getenv("CCRIDER_AWS_ACCESS_KEY_ID"), getenv("AWS_ACCESS_KEY_ID")),
			SecretAccessKey: firstNonEmpty(getenv("CCRIDER_AWS_SECRET_ACCESS_KEY"), getenv("AWS_SECRET_ACCESS_KEY")),
		}

	case ProviderCodex:
		if baseURL != "" {
			return nil, fmt.Errorf("a base URL applies to the %s and %s providers, not codex", ProviderOpenAI, ProviderAnthropic)
		}
		r.Codex = &CodexConfig{ModelID: model}

	default:
		return nil, fmt.Errorf("unknown provider: %s (use one of: %s)", provider, strings.Join(ProviderNames, ", "))
	}
	return r, nil
}

// NewProvider builds the provider described by r
func NewProvider(ctx context.Context, r *Resolved) (Provider, error) {
	switch {
	case r.Anthropic != nil:
		return NewAnthropicProvider(*r.Anthropic)
	case r.Bedrock != nil:
		return NewBedrockProvider(ctx, *r.Bedrock)
	case r.OpenAI != nil:
		return NewOpenAIProvider(*r.OpenAI)
	case r.Codex != nil:
		return NewCodexProvider(*r.Codex)
	}
	return nil, fmt.Errorf("no provider configured")
}

// NormalizeOpenAIBaseURL adds the /v1 prefix to a bare host URL such as
// http://localhost:11434, the most common mistake with local servers. A URL
// with any path is kept as given, because hosted OpenAI-compatible APIs use
// paths that don't end in /v1.
func NormalizeOpenAIBaseURL(baseURL string) string {
	u := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if u == "" {
		return ""
	}
	parsed, err := url.Parse(u)
	if err != nil || parsed.Host == "" {
		return u
	}
	if parsed.Path == "" {
		return u + "/v1"
	}
	return u
}

func hasAWSCredentials(getenv func(string) string) bool {
	return getenv("AWS_ACCESS_KEY_ID") != "" ||
		getenv("AWS_PROFILE") != "" ||
		getenv("AWS_DEFAULT_PROFILE") != "" ||
		getenv("CCRIDER_AWS_ACCESS_KEY_ID") != "" ||
		getenv("CCRIDER_AWS_PROFILE") != ""
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
