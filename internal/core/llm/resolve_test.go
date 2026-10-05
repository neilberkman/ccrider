package llm

import (
	"strings"
	"testing"
)

func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestResolveAutoDetectOrder(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"anthropic key", map[string]string{"ANTHROPIC_API_KEY": "k", "AWS_PROFILE": "p", "OPENAI_API_KEY": "o"}, ProviderAnthropic},
		{"anthropic base url", map[string]string{"ANTHROPIC_BASE_URL": "http://localhost:11434"}, ProviderAnthropic},
		{"aws before openai", map[string]string{"AWS_PROFILE": "p", "OPENAI_API_KEY": "o"}, ProviderBedrock},
		{"openai key", map[string]string{"OPENAI_API_KEY": "o"}, ProviderOpenAI},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// openai has no default model, so give one through config
			r, err := Resolve(Settings{}, Settings{Model: "m"}, envOf(tt.env))
			if err != nil {
				t.Fatal(err)
			}
			if r.Provider != tt.want || !r.AutoDetected {
				t.Errorf("got %s (auto=%v), want %s (auto)", r.Provider, r.AutoDetected, tt.want)
			}
		})
	}
}

func TestResolveNeverAutoDetectsCodex(t *testing.T) {
	_, err := Resolve(Settings{}, Settings{}, envOf(nil))
	if err == nil || !strings.Contains(err.Error(), "--provider codex") {
		t.Fatalf("want a no-credentials error that mentions --provider codex, got %v", err)
	}
}

func TestResolveBaseURLWithoutProviderIsAnError(t *testing.T) {
	if _, err := Resolve(Settings{BaseURL: "http://localhost:11434/v1"}, Settings{}, envOf(nil)); err == nil {
		t.Fatal("want an error for a base URL with no provider")
	}
}

func TestResolveDefaultModels(t *testing.T) {
	r, err := Resolve(Settings{Provider: "anthropic"}, Settings{}, envOf(map[string]string{"ANTHROPIC_API_KEY": "k"}))
	if err != nil {
		t.Fatal(err)
	}
	if r.Anthropic.ModelID != DefaultAnthropicModel {
		t.Errorf("anthropic model = %q", r.Anthropic.ModelID)
	}
	r, err = Resolve(Settings{Provider: "bedrock"}, Settings{}, envOf(nil))
	if err != nil {
		t.Fatal(err)
	}
	if r.Bedrock.ModelID != DefaultBedrockModel || r.Bedrock.Region != "us-east-1" {
		t.Errorf("bedrock = %+v", r.Bedrock)
	}
	r, err = Resolve(Settings{Provider: "codex"}, Settings{}, envOf(nil))
	if err != nil {
		t.Fatal(err)
	}
	if r.Codex.ModelID != "" {
		t.Errorf("codex model = %q, want empty (Codex CLI default)", r.Codex.ModelID)
	}
}

func TestResolveOpenAIRequiresModel(t *testing.T) {
	_, err := Resolve(Settings{Provider: "openai", BaseURL: "http://localhost:11434/v1"}, Settings{}, envOf(nil))
	if err == nil || !strings.Contains(err.Error(), "--model") {
		t.Fatalf("want a missing-model error, got %v", err)
	}
}

func TestResolveOpenAIKeyRules(t *testing.T) {
	// OpenAI itself needs a key
	if _, err := Resolve(Settings{Provider: "openai", Model: "m"}, Settings{}, envOf(nil)); err == nil {
		t.Error("want an error for OpenAI with no key")
	}
	// A local server doesn't
	r, err := Resolve(Settings{Provider: "openai", Model: "m", BaseURL: "http://localhost:11434"}, Settings{}, envOf(nil))
	if err != nil {
		t.Fatal(err)
	}
	if r.OpenAI.BaseURL != "http://localhost:11434/v1" {
		t.Errorf("base URL = %q, want /v1 added to the bare host", r.OpenAI.BaseURL)
	}
}

func TestResolveBaseURLPrecedence(t *testing.T) {
	env := envOf(map[string]string{"OPENAI_BASE_URL": "http://env:1/v1"})
	file := Settings{Provider: "openai", Model: "m", BaseURL: "http://file:1/v1"}

	r, err := Resolve(Settings{BaseURL: "http://flag:1/v1"}, file, env)
	if err != nil {
		t.Fatal(err)
	}
	if r.OpenAI.BaseURL != "http://flag:1/v1" {
		t.Errorf("flag should win, got %q", r.OpenAI.BaseURL)
	}

	r, err = Resolve(Settings{}, file, env)
	if err != nil {
		t.Fatal(err)
	}
	if r.OpenAI.BaseURL != "http://env:1/v1" {
		t.Errorf("env should beat config, got %q", r.OpenAI.BaseURL)
	}

	r, err = Resolve(Settings{}, file, envOf(nil))
	if err != nil {
		t.Fatal(err)
	}
	if r.OpenAI.BaseURL != "http://file:1/v1" {
		t.Errorf("config should apply, got %q", r.OpenAI.BaseURL)
	}
}

// config.toml set up for a local openai server must not leak its model or
// base URL into a provider picked with --provider.
func TestResolveConfigScopedToItsProvider(t *testing.T) {
	file := Settings{Provider: "openai", Model: "qwen3", BaseURL: "http://localhost:11434/v1"}
	r, err := Resolve(Settings{Provider: "codex"}, file, envOf(nil))
	if err != nil {
		t.Fatalf("codex should ignore openai's base URL from config: %v", err)
	}
	if r.Codex.ModelID != "" {
		t.Errorf("codex picked up config model %q", r.Codex.ModelID)
	}

	r, err = Resolve(Settings{Provider: "anthropic"}, file, envOf(map[string]string{"ANTHROPIC_API_KEY": "k"}))
	if err != nil {
		t.Fatal(err)
	}
	if r.Anthropic.ModelID != DefaultAnthropicModel || r.Anthropic.BaseURL != "" {
		t.Errorf("anthropic picked up openai config: %+v", r.Anthropic)
	}
}

func TestResolveBaseURLRejectedForBedrockAndCodex(t *testing.T) {
	for _, p := range []string{"bedrock", "codex"} {
		if _, err := Resolve(Settings{Provider: p, BaseURL: "http://localhost:1/v1"}, Settings{}, envOf(nil)); err == nil {
			t.Errorf("%s: want an error for --base-url", p)
		}
	}
}

func TestResolveUnknownProvider(t *testing.T) {
	if _, err := Resolve(Settings{Provider: "gemini"}, Settings{}, envOf(nil)); err == nil {
		t.Fatal("want an error for an unknown provider")
	}
}

func TestNormalizeOpenAIBaseURL(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", ""},
		{"http://localhost:11434", "http://localhost:11434/v1"},
		{"http://localhost:11434/", "http://localhost:11434/v1"},
		{"http://localhost:1234/v1", "http://localhost:1234/v1"},
		{"https://api.groq.com/openai/v1", "https://api.groq.com/openai/v1"},
		{"https://generativelanguage.googleapis.com/v1beta/openai", "https://generativelanguage.googleapis.com/v1beta/openai"},
	}
	for _, tt := range tests {
		if got := NormalizeOpenAIBaseURL(tt.in); got != tt.want {
			t.Errorf("NormalizeOpenAIBaseURL(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
