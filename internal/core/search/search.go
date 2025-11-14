package search

import (
	"database/sql"
	"fmt"
	"strings"

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

	// Convert map to slice maintaining order
	var sessionResults []SessionSearchResult
	for _, sessionID := range sessionOrder {
		sessionResults = append(sessionResults, *sessionMap[sessionID])
	}

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
		// Use LIKE for exact substring matching
		rows, err = database.Query(fmt.Sprintf(`
			SELECT
				m.uuid,
				s.session_id,
				COALESCE(s.summary, ''),
				m.text_content,
				m.timestamp,
				s.project_path
			FROM messages m
			JOIN sessions s ON s.id = m.session_id
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
				COALESCE(s.summary, ''),
				snippet(%s, -1, '', '', '...', 64) as snippet,
				m.timestamp,
				s.project_path
			FROM %s
			JOIN messages m ON %s.rowid = m.id
			JOIN sessions s ON s.id = m.session_id
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
		results = append(results, r)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating results: %w", err)
	}

	return results, nil
}
