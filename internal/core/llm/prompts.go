package llm

import "fmt"

// BuildSummaryPrompt creates a prompt for summarizing a session
func BuildSummaryPrompt(messageText string, mode string, chunkIndex int) string {
	if mode == "quick" {
		return fmt.Sprintf(`Summarize this Claude Code session.

Session:
%s

Write a concise technical summary (2-4 sentences) covering:
- What was worked on
- Problems encountered and solutions
- Key files, functions, or issue IDs (e.g., ENA-1234)

Write only factual technical content. No pleasantries, greetings, or sign-offs.`, messageText)
	}

	return fmt.Sprintf(`Summarize part %d of a Claude Code session.

%s

Write a technical summary covering:
- Main activities and goals in this part
- Problems encountered and solutions
- Specific files, functions, errors, issue IDs
- How this part connects to the overall work

Write only factual technical content. No pleasantries or sign-offs.`, chunkIndex+1, messageText)
}

// BuildCombinedSummaryPrompt creates a prompt for combining chunk summaries
func BuildCombinedSummaryPrompt(chunksText, projectPath string, messageCount, chunkCount int) string {
	return fmt.Sprintf(`Summarize this Claude Code session based on the chunk summaries below.

Project: %s
Messages: %d across %d chunks

%s

Generate EXACTLY this format:

1. ONE-LINER:
<write one sentence of 10-15 words describing what was accomplished>

2. FULL SUMMARY:
<write 2-4 paragraphs describing: the main goal, problems solved, outcome, and key technical details>

Examples of good one-liners:
- "Fixed authentication bug in login.js by updating token validation"
- "Added real-time sync feature using WebSockets and Redis"
- "Refactored database schema to support multi-tenancy"

DO NOT include:
- Bullet points in the one-liner
- Headers, labels, or formatting markers
- Pleasantries like "Best regards" or "Let me know"
- Redundant text or repetition

Write the one-liner as a single plain sentence. Write the full summary as plain paragraphs.`, projectPath, messageCount, chunkCount, chunksText)
}

// BuildOneLinerPrompt creates a prompt for generating just a one-line summary
func BuildOneLinerPrompt(fullSummary string) string {
	return fmt.Sprintf(`Create a one-line summary (10-15 words max) of this session:

%s

Write ONLY the one-line summary as a single plain sentence. No labels, no bullet points, no formatting.`, fullSummary)
}

// BuildRefinementPrompt creates a prompt to shorten a too-long summary
func BuildRefinementPrompt(tooLong, truncated string, maxChars int) string {
	return fmt.Sprintf(`Your summary is too long. Here's what you wrote:

%s

Here's what it looks like truncated to %d characters (the max space available):

%s

Rewrite your summary to fit in %d characters or less. Keep the same meaning, just make it more concise.

Write ONLY the shortened summary:`, tooLong, maxChars, truncated, maxChars)
}

// BuildSearchPrompt creates a prompt for LLM-powered search
func BuildSearchPrompt(query string, summariesText string) string {
	return fmt.Sprintf(`You are helping search through Claude Code sessions.

User query: "%s"

Available sessions:
%s

List the top 5 most relevant session IDs that match the query.
Respond with ONLY a JSON array of session IDs in order of relevance:
["session_id_1", "session_id_2", "session_id_3"]

If fewer than 5 sessions are relevant, include only those that are actually relevant.`, query, summariesText)
}

// BuildInvestigationPrompt creates a prompt for deep cross-session investigation
func BuildInvestigationPrompt(query string, sessionSummaries string) string {
	return fmt.Sprintf(`You are investigating an issue across multiple Claude Code sessions.

Query: "%s"

Relevant session summaries:
%s

Provide a comprehensive analysis including:
1. Timeline of events (what happened and when)
2. Key findings and decisions made
3. Patterns or recurring issues
4. Technical details (files, functions, errors)
5. Current status and any open questions

Structure your response with clear headings and bullet points.`, query, sessionSummaries)
}
