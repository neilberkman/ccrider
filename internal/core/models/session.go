package models

import (
	"errors"
	"time"
)

// Session represents a Claude Code conversation session
type Session struct {
	ID           int64
	SessionID    string // UUID from filename
	ProjectPath  string // Normalized project path
	Summary      string // From summary JSONL entry
	LeafUUID     string // Last message UUID
	CWD          string // Working directory
	GitBranch    string // Git branch
	CreatedAt    time.Time
	UpdatedAt    time.Time
	MessageCount int
	Version      string // Claude Code version
	ImportedAt   time.Time
	LastSyncedAt time.Time
	FileHash     string // SHA256 for change detection
	FileSize     int64
	FileMtime    time.Time
}

// Validate checks if the session has required fields
func (s *Session) Validate() error {
	if s.SessionID == "" {
		return errors.New("session_id is required")
	}
	if s.ProjectPath == "" {
		return errors.New("project_path is required")
	}
	return nil
}
