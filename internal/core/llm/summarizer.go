package llm

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/neilberkman/ccrider/internal/core/models"
)

// Summarizer handles progressive session summarization
type Summarizer struct {
	llm              *LLM
	maxChunkTokens   int // Maximum tokens per chunk
	overlapTokens    int // Overlap between chunks for context (in tokens)
	maxMessageChars  int // Maximum chars per message to include
}

// SessionSummary represents a complete session summary
type SessionSummary struct {
	SessionID      int64
	OneLiner       string         // Ultra-short summary for list view (10-15 words)
	FullSummary    string         // Full detailed summary
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
	// Calculate safe chunk size based on model context window
	// Leave ~40% for prompt overhead and response generation
	maxChunkTokens := int(float64(llm.contextSize) * 0.40)

	// Minimum safety limit (for small context models)
	if maxChunkTokens < 2048 {
		maxChunkTokens = 2048
	}

	return &Summarizer{
		llm:             llm,
		maxChunkTokens:  maxChunkTokens,      // ~40% of context for chunk content
		overlapTokens:   maxChunkTokens / 10, // 10% overlap
		maxMessageChars: 4000,                // Truncate very long messages
	}
}

// SummarizeSession creates progressive summaries for a session
func (s *Summarizer) SummarizeSession(ctx context.Context, session *models.Session, messages []models.Message) (*SessionSummary, error) {
	if len(messages) == 0 {
		return nil, fmt.Errorf("no messages to summarize")
	}

	// Estimate total tokens
	totalTokens := s.estimateTokens(messages)

	// Quick mode for short sessions (single chunk)
	if totalTokens <= s.maxChunkTokens {
		fullSummary, err := s.summarizeChunk(ctx, messages, 0, "quick")
		if err != nil {
			return nil, err
		}

		// Generate one-liner from full summary
		oneLiner, err := s.generateOneLiner(ctx, fullSummary)
		if err != nil {
			// Fallback: use first sentence
			oneLiner = truncateToWords(fullSummary, 15)
		}

		return &SessionSummary{
			SessionID:    session.ID,
			OneLiner:     oneLiner,
			FullSummary:  fullSummary,
			Version:      1,
			MessageCount: len(messages),
		}, nil
	}

	// Progressive summarization for long sessions (token-based chunking)
	log.Printf("[SUMMARIZE] Session has %d messages, starting chunked summarization", len(messages))
	var chunks []ChunkSummary
	chunkIndex := 0
	i := 0

	for i < len(messages) {
		// Build chunk by accumulating messages until we hit token limit
		chunkStart := i
		currentTokens := 0
		chunkEnd := i

		// Include overlap from previous chunk
		overlapStart := chunkStart
		if chunkIndex > 0 && i > 0 {
			// Walk backwards to include overlap
			overlapTokens := 0
			for j := i - 1; j >= 0 && overlapTokens < s.overlapTokens; j-- {
				msgTokens := s.estimateMessageTokens(&messages[j])
				if overlapTokens+msgTokens > s.overlapTokens {
					break
				}
				overlapTokens += msgTokens
				overlapStart = j
			}
		}

		// Accumulate messages for this chunk
		for chunkEnd < len(messages) {
			msgTokens := s.estimateMessageTokens(&messages[chunkEnd])
			if currentTokens+msgTokens > s.maxChunkTokens && chunkEnd > chunkStart {
				break // Chunk is full
			}
			currentTokens += msgTokens
			chunkEnd++
		}

		// Must make progress
		if chunkEnd == chunkStart {
			chunkEnd = chunkStart + 1
		}

		// Summarize this chunk
		chunkMessages := messages[overlapStart:chunkEnd]
		log.Printf("[SUMMARIZE] Processing chunk %d (messages %d-%d, %d tokens)", chunkIndex, chunkStart, chunkEnd-1, currentTokens)
		summary, err := s.summarizeChunk(ctx, chunkMessages, chunkIndex, "detailed")
		if err != nil {
			return nil, fmt.Errorf("chunk %d: %w", chunkIndex, err)
		}
		log.Printf("[SUMMARIZE] Chunk %d complete", chunkIndex)

		chunks = append(chunks, ChunkSummary{
			Index:        chunkIndex,
			MessageRange: fmt.Sprintf("messages %d-%d", chunkStart, chunkEnd-1),
			Summary:      summary,
			TokensApprox: currentTokens,
		})

		i = chunkEnd
		chunkIndex++
	}

	// Combine chunks into final summary (returns both one-liner and full summary)
	log.Printf("[SUMMARIZE] Combining %d chunks into final summary", len(chunks))
	oneLiner, fullSummary, err := s.combineChunks(ctx, chunks, session)
	if err != nil {
		return nil, fmt.Errorf("final summary: %w", err)
	}
	log.Printf("[SUMMARIZE] Final summary complete. One-liner: %q", oneLiner)

	return &SessionSummary{
		SessionID:      session.ID,
		OneLiner:       oneLiner,
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
			msgText.WriteString(fmt.Sprintf("%s: %s\n\n", role, truncate(msg.TextContent, s.maxMessageChars)))
		}
	}

	if msgText.Len() == 0 {
		return "", fmt.Errorf("no text content to summarize")
	}

	prompt := BuildSummaryPrompt(msgText.String(), mode, chunkIndex)
	return s.llm.Generate(ctx, prompt, 512)
}

// combineChunks combines chunk summaries into a final summary
// Returns: (oneLiner, fullSummary, error)
func (s *Summarizer) combineChunks(ctx context.Context, chunks []ChunkSummary, session *models.Session) (string, string, error) {
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

	response, err := s.llm.Generate(ctx, prompt, 1024)
	if err != nil {
		return "", "", err
	}

	// Parse response: "1. ONE-LINER:\n[line]\n\n2. FULL SUMMARY:\n[summary]"
	oneLiner, fullSummary := parseTwoPartSummary(response)

	// Clean the one-liner of meta-commentary
	oneLiner = cleanOneLiner(oneLiner)

	// Fallback if parsing failed
	if oneLiner == "" {
		oneLiner = truncateToWords(fullSummary, 15)
	}
	if fullSummary == "" {
		fullSummary = response
		oneLiner = truncateToWords(response, 15)
	}

	return oneLiner, fullSummary, nil
}

// generateOneLiner generates a one-line summary from a full summary
// Uses iterative refinement if initial attempt is too long
func (s *Summarizer) generateOneLiner(ctx context.Context, fullSummary string) (string, error) {
	const maxLength = 80 // Max characters for one-liner (matches TUI display)
	const maxAttempts = 3

	var shortest string
	shortestLen := 999999

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		var prompt string
		if attempt == 1 {
			// First attempt: normal prompt
			prompt = BuildOneLinerPrompt(fullSummary)
		} else {
			// Refinement attempt: show them the truncated version
			truncated := truncateToLength(shortest, maxLength)
			prompt = BuildRefinementPrompt(shortest, truncated, maxLength)
		}

		oneLiner, err := s.llm.Generate(ctx, prompt, 64)
		if err != nil {
			return "", err
		}

		// Clean up meta-commentary
		cleaned := cleanOneLiner(strings.TrimSpace(oneLiner))

		// Track shortest attempt
		if len(cleaned) < shortestLen {
			shortest = cleaned
			shortestLen = len(cleaned)
		}

		// Success! It fits
		if len(cleaned) <= maxLength {
			return cleaned, nil
		}

		// Don't retry if we're not improving
		if attempt > 1 && len(cleaned) >= len(shortest) {
			break
		}
	}

	// Give up - use shortest attempt, truncated
	return truncateToLength(shortest, maxLength), nil
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

// parseTwoPartSummary extracts one-liner and full summary from LLM response
func parseTwoPartSummary(response string) (oneLiner, fullSummary string) {
	// Look for numbered markers
	lines := strings.Split(response, "\n")

	inOneLiner := false
	inFullSummary := false
	var oneLinerBuilder, fullSummaryBuilder strings.Builder

	for _, line := range lines {
		lineLower := strings.ToLower(strings.TrimSpace(line))

		// Detect section markers
		if strings.HasPrefix(lineLower, "1.") && strings.Contains(lineLower, "one-liner") {
			inOneLiner = true
			inFullSummary = false
			continue
		}
		if strings.HasPrefix(lineLower, "2.") && strings.Contains(lineLower, "full summary") {
			inOneLiner = false
			inFullSummary = true
			continue
		}

		// Collect content
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		if inOneLiner && oneLinerBuilder.Len() == 0 {
			// Take first non-empty line after "ONE-LINER" marker
			oneLinerBuilder.WriteString(trimmed)
		} else if inFullSummary {
			if fullSummaryBuilder.Len() > 0 {
				fullSummaryBuilder.WriteString("\n")
			}
			fullSummaryBuilder.WriteString(trimmed)
		}
	}

	return strings.TrimSpace(oneLinerBuilder.String()), strings.TrimSpace(fullSummaryBuilder.String())
}

// truncateToWords truncates text to approximately N words
func truncateToWords(s string, maxWords int) string {
	s = strings.TrimSpace(s)
	words := strings.Fields(s)
	if len(words) <= maxWords {
		return s
	}
	return strings.Join(words[:maxWords], " ") + "..."
}

// estimateTokens estimates total tokens for a slice of messages
func (s *Summarizer) estimateTokens(messages []models.Message) int {
	total := 0
	for i := range messages {
		total += s.estimateMessageTokens(&messages[i])
	}
	return total
}

// estimateMessageTokens estimates tokens for a single message
// Uses the same logic as summarizeChunk to ensure consistency
func (s *Summarizer) estimateMessageTokens(msg *models.Message) int {
	// Skip non-conversational messages
	if msg.Type != models.MessageTypeUser &&
		msg.Type != models.MessageTypeAssistant &&
		msg.Type != models.MessageTypeSystem {
		return 0
	}

	// Estimate based on text content length
	textLen := len(msg.TextContent)
	if textLen == 0 {
		return 0
	}

	// Truncate to max message chars
	if textLen > s.maxMessageChars {
		textLen = s.maxMessageChars
	}

	// Add overhead for role label and formatting ("User: ...\n\n")
	overhead := 20

	// Rough estimate: 1 token ≈ 4 characters for English text
	return (textLen + overhead) / 4
}

// truncateToLength truncates text to max characters, adding ellipsis
func truncateToLength(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

// cleanOneLiner removes LLM meta-commentary and extracts the actual summary
func cleanOneLiner(oneLiner string) string {
	lines := strings.Split(oneLiner, "\n")

	// Common junk patterns to skip
	junkPrefixes := []string{
		"the summary should be",
		"this summary is",
		"let me know",
		"here is",
		"—",
		"(",
		"[",
	}

	junkContains := []string{
		"words or less",
		"characters",
		"further assistance",
		"rewritten",
		"shortened",
	}

	// Find the first line that looks like an actual summary
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Skip lines that are clearly meta-commentary
		lineLower := strings.ToLower(line)
		isJunk := false

		for _, prefix := range junkPrefixes {
			if strings.HasPrefix(lineLower, prefix) {
				isJunk = true
				break
			}
		}

		if !isJunk {
			for _, contains := range junkContains {
				if strings.Contains(lineLower, contains) {
					isJunk = true
					break
				}
			}
		}

		if !isJunk && len(line) > 20 {
			// This looks like a real summary - clean it up
			line = strings.TrimSuffix(line, ".")
			line = strings.TrimSuffix(line, " ")
			line = strings.TrimPrefix(line, "- ")
			line = strings.TrimPrefix(line, "* ")
			return line
		}
	}

	// Fallback: return first substantive line
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if len(line) > 20 {
			return line
		}
	}

	return oneLiner
}
