package llm

import "fmt"

// BuildSummaryPrompt creates a prompt for summarizing a session
func BuildSummaryPrompt(messageText string, mode string, chunkIndex int) string {
	if mode == "quick" {
		return fmt.Sprintf(`You are summarizing a Claude Code session. Be specific about:
- What was being worked on
- What problems were encountered
- What solutions were found
- Key files, functions, or technical details mentioned
- Any issue IDs mentioned (e.g., ena-6530, ENA-1234, #123)

Session:
%s

Provide a concise technical summary (2-4 sentences):`, messageText)
	}

	return fmt.Sprintf(`This is part %d of a longer Claude Code session. Summarize this segment:

%s

Focus on:
- Main activities and goals in this part
- Problems encountered and solutions
- Specific files, functions, errors mentioned
- Issue IDs (e.g., ena-6530, ENA-1234, #123)
- How this part connects to the overall work

Provide a clear technical summary:`, chunkIndex+1, messageText)
}

// BuildCombinedSummaryPrompt creates a prompt for combining chunk summaries
func BuildCombinedSummaryPrompt(chunksText, projectPath string, messageCount, chunkCount int) string {
	return fmt.Sprintf(`You are summarizing a Claude Code session. Below are summaries of different parts.

Session Info:
- Project: %s
- Duration: %d messages across %d chunks

Chunk Summaries:
%s

Create a concise overall summary that captures:
1. What was the main goal/task?
2. What problems were solved?
3. What was the final outcome?
4. Key technical details (file names, functions, technologies, issue IDs)

Be specific and technical. Include actual names of files, functions, and errors.

Summary:`, projectPath, messageCount, chunkCount, chunksText)
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
