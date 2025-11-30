package llm

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/neilberkman/ccrider/internal/core/db"
)

const (
	// ChunkSize is the number of messages per chunk for progressive summarization
	ChunkSize = 100
	// ChunkOverlap provides context continuity between chunks
	ChunkOverlap = 10
	// MaxContentLen truncates very long individual messages
	MaxContentLen = 500
)

// HierarchicalSummarizer implements progressive chunk-based summarization
type HierarchicalSummarizer struct {
	provider Provider
}

// NewHierarchicalSummarizer creates a new hierarchical summarizer
func NewHierarchicalSummarizer(provider Provider) *HierarchicalSummarizer {
	return &HierarchicalSummarizer{provider: provider}
}

// SummarizeSession generates a hierarchical summary for a session
func (s *HierarchicalSummarizer) SummarizeSession(ctx context.Context, req SummaryRequest) (*db.SessionSummary, error) {
	messages := req.Messages
	msgCount := len(messages)

	if msgCount == 0 {
		return nil, fmt.Errorf("no messages to summarize")
	}

	var summary db.SessionSummary
	summary.MessageCount = msgCount

	// Short sessions: single pass
	if msgCount <= ChunkSize {
		oneLine, full, tokens, err := s.summarizeDirect(ctx, req.ProjectPath, messages)
		if err != nil {
			return nil, err
		}
		summary.OneLine = oneLine
		summary.Full = full
		summary.TokensApprox = tokens
		return &summary, nil
	}

	// Long sessions: chunk and combine
	chunks := s.chunkMessages(messages)
	var chunkSummaries []db.ChunkSummary

	for i, chunk := range chunks {
		chunkText, tokens, err := s.summarizeChunk(ctx, req.ProjectPath, chunk.messages, i, len(chunks))
		if err != nil {
			return nil, fmt.Errorf("summarize chunk %d: %w", i, err)
		}
		chunkSummaries = append(chunkSummaries, db.ChunkSummary{
			ChunkIndex:   i,
			MessageStart: chunk.startSeq,
			MessageEnd:   chunk.endSeq,
			Summary:      chunkText,
			TokensApprox: tokens,
		})
	}

	// Combine chunk summaries into final summary
	oneLine, full, tokens, err := s.combineChunks(ctx, req.ProjectPath, chunkSummaries)
	if err != nil {
		return nil, fmt.Errorf("combine chunks: %w", err)
	}

	summary.OneLine = oneLine
	summary.Full = full
	summary.TokensApprox = tokens
	summary.ChunkSummaries = chunkSummaries

	return &summary, nil
}

type messageChunk struct {
	messages []Message
	startSeq int
	endSeq   int
}

func (s *HierarchicalSummarizer) chunkMessages(messages []Message) []messageChunk {
	var chunks []messageChunk
	for i := 0; i < len(messages); i += ChunkSize - ChunkOverlap {
		end := i + ChunkSize
		if end > len(messages) {
			end = len(messages)
		}
		chunks = append(chunks, messageChunk{
			messages: messages[i:end],
			startSeq: i,
			endSeq:   end - 1,
		})
		if end >= len(messages) {
			break
		}
	}
	return chunks
}

// summarizeDirect handles short sessions with a single LLM call
func (s *HierarchicalSummarizer) summarizeDirect(ctx context.Context, projectPath string, messages []Message) (oneLine, full string, tokens int, err error) {
	conversationText := formatMessages(messages)
	projectName := filepath.Base(projectPath)

	prompt := fmt.Sprintf(`Summarize this coding session. Focus on the TOPIC, not what happened.

Project: %s

Conversation:
%s

RULES:
- NO meta-descriptions like "The user worked on..." or "Fixed a bug that..."
- Include specific identifiers: issue IDs (ENA-1234), table/schema names, function names, error messages
- Be technical and specific, not vague
- Lead with the key entity or topic

BAD: "Fixed a migration issue that caused a unique constraint violation"
GOOD: "ENA-6962: accounts table unique constraint on (company_id, email) - dedupe migration"

BAD: "The user investigated email notification issues"
GOOD: "Unlinked email notifications: ProposalEmail association logic in email_processor.ex"

Provide TWO summaries:
1. ONE_LINE: 60-80 chars. Topic-focused, specific identifiers, no filler words.
2. FULL: 2-3 paragraphs with technical details, file paths, specific changes.

Format:
ONE_LINE: <summary>
FULL: <summary>`, projectName, conversationText)

	response, err := s.provider.GenerateText(ctx, prompt)
	if err != nil {
		return "", "", 0, err
	}

	oneLine, full = parseSummaryResponse(response)
	tokens = estimateTokens(conversationText) + estimateTokens(response)

	return oneLine, full, tokens, nil
}

// summarizeChunk summarizes a single chunk of messages
func (s *HierarchicalSummarizer) summarizeChunk(ctx context.Context, projectPath string, messages []Message, chunkIndex, totalChunks int) (string, int, error) {
	conversationText := formatMessages(messages)
	projectName := filepath.Base(projectPath)

	prompt := fmt.Sprintf(`Summarize this portion of a coding session (chunk %d of %d).

Project: %s

Conversation:
%s

RULES:
- Focus on WHAT was worked on, not that work happened
- Include specific identifiers: issue IDs, table names, function names, file paths
- Be technical and specific
- NO meta-language like "the user" or "the assistant"

Provide 1-2 paragraphs covering: specific files changed, functions modified, bugs fixed (with specifics), schema/data changes.`, chunkIndex+1, totalChunks, projectName, conversationText)

	response, err := s.provider.GenerateText(ctx, prompt)
	if err != nil {
		return "", 0, err
	}

	tokens := estimateTokens(conversationText) + estimateTokens(response)
	return strings.TrimSpace(response), tokens, nil
}

// combineChunks combines chunk summaries into a final summary
func (s *HierarchicalSummarizer) combineChunks(ctx context.Context, projectPath string, chunks []db.ChunkSummary) (oneLine, full string, tokens int, err error) {
	var chunkTexts []string
	for _, c := range chunks {
		chunkTexts = append(chunkTexts, fmt.Sprintf("Part %d (messages %d-%d):\n%s", c.ChunkIndex+1, c.MessageStart, c.MessageEnd, c.Summary))
	}
	combinedChunks := strings.Join(chunkTexts, "\n\n")
	projectName := filepath.Base(projectPath)

	prompt := fmt.Sprintf(`Combine these summaries into one coherent summary.

Project: %s

Individual Part Summaries:
%s

RULES:
- NO meta-descriptions like "The user worked on..." or "This session covered..."
- Include specific identifiers: issue IDs (ENA-1234), table/schema names, function names
- Lead with the primary topic or issue
- Be technical and specific

BAD: "Fixed migration issues and investigated email problems"
GOOD: "ENA-6962: dedupe migration for accounts(company_id,email); email association in ProposalEmail"

Provide TWO summaries:
1. ONE_LINE: 60-80 chars. Primary topic with key identifiers. No filler.
2. FULL: 2-3 paragraphs synthesizing all technical details, files, changes.

Format:
ONE_LINE: <summary>
FULL: <summary>`, projectName, combinedChunks)

	response, err := s.provider.GenerateText(ctx, prompt)
	if err != nil {
		return "", "", 0, err
	}

	oneLine, full = parseSummaryResponse(response)
	tokens = estimateTokens(combinedChunks) + estimateTokens(response)

	return oneLine, full, tokens, nil
}

func formatMessages(messages []Message) string {
	var sb strings.Builder
	for _, msg := range messages {
		role := "Human"
		if msg.Type == "assistant" {
			role = "Assistant"
		}
		content := msg.Content
		if len(content) > MaxContentLen {
			content = content[:MaxContentLen] + "..."
		}
		sb.WriteString(role)
		sb.WriteString(": ")
		sb.WriteString(content)
		sb.WriteString("\n\n")
	}
	return sb.String()
}

func parseSummaryResponse(response string) (oneLine, full string) {
	response = strings.TrimSpace(response)

	// Try to parse ONE_LINE: and FULL: format
	oneLineRe := regexp.MustCompile(`(?i)ONE_LINE:\s*(.+?)(?:\n|FULL:|$)`)
	fullRe := regexp.MustCompile(`(?i)FULL:\s*([\s\S]+)$`)

	if matches := oneLineRe.FindStringSubmatch(response); len(matches) > 1 {
		oneLine = strings.TrimSpace(matches[1])
	}
	if matches := fullRe.FindStringSubmatch(response); len(matches) > 1 {
		full = strings.TrimSpace(matches[1])
	}

	// Fallback: if parsing failed, use whole response for both
	if oneLine == "" {
		lines := strings.SplitN(response, "\n", 2)
		oneLine = strings.TrimSpace(lines[0])
		if len(oneLine) > 100 {
			oneLine = oneLine[:97] + "..."
		}
	}

	// Clean up bad patterns from one-line summary
	oneLine = cleanSummary(oneLine)
	if full == "" {
		full = response
	}

	return oneLine, full
}

// cleanSummary removes common bad patterns from summaries
func cleanSummary(s string) string {
	// Strip common meta-prefixes
	badPrefixes := []string{
		"Based on the conversation,",
		"Based on this conversation,",
		"The key topics covered were:",
		"The key topics covered were",
		"Key topics covered:",
		"The user and assistant",
		"The user ",
		"The assistant ",
		"In this session,",
		"This session covers",
		"This session involved",
		"Investigated ",
		"Explored ",
	}

	s = strings.TrimSpace(s)
	for _, prefix := range badPrefixes {
		if strings.HasPrefix(strings.ToLower(s), strings.ToLower(prefix)) {
			s = strings.TrimSpace(s[len(prefix):])
			// Capitalize first letter
			if len(s) > 0 {
				s = strings.ToUpper(s[:1]) + s[1:]
			}
		}
	}

	return s
}

func estimateTokens(text string) int {
	// Rough estimate: ~4 chars per token
	return len(text) / 4
}
