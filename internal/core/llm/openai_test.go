package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

type openAICapture struct {
	path, auth string
	body       map[string]any
}

func fakeOpenAIServer(t *testing.T, c *openAICapture) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.path = r.URL.Path
		c.auth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&c.body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"c1","object":"chat.completion","model":"qwen3",` +
			`"choices":[{"index":0,"message":{"role":"assistant","content":"local summary"},"finish_reason":"stop"}],` +
			`"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`))
	}))
}

func TestOpenAIProviderLocalServer(t *testing.T) {
	var c openAICapture
	srv := fakeOpenAIServer(t, &c)
	defer srv.Close()

	p, err := NewOpenAIProvider(OpenAIConfig{BaseURL: srv.URL + "/v1", ModelID: "qwen3"})
	if err != nil {
		t.Fatal(err)
	}
	out, err := p.GenerateText(context.Background(), "summarize this")
	if err != nil {
		t.Fatal(err)
	}
	if out != "local summary" {
		t.Errorf("GenerateText() = %q", out)
	}
	if c.path != "/v1/chat/completions" {
		t.Errorf("path = %q, want /v1/chat/completions", c.path)
	}
	if c.body["model"] != "qwen3" {
		t.Errorf("model = %v", c.body["model"])
	}
	if c.auth != "Bearer "+localAPIKey {
		t.Errorf("Authorization = %q, want placeholder", c.auth)
	}
	// Local servers read max_tokens, not max_completion_tokens
	if _, ok := c.body["max_tokens"]; !ok {
		t.Errorf("max_tokens missing from request: %v", c.body)
	}
	if _, ok := c.body["max_completion_tokens"]; ok {
		t.Errorf("max_completion_tokens sent to a local server: %v", c.body)
	}
}

func TestNewOpenAIProviderValidation(t *testing.T) {
	if _, err := NewOpenAIProvider(OpenAIConfig{APIKey: "k"}); err == nil {
		t.Error("want an error with no model")
	}
	if _, err := NewOpenAIProvider(OpenAIConfig{ModelID: "m"}); err == nil {
		t.Error("want an error for OpenAI itself with no key")
	}
}
