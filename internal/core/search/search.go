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

// Search performs a full-text search using the natural language FTS table
// Results are ordered by relevance (FTS5 rank)
func Search(database *db.DB, query string) ([]SearchResult, error) {
	return search(database, query, "messages_fts", 50)
}

// SearchCode performs a full-text search using the code-optimized FTS table
// This table uses unicode61 tokenizer without stemming to preserve code identifiers
func SearchCode(database *db.DB, query string) ([]SearchResult, error) {
	return search(database, query, "messages_fts_code", 50)
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
		rows, err = database.Query(`
			SELECT
				m.uuid,
				s.session_id,
				COALESCE(s.summary, ''),
				m.text_content,
				m.timestamp,
				s.project_path
			FROM messages m
			JOIN sessions s ON s.id = m.session_id
			WHERE m.text_content LIKE '%' || ? || '%'
			ORDER BY m.timestamp DESC
			LIMIT ?
		`, query, limit)
	} else {
		// Use FTS5 with snippet for regular queries
		// Quote the query for exact phrase matching, unless it contains wildcards
		escapedQuery := query
		if !strings.Contains(query, "*") {
			escapedQuery = `"` + strings.ReplaceAll(query, `"`, `""`) + `"`
		}

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
			ORDER BY rank
			LIMIT ?
		`, ftsTable, ftsTable, ftsTable, ftsTable)

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
