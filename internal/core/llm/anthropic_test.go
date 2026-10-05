package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNormalizeAnthropicBaseURL(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"", ""},
		{"  ", ""},
		{"http://localhost:11434", "http://localhost:11434/v1"},
		{"http://localhost:11434/", "http://localhost:11434/v1"},
		{"http://localhost:1234/v1", "http://localhost:1234/v1"},
		{"http://localhost:1234/v1/", "http://localhost:1234/v1"},
		{"https://proxy.example.com/anthropic", "https://proxy.example.com/anthropic/v1"},
	}
	for _, tt := range tests {
		if got := NormalizeAnthropicBaseURL(tt.in); got != tt.want {
			t.Errorf("NormalizeAnthropicBaseURL(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestNewAnthropicProviderRequiresKeyWithoutBaseURL(t *testing.T) {
	if _, err := NewAnthropicProvider(AnthropicConfig{}); err == nil {
		t.Fatal("expected an error with no API key and no base URL")
	}
}

func TestNewAnthropicProviderDefaultsToHaiku(t *testing.T) {
	p, err := NewAnthropicProvider(AnthropicConfig{APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	if p.ModelID() != DefaultAnthropicModel {
		t.Errorf("ModelID() = %q, want %q", p.ModelID(), DefaultAnthropicModel)
	}
}

// A local server reached through the SDK-style base URL (no /v1) receives
// POST /v1/messages with the requested model and no real key.
func TestAnthropicProviderLocalBaseURL(t *testing.T) {
	var gotPath, gotModel, gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.Header.Get("x-api-key")
		var body struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotModel = body.Model
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","model":"qwen3",` +
			`"content":[{"type":"text","text":"local summary"}],` +
			`"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":2}}`))
	}))
	defer srv.Close()

	p, err := NewAnthropicProvider(AnthropicConfig{BaseURL: srv.URL, ModelID: "qwen3"})
	if err != nil {
		t.Fatal(err)
	}
	out, err := p.GenerateText(context.Background(), "summarize this")
	if err != nil {
		t.Fatal(err)
	}

	if out != "local summary" {
		t.Errorf("GenerateText() = %q, want %q", out, "local summary")
	}
	if gotPath != "/v1/messages" {
		t.Errorf("request path = %q, want /v1/messages", gotPath)
	}
	if gotModel != "qwen3" {
		t.Errorf("request model = %q, want qwen3", gotModel)
	}
	if gotKey != localAPIKey {
		t.Errorf("x-api-key = %q, want placeholder %q", gotKey, localAPIKey)
	}
}
