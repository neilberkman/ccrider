package search

import (
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
	LastCwd        string
	MessageCount   int
	Sequence       int    // Message sequence number within session
	Provider       string // claude, codex, etc.
}

// SearchFilters defines filtering criteria for search
type SearchFilters struct {
	Query            string // The search query text
	ProjectPath      string // Filter by project path (substring match)
	Provider         string // Filter by provider (claude, codex)
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
	LastCwd        string
	UpdatedAt      string
	MessageCount   int
	Matches        []SearchResult
	Score          float64 // Relevance score for ranking
	Provider       string  // claude, codex, etc.
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
	query := strings.TrimSpace(filters.Query)
	hasFilters := filters.AfterDate != "" || filters.BeforeDate != "" || filters.ProjectPath != ""

	// If no query but has filters, do filter-only search (no FTS)
	if len(query) < 2 && hasFilters {
		return filterOnlySessions(database, filters)
	}

	// Require minimum 2 characters for text search
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

		// Filter by provider
		if filters.Provider != "" && result.Provider != filters.Provider {
			continue
		}

		// Filter by project path
		if filters.ProjectPath != "" && !strings.Contains(result.ProjectPath, filters.ProjectPath) {
			continue
		}

		// Filter by date range (parse timestamps for proper comparison)
		if filters.AfterDate != "" {
			afterTime, _ := time.Parse(time.RFC3339, filters.AfterDate)
			resultTime := parseDBTimestamp(result.Timestamp)
			if resultTime.Before(afterTime) {
				continue
			}
		}
		if filters.BeforeDate != "" {
			beforeTime, _ := time.Parse(time.RFC3339, filters.BeforeDate)
			resultTime := parseDBTimestamp(result.Timestamp)
			if resultTime.After(beforeTime) {
				continue
			}
		}

		// Group by session
		sessionID := result.SessionID
		session, exists := sessionMap[sessionID]
		if !exists {
			session = &SessionSearchResult{
				SessionID:      sessionID,
				SessionSummary: result.SessionSummary,
				ProjectPath:    result.ProjectPath,
				LastCwd:        result.LastCwd,
				UpdatedAt:      result.Timestamp,
				MessageCount:   result.MessageCount,
				Matches:        []SearchResult{},
				Provider:       result.Provider,
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

	// Escape query for FTS5 - wraps each token in quotes to handle special characters
	escapedQuery := escapeFTS5Query(query)

	sql := fmt.Sprintf(`
		SELECT
			m.uuid,
			s.session_id,
			COALESCE(ss.one_line_summary, s.llm_summary, s.summary, ''),
			snippet(%s, -1, '', '', '...', 64) as snippet,
			m.timestamp,
			s.project_path,
			COALESCE(s.cwd, s.project_path),
			s.message_count,
			m.sequence,
			COALESCE(s.provider, 'claude')
		FROM %s
		JOIN messages m ON %s.rowid = m.id
		JOIN sessions s ON s.id = m.session_id
		LEFT JOIN session_summaries ss ON s.id = ss.session_id
		WHERE %s MATCH ?
		ORDER BY %s
		LIMIT ?
	`, ftsTable, ftsTable, ftsTable, ftsTable, defaultOrderBy)

	rows, err := database.Query(sql, escapedQuery, limit)
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
			&r.LastCwd,
			&r.MessageCount,
			&r.Sequence,
			&r.Provider,
		); err != nil {
			return nil, fmt.Errorf("failed to scan result: %w", err)
		}

		results = append(results, r)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating results: %w", err)
	}

	return results, nil
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

// filterOnlySessions returns sessions matching date/project filters without text search
func filterOnlySessions(database *db.DB, filters SearchFilters) ([]SessionSearchResult, error) {
	// Build query with optional filters
	query := `
		SELECT
			s.session_id,
			COALESCE(ss.one_line_summary, s.llm_summary, s.summary, ''),
			s.project_path,
			COALESCE(s.cwd, s.project_path),
			s.updated_at,
			s.message_count,
			COALESCE(s.provider, 'claude')
		FROM sessions s
		LEFT JOIN session_summaries ss ON s.id = ss.session_id
		WHERE 1=1
	`
	var args []interface{}

	if filters.ProjectPath != "" {
		query += " AND s.project_path LIKE '%' || ? || '%'"
		args = append(args, filters.ProjectPath)
	}
	if filters.Provider != "" {
		query += " AND COALESCE(s.provider, 'claude') = ?"
		args = append(args, filters.Provider)
	}
	// Date filtering done in Go due to timestamp format inconsistencies

	query += " ORDER BY s.updated_at DESC LIMIT 500" // Fetch more, filter in Go

	rows, err := database.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("filter query failed: %w", err)
	}
	defer func() { _ = rows.Close() }()

	// Parse filter dates once
	var afterTime, beforeTime time.Time
	if filters.AfterDate != "" {
		afterTime, _ = time.Parse(time.RFC3339, filters.AfterDate)
	}
	if filters.BeforeDate != "" {
		beforeTime, _ = time.Parse(time.RFC3339, filters.BeforeDate)
	}

	var results []SessionSearchResult
	for rows.Next() {
		var r SessionSearchResult
		if err := rows.Scan(
			&r.SessionID,
			&r.SessionSummary,
			&r.ProjectPath,
			&r.LastCwd,
			&r.UpdatedAt,
			&r.MessageCount,
			&r.Provider,
		); err != nil {
			return nil, fmt.Errorf("failed to scan session: %w", err)
		}

		// Apply date filters in Go
		if filters.AfterDate != "" {
			sessionTime := parseDBTimestamp(r.UpdatedAt)
			if sessionTime.Before(afterTime) {
				continue
			}
		}
		if filters.BeforeDate != "" {
			sessionTime := parseDBTimestamp(r.UpdatedAt)
			if sessionTime.After(beforeTime) {
				continue
			}
		}

		// No matches for filter-only results
		r.Matches = []SearchResult{}
		results = append(results, r)

		// Limit to 100 after filtering
		if len(results) >= 100 {
			break
		}
	}

	return results, nil
}

// escapeFTS5Query escapes a user query to be safe for FTS5 MATCH.
// It handles all FTS5 special characters and operators by quoting each token.
//
// FTS5 special characters that cause syntax errors include:
// - Operators: AND, OR, NOT, NEAR
// - Punctuation: * ^ + : - ( ) { } " ,
// - Various other chars: . / ? @ # $ % &
//
// The proper escaping approach (used by sqlite-utils/datasette):
// 1. If user explicitly quoted a phrase with "", preserve it as a phrase search
// 2. Otherwise, split into tokens, escape internal quotes, wrap each in quotes
// 3. Preserve trailing * for prefix/wildcard queries (FTS5 feature)
// 4. Join with spaces (implicit AND)
//
// Examples:
//
//	"hello world" -> `"hello" "world"` (matches both words, any order)
//	"hello, world" -> `"hello," "world"` (comma preserved in token)
//	`"exact phrase"` -> `"exact phrase"` (user's phrase preserved)
//	"test-driven" -> `"test-driven"` (hyphen preserved)
//	"foo"bar" -> `"foo""bar"` (embedded quote escaped)
//	"handle*" -> `"handle"*` (wildcard preserved outside quotes)
func escapeFTS5Query(query string) string {
	query = strings.TrimSpace(query)
	if query == "" {
		return query
	}

	// Check if user explicitly wrapped query in quotes for phrase search
	// Preserve their intent for exact phrase matching
	if strings.HasPrefix(query, "\"") && strings.HasSuffix(query, "\"") && len(query) > 2 {
		// User wants phrase search - escape internal quotes and return
		inner := query[1 : len(query)-1]
		escaped := strings.ReplaceAll(inner, "\"", "\"\"")
		return "\"" + escaped + "\""
	}

	// Split on whitespace into tokens
	tokens := strings.Fields(query)
	if len(tokens) == 0 {
		return query
	}

	// Escape each token: replace " with "", wrap in quotes
	// Preserve trailing * for prefix queries (must be outside quotes for FTS5)
	var escaped []string
	for _, token := range tokens {
		// Check for trailing wildcard
		hasWildcard := strings.HasSuffix(token, "*")
		if hasWildcard {
			token = token[:len(token)-1]
		}

		// Escape any embedded double quotes
		token = strings.ReplaceAll(token, "\"", "\"\"")

		// Wrap in double quotes, with wildcard outside if present
		if hasWildcard {
			escaped = append(escaped, "\""+token+"\"*")
		} else {
			escaped = append(escaped, "\""+token+"\"")
		}
	}

	// Join with spaces (FTS5 treats this as implicit AND)
	return strings.Join(escaped, " ")
}

// parseDBTimestamp parses timestamps from the database which may be in various formats
func parseDBTimestamp(ts string) time.Time {
	// Try RFC3339 first (standard)
	if t, err := time.Parse(time.RFC3339, ts); err == nil {
		return t
	}
	// Try Go's default format (what the DB stores: "2006-01-02 15:04:05.999 -0700 MST")
	if t, err := time.Parse("2006-01-02 15:04:05.999999999 -0700 MST", ts); err == nil {
		return t
	}
	// Try without fractional seconds
	if t, err := time.Parse("2006-01-02 15:04:05 -0700 MST", ts); err == nil {
		return t
	}
	// Try with +0000 UTC format
	if t, err := time.Parse("2006-01-02 15:04:05.999 +0000 UTC", ts); err == nil {
		return t
	}
	if t, err := time.Parse("2006-01-02 15:04:05 +0000 UTC", ts); err == nil {
		return t
	}
	// Fallback: return zero time
	return time.Time{}
}
