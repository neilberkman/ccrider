package llm

import "fmt"

// BuildSummaryPrompt creates a prompt for summarizing a session
func BuildSummaryPrompt(messageText string, mode string, chunkIndex int) string {
	if mode == "quick" {
		return fmt.Sprintf(`Summarize this coding session in 2-4 sentences.

Session:
%s

Cover:
- Features, bugs, or systems worked on
- Technical problems and solutions
- Specific files, functions, or errors

Write factual technical content. State what was built or fixed.`, messageText)
	}

	return fmt.Sprintf(`Summarize part %d of a coding session.

%s

Cover:
- Features, bugs, or systems worked on
- Technical problems and solutions
- Specific files, functions, errors, or code changes
- How this connects to the overall work

Write factual technical content. State what was built or fixed.`, chunkIndex+1, messageText)
}

// BuildCombinedSummaryPrompt creates a prompt for combining chunk summaries
func BuildCombinedSummaryPrompt(chunksText, projectPath string, messageCount, chunkCount int) string {
	return fmt.Sprintf(`Read these summaries and write one short sentence (10-15 words) describing what was worked on.

Project: %s
Messages: %d across %d chunks

%s

Write one sentence stating what feature, bug, or system was the focus.`, projectPath, messageCount, chunkCount, chunksText)
}

// BuildOneLinerPrompt creates a prompt for generating just a one-line summary
func BuildOneLinerPrompt(fullSummary string) string {
	return fmt.Sprintf(`Create a one-line summary (10-15 words) of this session:

%s

Write a single plain sentence stating what feature/bug/system was the focus.
Use specific technical terms. No labels, no formatting.`, fullSummary)
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
