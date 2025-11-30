package llm

import (
	"context"
)

// Provider is the interface for LLM backends
type Provider interface {
	// GenerateText generates text from a prompt
	GenerateText(ctx context.Context, prompt string) (string, error)

	// Name returns the provider name (e.g., "bedrock", "anthropic", "openai")
	Name() string
}

// SummaryRequest contains the data needed to generate a session summary
type SummaryRequest struct {
	SessionID       string
	ProjectPath     string
	Messages        []Message
	ExistingSummary string // Claude Code's native summary, if any
}

// Message is a simplified message for summarization
type Message struct {
	Type    string // "user" or "assistant"
	Content string
}
