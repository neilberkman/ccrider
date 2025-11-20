package search

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/neilberkman/ccrider/internal/core/db"
	"github.com/neilberkman/ccrider/internal/core/llm"
)

// SmartSearcher implements three-tier search: exact match → FTS5 → LLM
type SmartSearcher struct {
	database *db.DB
	llm      *llm.LLM
}

// SmartSearchResult represents a search result with relevance score
type SmartSearchResult struct {
	Session   db.Session
	Relevance float64
	Method    string // "exact", "fts5", or "llm"
	Summary   string // AI-generated summary if available
}

// NewSmartSearcher creates a new smart searcher
func NewSmartSearcher(database *db.DB, llm *llm.LLM) *SmartSearcher {
	return &SmartSearcher{
		database: database,
		llm:      llm,
	}
}

// Search performs intelligent three-tier search
func (s *SmartSearcher) Search(ctx context.Context, query string) ([]SmartSearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("query cannot be empty")
	}

	// Tier 1: Try exact match (issue ID or file path)
	if results, ok := s.tryExactMatch(query); ok && len(results) > 0 {
		return results, nil
	}

	// Tier 2: Try FTS5 keyword search
	if results, ok := s.tryFTS5Search(query); ok && len(results) >= 3 {
		return results, nil
	}

	// Tier 3: Use LLM over summaries
	return s.llmSearch(ctx, query)
}

// tryExactMatch attempts exact match on issue IDs and file paths
func (s *SmartSearcher) tryExactMatch(query string) ([]SmartSearchResult, bool) {
	// Check if query looks like an issue ID
	if isIssueIDPattern(query) {
		sessions, err := s.database.FindSessionsByIssueID(query)
		if err == nil && len(sessions) > 0 {
			results := make([]SmartSearchResult, len(sessions))
			for i, session := range sessions {
				results[i] = SmartSearchResult{
					Session:   session,
					Relevance: 1.0, // Exact match
					Method:    "exact",
				}
			}
			return results, true
		}
	}

	// Check if query looks like a file path
	if isFilePathPattern(query) {
		sessions, err := s.database.FindSessionsByFilePath(query)
		if err == nil && len(sessions) > 0 {
			results := make([]SmartSearchResult, len(sessions))
			for i, session := range sessions {
				results[i] = SmartSearchResult{
					Session:   session,
					Relevance: 1.0, // Exact match
					Method:    "exact",
				}
			}
			return results, true
		}
	}

	return nil, false
}

// tryFTS5Search attempts keyword search using FTS5
func (s *SmartSearcher) tryFTS5Search(query string) ([]SmartSearchResult, bool) {
	// Use existing SearchWithFilters to get session-grouped results
	filters := SearchFilters{
		Query: query,
	}

	sessionResults, err := SearchWithFilters(s.database, filters)
	if err != nil || len(sessionResults) == 0 {
		return nil, false
	}

	// Convert to SmartSearchResults
	results := make([]SmartSearchResult, 0, len(sessionResults))
	for _, sr := range sessionResults {
		// Convert db.Session for compatibility
		session := db.Session{
			SessionID:    sr.SessionID,
			Summary:      sr.SessionSummary,
			ProjectPath:  sr.ProjectPath,
			MessageCount: len(sr.Matches),
			// Note: UpdatedAt would need to be parsed from string, but we can skip for now
		}

		// Calculate relevance based on number of matches
		relevance := float64(len(sr.Matches)) / 10.0
		if relevance > 1.0 {
			relevance = 1.0
		}

		results = append(results, SmartSearchResult{
			Session:   session,
			Relevance: relevance,
			Method:    "fts5",
		})
	}

	return results, true
}

// llmSearch uses LLM to rank summaries
func (s *SmartSearcher) llmSearch(ctx context.Context, query string) ([]SmartSearchResult, error) {
	if s.llm == nil {
		return nil, fmt.Errorf("LLM not initialized")
	}

	// Load all summaries from database
	rows, err := s.database.Query(`
		SELECT s.id, s.session_id, s.project_path, s.updated_at,
		       COALESCE(ss.full_summary, s.summary, '') as summary
		FROM sessions s
		LEFT JOIN session_summaries ss ON s.id = ss.session_id
		WHERE s.message_count > 0
		ORDER BY s.updated_at DESC
		LIMIT 100
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to load summaries: %w", err)
	}
	defer rows.Close()

	type sessionSummary struct {
		ID        int64
		SessionID string
		Path      string
		UpdatedAt string
		Summary   string
	}

	var summaries []sessionSummary
	for rows.Next() {
		var s sessionSummary
		err := rows.Scan(&s.ID, &s.SessionID, &s.Path, &s.UpdatedAt, &s.Summary)
		if err != nil {
			return nil, fmt.Errorf("failed to scan summary: %w", err)
		}

		// Only include sessions with meaningful summaries
		if len(strings.TrimSpace(s.Summary)) > 20 {
			summaries = append(summaries, s)
		}
	}

	if len(summaries) == 0 {
		return nil, fmt.Errorf("no summaries available for LLM search")
	}

	// Build prompt for LLM
	var summariesText strings.Builder
	for i, s := range summaries {
		summary := s.Summary
		if len(summary) > 300 {
			summary = summary[:297] + "..."
		}
		summariesText.WriteString(fmt.Sprintf("%d. [%s] %s\n   %s\n\n",
			i+1, s.SessionID, s.Path, summary))
	}

	prompt := llm.BuildSearchPrompt(query, summariesText.String())

	// Generate response
	response, err := s.llm.Generate(ctx, prompt, 256)
	if err != nil {
		return nil, fmt.Errorf("LLM generation failed: %w", err)
	}

	// Parse session IDs from response
	sessionIDs := parseSessionIDsFromJSON(response)
	if len(sessionIDs) == 0 {
		return nil, fmt.Errorf("LLM did not return valid session IDs")
	}

	// Build results in ranked order
	var results []SmartSearchResult
	for i, sessionID := range sessionIDs {
		// Find the session in our summaries list
		for _, s := range summaries {
			if s.SessionID == sessionID {
				relevance := 1.0 - (float64(i) * 0.15) // Decay by rank
				if relevance < 0.1 {
					relevance = 0.1
				}

				results = append(results, SmartSearchResult{
					Session: db.Session{
						SessionID: s.SessionID,
						Summary:   s.Summary,
						ProjectPath: s.Path,
						// UpdatedAt would need parsing
					},
					Relevance: relevance,
					Method:    "llm",
				})
				break
			}
		}
	}

	return results, nil
}

// Helper functions

func isIssueIDPattern(s string) bool {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`^[A-Za-z]+-\d+$`), // ENA-123, ena-123
		regexp.MustCompile(`^#\d{2,}$`),       // #123
	}

	for _, pattern := range patterns {
		if pattern.MatchString(s) {
			return true
		}
	}

	return false
}

func isFilePathPattern(s string) bool {
	// File path usually has extension or slash
	return strings.Contains(s, "/") || strings.Contains(s, ".")
}

func parseSessionIDsFromJSON(response string) []string {
	// Try to find JSON array in response
	start := strings.Index(response, "[")
	end := strings.LastIndex(response, "]")
	if start == -1 || end == -1 || start >= end {
		return nil
	}

	jsonStr := response[start : end+1]

	// Parse JSON
	var sessionIDs []string
	if err := json.Unmarshal([]byte(jsonStr), &sessionIDs); err != nil {
		// Fallback: simple string parsing
		content := response[start+1 : end]
		parts := strings.Split(content, ",")

		for _, part := range parts {
			id := strings.Trim(strings.TrimSpace(part), `"'`)
			if id != "" {
				sessionIDs = append(sessionIDs, id)
			}
		}
	}

	return sessionIDs
}
