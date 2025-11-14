package db

import (
	"database/sql"
	"time"
)

// Stats represents database statistics
type Stats struct {
	TotalSessions          int
	TotalMessages          int
	TotalToolUses          int
	OldestSession          time.Time
	NewestSession          time.Time
	MostActiveProject      string
	MostActiveProjectCount int
}

// GetStats returns comprehensive database statistics
func (db *DB) GetStats() (*Stats, error) {
	stats := &Stats{}

	// Total sessions
	err := db.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&stats.TotalSessions)
	if err != nil {
		return nil, err
	}

	// Total messages
	err = db.QueryRow("SELECT COUNT(*) FROM messages").Scan(&stats.TotalMessages)
	if err != nil {
		return nil, err
	}

	// Total tool uses
	err = db.QueryRow("SELECT COUNT(*) FROM tool_uses").Scan(&stats.TotalToolUses)
	if err != nil {
		return nil, err
	}

	// Date range (only if we have sessions)
	if stats.TotalSessions > 0 {
		var minCreated, maxUpdated sql.NullString
		err = db.QueryRow("SELECT MIN(created_at), MAX(updated_at) FROM sessions").Scan(&minCreated, &maxUpdated)
		if err != nil {
			return nil, err
		}

		if minCreated.Valid {
			// Try to parse the timestamp
			formats := []string{
				time.RFC3339,
				time.RFC3339Nano,
				"2006-01-02 15:04:05.999999999 -0700 MST",
				"2006-01-02 15:04:05",
			}
			for _, format := range formats {
				if t, parseErr := time.Parse(format, minCreated.String); parseErr == nil {
					stats.OldestSession = t
					break
				}
			}
		}

		if maxUpdated.Valid {
			// Try to parse the timestamp
			formats := []string{
				time.RFC3339,
				time.RFC3339Nano,
				"2006-01-02 15:04:05.999999999 -0700 MST",
				"2006-01-02 15:04:05",
			}
			for _, format := range formats {
				if t, parseErr := time.Parse(format, maxUpdated.String); parseErr == nil {
					stats.NewestSession = t
					break
				}
			}
		}

		// Most active project
		var mostActiveProject sql.NullString
		err = db.QueryRow(`
			SELECT project_path, COUNT(*) as count
			FROM sessions
			GROUP BY project_path
			ORDER BY count DESC
			LIMIT 1
		`).Scan(&mostActiveProject, &stats.MostActiveProjectCount)

		if err != nil && err != sql.ErrNoRows {
			return nil, err
		}

		if mostActiveProject.Valid {
			stats.MostActiveProject = mostActiveProject.String
		}
	}

	return stats, nil
}
