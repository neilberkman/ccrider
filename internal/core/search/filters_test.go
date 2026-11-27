package search

import (
	"testing"
	"time"
)

// ParseQuery documents day counts such as 7d and phrases such as last-week.
// Each must set the filter to about that long ago, not drop it.
func TestParseQueryReadsDaysAndWeeks(t *testing.T) {
	for _, tc := range []struct {
		value string
		days  int
	}{
		{"7d", 7},
		{"2d", 2},
		{"2w", 14},
		{"last-week", 7},
	} {
		filters := ParseQuery("parser after:" + tc.value)
		if filters.Query != "parser" {
			t.Errorf("after:%s: query = %q, want %q", tc.value, filters.Query, "parser")
		}
		got, err := time.Parse(time.RFC3339, filters.AfterDate)
		if err != nil {
			t.Errorf("after:%s: AfterDate = %q, want a date", tc.value, filters.AfterDate)
			continue
		}
		// Calendar days, so a daylight saving change in the window keeps the
		// clock time rather than shifting it by an hour.
		if want := time.Now().AddDate(0, 0, -tc.days); got.Sub(want).Abs() > time.Minute {
			t.Errorf("after:%s: AfterDate = %v, want about %v", tc.value, got, want)
		}
	}
}

// A day or week count too large to be a real look-back is not a date: a
// multiplication that overflows would otherwise land in the future.
func TestParseQueryRejectsHugeDayCounts(t *testing.T) {
	for _, value := range []string{"99999999999d", "9999999999999999w"} {
		if got := ParseQuery("parser after:" + value).AfterDate; got != "" {
			t.Errorf("after:%s: AfterDate = %q, want no date", value, got)
		}
	}
}
