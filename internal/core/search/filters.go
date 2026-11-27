package search

import (
	"strconv"
	"strings"
	"time"

	"github.com/olebedev/when"
	"github.com/olebedev/when/rules/common"
	"github.com/olebedev/when/rules/en"
)

// dateParser is a package-level parser initialized once
var dateParser *when.Parser

func init() {
	dateParser = when.New(nil)
	dateParser.Add(en.All...)
	dateParser.Add(common.All...)
}

// ParseQuery extracts filters from a search query string and returns SearchFilters.
// This is the centralized parser used by TUI, CLI, and any other interface.
//
// Supported filter syntax:
//   - project:<path>           - filter by project path substring
//   - after:<date>             - sessions updated after date
//   - before:<date>            - sessions updated before date
//   - date:<date>              - alias for after:<date>
//
// Supported date formats:
//   - ISO 8601: 2024-01-15, 2024-01-15T10:30:00
//   - Go duration: 3h, 24h; day or week count: 7d, 2w (treated as "X ago")
//   - Natural language: yesterday, today, tomorrow (the start of that day)
//   - Relative: "3 days ago", "last week", "in 2 hours"
//   - Hyphenated: 3-days-ago, last-week (converted to spaces)
//
// Examples:
//
//	"authentication after:yesterday" -> query="authentication", after=yesterday
//	"error project:myapp before:2024-01-01" -> query="error", project="myapp", before=2024-01-01
//	"after:3h" -> sessions from last 3 hours (no text query)
//	"after:3-days-ago fix bug" -> query="fix bug", after=3 days ago
func ParseQuery(query string) SearchFilters {
	filters := SearchFilters{}

	tokens := strings.Fields(query)
	var queryParts []string

	for _, token := range tokens {
		switch {
		case strings.HasPrefix(token, "project:"):
			filters.ProjectPath = strings.TrimPrefix(token, "project:")

		case strings.HasPrefix(token, "date:"):
			dateStr := strings.TrimPrefix(token, "date:")
			if t := parseDate(dateStr); t != nil {
				filters.AfterDate = t.Format(time.RFC3339)
			}

		case strings.HasPrefix(token, "after:"):
			dateStr := strings.TrimPrefix(token, "after:")
			if t := parseDate(dateStr); t != nil {
				filters.AfterDate = t.Format(time.RFC3339)
			}

		case strings.HasPrefix(token, "before:"):
			dateStr := strings.TrimPrefix(token, "before:")
			if t := parseDate(dateStr); t != nil {
				filters.BeforeDate = t.Format(time.RFC3339)
			}

		default:
			queryParts = append(queryParts, token)
		}
	}

	filters.Query = strings.Join(queryParts, " ")
	return filters
}

// parseDate attempts to parse a date string using multiple strategies.
// Returns nil if parsing fails.
func parseDate(dateStr string) *time.Time {
	// 1. Try Go duration format (3h, 30m, 24h, 168h, etc.)
	// Treat as "X ago" - subtract from now
	if duration, err := time.ParseDuration(dateStr); err == nil {
		t := time.Now().Add(-duration)
		return &t
	}

	// 1b. Day and week counts (7d, 2w), which Go durations lack, also "X ago"
	if days, ok := dayCount(dateStr); ok {
		t := time.Now().AddDate(0, 0, -days)
		return &t
	}

	// 2. Try standard ISO date formats (before natural language to avoid misparses)
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

	// 3. Convert hyphenated format to spaces for natural language
	// "3-days-ago" -> "3 days ago", "last-week" -> "last week"
	normalizedStr := strings.ReplaceAll(dateStr, "-", " ")

	// A named day means the start of that day in local time, so after:today
	// covers all of today. The natural language parser reads these as the
	// same clock time on that day: after:today meant "after now" and found
	// nothing.
	if offset, ok := map[string]int{"today": 0, "yesterday": -1, "tomorrow": 1}[normalizedStr]; ok {
		now := time.Now()
		t := time.Date(now.Year(), now.Month(), now.Day()+offset, 0, 0, 0, 0, now.Location())
		return &t
	}

	// The natural language parser has no rule for "last week", so it means
	// seven days ago, like the other relative forms.
	if normalizedStr == "last week" {
		t := time.Now().AddDate(0, 0, -7)
		return &t
	}

	// 4. Try natural language parsing with 'when' library
	result, err := dateParser.Parse(normalizedStr, time.Now())
	if err == nil && result != nil {
		return &result.Time
	}

	return nil
}

// maxDayCount bounds a day or week count at 100 years, far past any session
// history, so a typo can't overflow into a date in the future.
const maxDayCount = 100 * 366

// dayCount reads a count of days (7d) or weeks (2w) as a number of days.
func dayCount(s string) (int, bool) {
	if len(s) < 2 {
		return 0, false
	}
	n, err := strconv.Atoi(s[:len(s)-1])
	if err != nil || n < 0 || n > maxDayCount {
		return 0, false
	}
	switch s[len(s)-1] {
	case 'd':
		return n, true
	case 'w':
		if n*7 > maxDayCount {
			return 0, false
		}
		return n * 7, true
	}
	return 0, false
}
