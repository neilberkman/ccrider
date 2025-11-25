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
	SessionID    string
	ProjectPath  string
	Messages     []Message
	ExistingSummary string // Claude Code's native summary, if any
}

// Message is a simplified message for summarization
type Message struct {
	Type    string // "user" or "assistant"
	Content string
}

// Summarizer generates summaries using an LLM provider
type Summarizer struct {
	provider Provider
}

// NewSummarizer creates a new summarizer with the given provider
func NewSummarizer(provider Provider) *Summarizer {
	return &Summarizer{provider: provider}
}

// Summarize generates a summary for a session
func (s *Summarizer) Summarize(ctx context.Context, req SummaryRequest) (string, error) {
	prompt := buildSummaryPrompt(req)
	return s.provider.GenerateText(ctx, prompt)
}

func buildSummaryPrompt(req SummaryRequest) string {
	// Build conversation excerpt (first and last messages, key exchanges)
	var conversationText string
	maxMessages := 15 // Limit to avoid token overflow
	maxContentLen := 300 // Max chars per message

	messages := req.Messages
	if len(messages) > maxMessages {
		// Take first 3 and last 12 for context
		firstN := 3
		lastN := maxMessages - firstN
		messages = append(messages[:firstN], messages[len(messages)-lastN:]...)
	}

	for _, msg := range messages {
		role := "Human"
		if msg.Type == "assistant" {
			role = "Assistant"
		}
		// Truncate very long messages
		content := msg.Content
		if len(content) > maxContentLen {
			content = content[:maxContentLen] + "..."
		}
		conversationText += role + ": " + content + "\n\n"
	}

	prompt := `Summarize this Claude Code session in 1-2 sentences. Focus on what was accomplished or attempted.

Project: ` + req.ProjectPath + `

Conversation:
` + conversationText + `

Summary:`

	return prompt
}
