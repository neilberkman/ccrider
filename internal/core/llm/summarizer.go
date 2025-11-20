package llm

import (
	"context"
	"fmt"
	"strings"

	"github.com/neilberkman/ccrider/internal/core/models"
)

// Summarizer handles progressive session summarization
type Summarizer struct {
	llm         *LLM
	chunkSize   int // Messages per chunk
	overlapSize int // Overlap between chunks for context
}

// SessionSummary represents a complete session summary
type SessionSummary struct {
	SessionID      int64
	FullSummary    string
	ChunkSummaries []ChunkSummary
	Version        int
	MessageCount   int
}

// ChunkSummary represents a summary of a message chunk
type ChunkSummary struct {
	Index        int
	MessageRange string
	Summary      string
	TokensApprox int
}

// NewSummarizer creates a new summarizer
func NewSummarizer(llm *LLM) *Summarizer {
	return &Summarizer{
		llm:         llm,
		chunkSize:   100, // 100 messages per chunk
		overlapSize: 10,  // 10 message overlap
	}
}

// SummarizeSession creates progressive summaries for a session
func (s *Summarizer) SummarizeSession(ctx context.Context, session *models.Session, messages []models.Message) (*SessionSummary, error) {
	if len(messages) == 0 {
		return nil, fmt.Errorf("no messages to summarize")
	}

	// Quick mode for short sessions (single chunk)
	if len(messages) <= s.chunkSize {
		summary, err := s.summarizeChunk(ctx, messages, 0, "quick")
		if err != nil {
			return nil, err
		}

		return &SessionSummary{
			SessionID:    session.ID,
			FullSummary:  summary,
			Version:      1,
			MessageCount: len(messages),
		}, nil
	}

	// Progressive summarization for long sessions
	var chunks []ChunkSummary

	for i := 0; i < len(messages); i += s.chunkSize {
		end := min(i+s.chunkSize, len(messages))

		// Include overlap from previous chunk
		start := i
		if i > 0 {
			start = max(0, i-s.overlapSize)
		}

		chunkMessages := messages[start:end]
		summary, err := s.summarizeChunk(ctx, chunkMessages, i/s.chunkSize, "detailed")
		if err != nil {
			return nil, fmt.Errorf("chunk %d: %w", i/s.chunkSize, err)
		}

		chunks = append(chunks, ChunkSummary{
			Index:        i / s.chunkSize,
			MessageRange: fmt.Sprintf("messages %d-%d", i, end-1),
			Summary:      summary,
			TokensApprox: len(summary) / 4, // Rough estimate: 1 token ≈ 4 chars
		})
	}

	// Combine chunks into final summary
	fullSummary, err := s.combineChunks(ctx, chunks, session)
	if err != nil {
		return nil, fmt.Errorf("final summary: %w", err)
	}

	return &SessionSummary{
		SessionID:      session.ID,
		FullSummary:    fullSummary,
		ChunkSummaries: chunks,
		Version:        1,
		MessageCount:   len(messages),
	}, nil
}

// summarizeChunk summarizes a chunk of messages
func (s *Summarizer) summarizeChunk(ctx context.Context, messages []models.Message, chunkIndex int, mode string) (string, error) {
	// Build message context
	var msgText strings.Builder
	for _, msg := range messages {
		role := "User"
		if msg.Type == models.MessageTypeAssistant {
			role = "Claude"
		} else if msg.Type == models.MessageTypeSystem {
			role = "System"
		} else if msg.Type != models.MessageTypeUser {
			continue // Skip non-conversational messages
		}

		// Use text content (already extracted during import)
		if msg.TextContent != "" {
			msgText.WriteString(fmt.Sprintf("%s: %s\n\n", role, truncate(msg.TextContent, 2000)))
		}
	}

	if msgText.Len() == 0 {
		return "", fmt.Errorf("no text content to summarize")
	}

	prompt := BuildSummaryPrompt(msgText.String(), mode, chunkIndex)
	return s.llm.Generate(ctx, prompt, 512)
}

// combineChunks combines chunk summaries into a final summary
func (s *Summarizer) combineChunks(ctx context.Context, chunks []ChunkSummary, session *models.Session) (string, error) {
	var chunksText strings.Builder
	for _, chunk := range chunks {
		chunksText.WriteString(fmt.Sprintf("Part %d (%s):\n%s\n\n",
			chunk.Index+1, chunk.MessageRange, chunk.Summary))
	}

	messageCount := 0
	for _, chunk := range chunks {
		// Parse message count from range (e.g., "messages 0-99")
		var start, end int
		fmt.Sscanf(chunk.MessageRange, "messages %d-%d", &start, &end)
		messageCount = end + 1
	}

	prompt := BuildCombinedSummaryPrompt(
		chunksText.String(),
		session.ProjectPath,
		messageCount,
		len(chunks),
	)

	return s.llm.Generate(ctx, prompt, 1024)
}

// NeedsUpdate checks if a session needs re-summarization
func (s *Summarizer) NeedsUpdate(currentMessageCount, lastMessageCount int) bool {
	if lastMessageCount == 0 {
		return true // Never summarized
	}
	return currentMessageCount > lastMessageCount
}

// Helper functions
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
