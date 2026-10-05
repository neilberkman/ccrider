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

	prompt := fmt.Sprintf(`Summarize this coding session. Focus on PROBLEM→SOLUTION, not activities.

Project: %s

Conversation:
%s

YOUR TASK: Identify the MAIN PROBLEM and how it was SOLVED.

Ask yourself:
1. What specific problem or bug was being fixed?
2. What was the root cause?
3. What was the solution implemented?
4. Was it completed successfully?

RULES:
- Lead with the PROBLEM and SOLUTION, not activities
- Include specific technical details: error messages, function names, root causes
- NO meta-language: "worked on", "investigated", "the user"
- NO project name prefix (we already know the project)

BAD ONE_LINE examples:
- "Fixed a migration issue that caused errors" (vague)
- "The user investigated email notification issues" (meta-language)
- "MyProject: authentication improvements" (includes project name)

GOOD ONE_LINE examples:
- "Unique constraint violation on accounts(company_id,email) - added dedupe migration"
- "ProposalEmail unlinked from notifications - fixed association in email_processor.ex"
- "API timeout on /users endpoint - added Redis caching layer"

Provide TWO summaries:
1. ONE_LINE: 50-90 chars. Problem→Solution format. Specific. No filler.
2. FULL: 2-3 paragraphs. What broke, why, how it was fixed, outcome.

Format:
ONE_LINE: <summary>
FULL: <summary>`, projectName, conversationText)

	response, err := s.generate(ctx, prompt)
	if err != nil {
		return "", "", 0, err
	}

	oneLine, full = parseSummaryResponse(response)
	tokens = estimateTokens(conversationText) + estimateTokens(response)

	return oneLine, full, tokens, nil
}

// generate calls the provider and drops any reasoning the model returned
// inline, so it never reaches the summary parser.
func (s *HierarchicalSummarizer) generate(ctx context.Context, prompt string) (string, error) {
	response, err := s.provider.GenerateText(ctx, prompt)
	if err != nil {
		return "", err
	}
	return stripReasoning(response), nil
}

// thinkBlockRe matches a reasoning block as emitted inline by local
// reasoning models (Qwen3, DeepSeek-R1, QwQ) on servers that don't split it
// into a separate field.
var thinkBlockRe = regexp.MustCompile(`(?is)<(think|thinking|reasoning)>.*?</(think|thinking|reasoning)>`)

// stripReasoning removes inline reasoning blocks. Some chat templates open
// the block in the prompt, so the response holds only the closing tag; in
// that case everything up to the last closing tag is reasoning.
func stripReasoning(response string) string {
	out := thinkBlockRe.ReplaceAllString(response, "")
	lower := strings.ToLower(out)
	for _, tag := range []string{"</think>", "</thinking>", "</reasoning>"} {
		if i := strings.LastIndex(lower, tag); i >= 0 {
			out = out[i+len(tag):]
			lower = lower[i+len(tag):]
		}
	}
	return strings.TrimSpace(out)
}

// summarizeChunk summarizes a single chunk of messages
func (s *HierarchicalSummarizer) summarizeChunk(ctx context.Context, projectPath string, messages []Message, chunkIndex, totalChunks int) (string, int, error) {
	conversationText := formatMessages(messages)
	projectName := filepath.Base(projectPath)

	prompt := fmt.Sprintf(`Summarize this portion of a coding session (chunk %d of %d).

Project: %s

Conversation:
%s

Focus on: What PROBLEM was being addressed? What PROGRESS was made? What was CHANGED?

RULES:
- Be specific: include error messages, function names, file paths
- NO meta-language: "the user", "the assistant", "worked on"
- Capture the problem/solution arc, not just activities

Provide 1-2 paragraphs covering: the specific problem being solved, root cause if discovered, changes made, whether this chunk completed the fix or it continues.`, chunkIndex+1, totalChunks, projectName, conversationText)

	response, err := s.generate(ctx, prompt)
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

	prompt := fmt.Sprintf(`Synthesize these coding session summaries into a final summary.

Project: %s

Individual Part Summaries:
%s

YOUR TASK: Identify the MAIN PROBLEM being solved and whether it was SOLVED.

Ask yourself:
1. What specific problem or bug was the user trying to fix?
2. What was the root cause discovered?
3. What was the solution/fix implemented?
4. Was it successfully completed?

RULES:
- Lead with the PROBLEM and SOLUTION, not activities
- Include specific technical details: error messages, function names, root causes
- NO meta-language: "worked on", "investigated", "the session covered"
- NO project name prefix (we already know the project)

BAD ONE_LINE examples:
- "CCRider: fixed session resume exit 131 via Validation" (vague, includes project name)
- "Fixed migration issues and investigated problems" (no specifics)
- "Worked on authentication bug fixes" (meta-language)

GOOD ONE_LINE examples:
- "Pre-flight validation catches asdf/mise failures before claude --resume"
- "Exit 131 on resume: .tool-versions nodejs mismatch - added ValidateClaudeRunnable()"
- "TUI stderr corruption from importer warnings - silenced during render"

Provide TWO summaries:
1. ONE_LINE: 50-90 chars. Problem→Solution format. Specific. No filler.
2. FULL: 2-3 paragraphs. What broke, why, how it was fixed, outcome.

Format:
ONE_LINE: <summary>
FULL: <summary>`, projectName, combinedChunks)

	response, err := s.generate(ctx, prompt)
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
