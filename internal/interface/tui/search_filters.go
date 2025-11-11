package tui

import (
	"strings"
	"time"

	"github.com/olebedev/when"
	"github.com/olebedev/when/rules/common"
	"github.com/olebedev/when/rules/en"
)

// SearchFilters represents parsed filters from a search query
type SearchFilters struct {
	Query      string    // The actual search text
	Project    string    // Filter by project path
	AfterDate  time.Time // Only sessions after this date
	BeforeDate time.Time // Only sessions before this date
	HasAfter   bool      // Whether AfterDate was set
	HasBefore  bool      // Whether BeforeDate was set
}

// ParseSearchQuery extracts filters from a search query string
// Supports:
//   - project:<path> - filter by project
//   - date:yesterday, date:last-week, date:2024-11-01 - filter by date
//   - after:yesterday, before:2024-11-01 - explicit date ranges
func ParseSearchQuery(query string) SearchFilters {
	filters := SearchFilters{}

	// Initialize date parser with English rules
	w := when.New(nil)
	w.Add(en.All...)
	w.Add(common.All...)

	// Split query into tokens
	tokens := strings.Fields(query)
	var queryParts []string

	for _, token := range tokens {
		// Check for filter prefixes
		if strings.HasPrefix(token, "project:") {
			filters.Project = strings.TrimPrefix(token, "project:")
			continue
		}

		if strings.HasPrefix(token, "date:") {
			dateStr := strings.TrimPrefix(token, "date:")
			if parsed := parseDate(w, dateStr); parsed != nil {
				// For "date:" treat as "after this date"
				filters.AfterDate = *parsed
				filters.HasAfter = true
			}
			continue
		}

		if strings.HasPrefix(token, "after:") {
			dateStr := strings.TrimPrefix(token, "after:")
			if parsed := parseDate(w, dateStr); parsed != nil {
				filters.AfterDate = *parsed
				filters.HasAfter = true
			}
			continue
		}

		if strings.HasPrefix(token, "before:") {
			dateStr := strings.TrimPrefix(token, "before:")
			if parsed := parseDate(w, dateStr); parsed != nil {
				filters.BeforeDate = *parsed
				filters.HasBefore = true
			}
			continue
		}

		// Not a filter, add to query
		queryParts = append(queryParts, token)
	}

	filters.Query = strings.Join(queryParts, " ")
	return filters
}

// parseDate attempts to parse a date string using natural language parsing
func parseDate(w *when.Parser, dateStr string) *time.Time {
	// Try natural language parsing first
	result, err := w.Parse(dateStr, time.Now())
	if err == nil && result != nil {
		return &result.Time
	}

	// Try standard formats
	formats := []string{
		"2006-01-02",
		"2006-01-02T15:04:05",
		time.RFC3339,
		"2006/01/02",
		"01/02/2006",
	}

	for _, format := range formats {
		if t, err := time.Parse(format, dateStr); err == nil {
			return &t
		}
	}

	return nil
}
