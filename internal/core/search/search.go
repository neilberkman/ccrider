package search

import (
	"database/sql"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/neilberkman/ccrider/internal/core/db"
)

// SearchResult represents a single search result
type SearchResult struct {
	MessageUUID    string
	SessionID      string
	SessionSummary string
	MessageText    string
	Timestamp      string
	ProjectPath    string
}

// SearchFilters defines filtering criteria for search
type SearchFilters struct {
	Query            string // The search query text
	ProjectPath      string // Filter by project path (substring match)
	CurrentSessionID string // If set, only search within this session
	ExcludeCurrent   bool   // If true with CurrentSessionID set, exclude that session
	AfterDate        string // Only results after this timestamp (ISO 8601)
	BeforeDate       string // Only results before this timestamp (ISO 8601)
}

// SessionSearchResult represents search results grouped by session
type SessionSearchResult struct {
	SessionID      string
	SessionSummary string
	ProjectPath    string
	UpdatedAt      string
	Matches        []SearchResult
	Score          float64 // Relevance score for ranking
}

// Default sort order for search results (most recent first)
const defaultOrderBy = "m.timestamp DESC"

// Search performs a full-text search using the natural language FTS table
// Results are ordered by timestamp (most recent first)
func Search(database *db.DB, query string) ([]SearchResult, error) {
	return search(database, query, "messages_fts", 1000)
}

// SearchWithFilters performs filtered search and groups results by session
// This consolidates business logic that was duplicated across TUI and MCP
func SearchWithFilters(database *db.DB, filters SearchFilters) ([]SessionSearchResult, error) {
	// Validate query (minimum 2 characters)
	query := strings.TrimSpace(filters.Query)
	if len(query) < 2 {
		return nil, nil // Empty results for queries too short
	}

	// Perform core search
	results, err := Search(database, query)
	if err != nil {
		return nil, err
	}

	// Apply filters (business logic)
	sessionMap := make(map[string]*SessionSearchResult)
	var sessionOrder []string

	for _, result := range results {
		// Filter by current session
		if filters.CurrentSessionID != "" {
			if filters.ExcludeCurrent && result.SessionID == filters.CurrentSessionID {
				continue
			}
			if !filters.ExcludeCurrent && result.SessionID != filters.CurrentSessionID {
				continue
			}
		}

		// Filter by project path
		if filters.ProjectPath != "" && !strings.Contains(result.ProjectPath, filters.ProjectPath) {
			continue
		}

		// Filter by date range
		if filters.AfterDate != "" && result.Timestamp < filters.AfterDate {
			continue
		}
		if filters.BeforeDate != "" && result.Timestamp > filters.BeforeDate {
			continue
		}

		// Group by session
		sessionID := result.SessionID
		session, exists := sessionMap[sessionID]
		if !exists {
			session = &SessionSearchResult{
				SessionID:      sessionID,
				SessionSummary: result.SessionSummary,
				ProjectPath:    result.ProjectPath,
				UpdatedAt:      result.Timestamp,
				Matches:        []SearchResult{},
			}
			sessionMap[sessionID] = session
			sessionOrder = append(sessionOrder, sessionID)
		}

		// Add this match to the session
		session.Matches = append(session.Matches, result)
	}

	// Calculate relevance scores for each session
	now := time.Now()
	for _, session := range sessionMap {
		session.Score = calculateRelevanceScore(query, session, now)
	}

	// Convert map to slice and sort by relevance score (descending)
	var sessionResults []SessionSearchResult
	for _, sessionID := range sessionOrder {
		sessionResults = append(sessionResults, *sessionMap[sessionID])
	}

	sort.Slice(sessionResults, func(i, j int) bool {
		return sessionResults[i].Score > sessionResults[j].Score
	})

	return sessionResults, nil
}

// SearchCode performs a full-text search using the code-optimized FTS table
// This table uses unicode61 tokenizer without stemming to preserve code identifiers
func SearchCode(database *db.DB, query string) ([]SearchResult, error) {
	return search(database, query, "messages_fts_code", 1000)
}

// search is the internal implementation shared by Search and SearchCode
func search(database *db.DB, query string, ftsTable string, limit int) ([]SearchResult, error) {
	// Validate query
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("search query cannot be empty")
	}

	// Check if query contains special characters that FTS5 can't handle well
	// For these, use LIKE instead for exact substring matching
	hasSpecialChars := strings.ContainsAny(query, "-_@#$%&")

	var rows *sql.Rows
	var err error

	if hasSpecialChars {
		// Use LIKE for exact substring matching with snippet extraction
		rows, err = database.Query(fmt.Sprintf(`
			SELECT
				m.uuid,
				s.session_id,
				COALESCE(ss.one_line_summary, s.llm_summary, s.summary, ''),
				m.text_content,
				m.timestamp,
				s.project_path
			FROM messages m
			JOIN sessions s ON s.id = m.session_id
			LEFT JOIN session_summaries ss ON s.id = ss.session_id
			WHERE m.text_content LIKE '%%' || ? || '%%'
			ORDER BY %s
			LIMIT ?
		`, defaultOrderBy), query, limit)
	} else {
		// Use FTS5 with snippet for regular queries
		escapedQuery := query

		sql := fmt.Sprintf(`
			SELECT
				m.uuid,
				s.session_id,
				COALESCE(ss.one_line_summary, s.llm_summary, s.summary, ''),
				snippet(%s, -1, '', '', '...', 64) as snippet,
				m.timestamp,
				s.project_path
			FROM %s
			JOIN messages m ON %s.rowid = m.id
			JOIN sessions s ON s.id = m.session_id
			LEFT JOIN session_summaries ss ON s.id = ss.session_id
			WHERE %s MATCH ?
			ORDER BY %s
			LIMIT ?
		`, ftsTable, ftsTable, ftsTable, ftsTable, defaultOrderBy)

		rows, err = database.Query(sql, escapedQuery, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("search query failed: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		if err := rows.Scan(
			&r.MessageUUID,
			&r.SessionID,
			&r.SessionSummary,
			&r.MessageText,
			&r.Timestamp,
			&r.ProjectPath,
		); err != nil {
			return nil, fmt.Errorf("failed to scan result: %w", err)
		}

		// For LIKE searches, extract snippet around the match
		// Keep it short (100 chars) so TUI can show full snippet with match visible
		if hasSpecialChars {
			r.MessageText = extractSnippet(r.MessageText, query, 100)
		}

		results = append(results, r)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating results: %w", err)
	}

	return results, nil
}

// extractSnippet extracts a snippet from text centered around the query match
// with specified max length. Case-insensitive matching.
// Handles JSON by extracting just the field containing the match.
func extractSnippet(text, query string, maxLen int) string {
	// Find the match position (case-insensitive)
	lowerText := strings.ToLower(text)
	lowerQuery := strings.ToLower(query)
	pos := strings.Index(lowerText, lowerQuery)

	if pos == -1 {
		// No match found, return beginning of text
		if len(text) <= maxLen {
			return text
		}
		return text[:maxLen] + "..."
	}

	// Check if match is inside JSON - look back for {"
	jsonStart := -1
	if pos > 0 {
		// Look for start of JSON object within 500 chars before match
		for i := pos; i >= 0 && i > pos-500; i-- {
			if i+1 < len(text) && text[i] == '{' && text[i+1] == '"' {
				jsonStart = i
				break
			}
		}
	}

	// If match is in JSON, try to extract just the relevant field value
	if jsonStart != -1 {
		// Find the JSON field containing the match
		// Look backwards from match for the nearest quote that starts a field value
		fieldStart := -1
		for i := pos - 1; i > jsonStart; i-- {
			if text[i] == '"' {
				// Check if this is the start of a field value (preceded by :" or : ")
				if i > 0 && text[i-1] == ':' {
					fieldStart = i + 1
					break
				}
				if i > 1 && text[i-1] == ' ' && text[i-2] == ':' {
					fieldStart = i + 1
					break
				}
			}
		}

		if fieldStart != -1 {
			// Find field end (next unescaped closing quote)
			fieldEnd := -1
			escaped := false
			for i := fieldStart; i < len(text) && i < fieldStart+5000; i++ {
				if escaped {
					escaped = false
					continue
				}
				if text[i] == '\\' {
					escaped = true
					continue
				}
				if text[i] == '"' {
					fieldEnd = i
					break
				}
			}

			// Extract just this field value, cleaning up newlines
			if fieldEnd != -1 && fieldEnd-fieldStart < 800 {
				snippet := text[fieldStart:fieldEnd]
				// Replace escaped newlines (\\n) and actual newlines with spaces
				snippet = strings.ReplaceAll(snippet, `\n`, " ")
				snippet = strings.ReplaceAll(snippet, "\n", " ")
				// If still too long, truncate around the match
				if len(snippet) > 400 {
					matchOffset := pos - fieldStart
					start := matchOffset - 150
					if start < 0 {
						start = 0
					}
					end := matchOffset + len(query) + 150
					if end > len(snippet) {
						end = len(snippet)
					}
					snippet = snippet[start:end]
					if start > 0 {
						snippet = "..." + snippet
					}
					if end < fieldEnd-fieldStart {
						snippet = snippet + "..."
					}
				}
				return snippet
			}
		}
	}

	// Not JSON or extraction failed - do standard snippet extraction
	queryLen := len(query)
	halfMax := maxLen / 2

	start := pos - halfMax
	if start < 0 {
		start = 0
	}

	end := pos + queryLen + halfMax
	if end > len(text) {
		end = len(text)
	}

	// Adjust to try to break at word/line boundaries
	if start > 0 {
		// Look for newline or sentence boundary before start
		for i := start; i > 0 && i > start-50; i-- {
			if text[i] == '\n' || (text[i] == '.' && i+1 < len(text) && text[i+1] == ' ') {
				start = i + 1
				// Skip leading whitespace
				for start < len(text) && (text[start] == ' ' || text[start] == '\n') {
					start++
				}
				break
			}
		}
		// If no sentence boundary, look for word boundary
		if start == pos-halfMax {
			for i := start; i > 0 && i > start-20; i-- {
				if text[i] == ' ' {
					start = i + 1
					break
				}
			}
		}
	}

	if end < len(text) {
		// Look for sentence or line boundary after end
		for i := end; i < len(text) && i < end+50; i++ {
			if text[i] == '\n' || (text[i] == '.' && i+1 < len(text) && text[i+1] == ' ') {
				end = i
				break
			}
		}
		// If no sentence boundary, look for word boundary
		if end == pos+queryLen+halfMax {
			for i := end; i < len(text) && i < end+20; i++ {
				if text[i] == ' ' {
					end = i
					break
				}
			}
		}
	}

	snippet := text[start:end]

	// Replace escaped newlines (\\n) and actual newlines with spaces
	snippet = strings.ReplaceAll(snippet, `\n`, " ")
	snippet = strings.ReplaceAll(snippet, "\n", " ")

	// Add ellipsis if truncated
	if start > 0 {
		snippet = "..." + snippet
	}
	if end < len(text) {
		snippet = snippet + "..."
	}

	return snippet
}

// calculateRelevanceScore computes a relevance score for a session based on:
// - Number of matching messages (10 points each)
// - Whether query appears in summary (50 point boost)
// - Recency of the session (0-20 point boost, logarithmic decay)
func calculateRelevanceScore(query string, session *SessionSearchResult, now time.Time) float64 {
	score := 0.0

	// Base score: 10 points per matching message
	score += float64(len(session.Matches)) * 10.0

	// Summary boost: 50 points if query appears in summary
	queryLower := strings.ToLower(query)
	summaryLower := strings.ToLower(session.SessionSummary)
	if strings.Contains(summaryLower, queryLower) {
		score += 50.0
	}

	// Recency boost: 0-20 points based on how recent the session is
	// Uses logarithmic decay: sessions from today get ~20, last week ~10, last month ~5
	updatedAt, err := time.Parse(time.RFC3339, session.UpdatedAt)
	if err == nil {
		ageHours := now.Sub(updatedAt).Hours()
		if ageHours < 1 {
			ageHours = 1 // Avoid log(0)
		}
		// Log scale: recent sessions get higher boost, old ones get minimal
		recencyBoost := 20.0 * math.Exp(-ageHours/168.0) // 168 hours = 1 week decay constant
		score += recencyBoost
	}

	return score
}
