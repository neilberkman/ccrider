package db

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/sethvargo/go-diceware/diceware"
)

// SessionAnchor is a saved phrase and the sessions it identifies. SessionID is
// an explicit binding; otherwise Sessions contains all literal transcript matches.
// An anchor with no matches is still saved and can be listed before import.
type SessionAnchor struct {
	Anchor    string
	Label     string
	SessionID string
	CreatedAt time.Time
	Sessions  []Session
}

type GenerateSessionAnchorOptions struct {
	Label     string
	SessionID string
}

type SessionAnchorFilters struct {
	SessionID   string
	ProjectPath string
	Provider    string
}

// GenerateSessionAnchor persists a unique phrase before returning it. A known
// session can be bound immediately; otherwise listing discovers transcript matches.
func (db *DB) GenerateSessionAnchor(ctx context.Context, opts GenerateSessionAnchorOptions) (*SessionAnchor, error) {
	label := strings.TrimSpace(opts.Label)
	prefix, err := anchorLabelPrefix(label)
	if err != nil {
		return nil, err
	}
	sessionID := strings.TrimSpace(opts.SessionID)
	if sessionID != "" {
		sessionID, err = db.ResolveSessionID(sessionID)
		if err != nil {
			return nil, err
		}
	}

	for attempt := 0; attempt < 5; attempt++ {
		phrase, err := generateAnchorPhrase(prefix)
		if err != nil {
			return nil, err
		}
		anchor := &SessionAnchor{
			Anchor: phrase, Label: label, SessionID: sessionID,
			CreatedAt: time.Now().UTC(), Sessions: []Session{},
		}
		result, err := db.conn.ExecContext(ctx, `
			INSERT INTO session_anchors (anchor, label, session_id, created_at)
			VALUES (?, ?, NULLIF(?, ''), ?)
			ON CONFLICT(anchor) DO NOTHING
		`, anchor.Anchor, anchor.Label, anchor.SessionID, anchor.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("save session anchor: %w", err)
		}
		inserted, err := result.RowsAffected()
		if err != nil {
			return nil, err
		}
		if inserted > 0 {
			return anchor, nil
		}
	}
	return nil, fmt.Errorf("could not generate a unique session anchor")
}

func anchorLabelPrefix(label string) (string, error) {
	if !utf8.ValidString(label) || utf8.RuneCountInString(label) > 120 {
		return "", fmt.Errorf("label must be valid UTF-8 and at most 120 characters")
	}
	var prefix strings.Builder
	separator := false
	for _, r := range label {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("label must not contain control characters")
		}
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			if separator && prefix.Len() > 0 {
				prefix.WriteByte('-')
			}
			prefix.WriteRune(unicode.ToLower(r))
			separator = false
		} else {
			separator = true
		}
	}
	if label != "" && prefix.Len() == 0 {
		return "", fmt.Errorf("label must contain a letter or number")
	}
	return prefix.String(), nil
}

// RandomPhrase returns four random words joined by hyphens, the same form as
// unlabeled session anchors.
func RandomPhrase() (string, error) { return generateAnchorPhrase("") }

func generateAnchorPhrase(prefix string) (string, error) {
	if prefix == "" {
		words, err := diceware.Generate(4)
		if err != nil {
			return "", err
		}
		return strings.Join(words, "-"), nil
	}
	var suffix [6]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", err
	}
	return prefix + "-" + hex.EncodeToString(suffix[:]), nil
}

// ListSessionAnchors returns all saved anchors, newest first. Transcript lookup
// has no date cutoff, uses the non-stemming FTS index, and verifies the literal
// phrase so punctuation/tokenization cannot associate a different tag. Copied
// phrases may match multiple sessions; no arbitrary session is chosen as owner.
// Session filters omit anchors that have no matching session yet.
func (db *DB) ListSessionAnchors(ctx context.Context, filters SessionAnchorFilters) ([]SessionAnchor, error) {
	if filters.SessionID != "" {
		id, err := db.ResolveSessionID(filters.SessionID)
		if err != nil {
			return nil, err
		}
		filters.SessionID = id
	}
	rows, err := db.conn.QueryContext(ctx, `
		SELECT anchor, label, COALESCE(session_id, ''), created_at
		FROM session_anchors ORDER BY created_at DESC, rowid DESC
	`)
	if err != nil {
		return nil, err
	}
	anchors := []SessionAnchor{}
	for rows.Next() {
		var a SessionAnchor
		if err := rows.Scan(&a.Anchor, &a.Label, &a.SessionID, &a.CreatedAt); err != nil {
			_ = rows.Close()
			return nil, err
		}
		anchors = append(anchors, a)
	}
	err = rows.Err()
	_ = rows.Close() // Release the single DB connection before resolving sessions.
	if err != nil {
		return nil, err
	}

	result := make([]SessionAnchor, 0, len(anchors))
	for _, a := range anchors {
		a.Sessions, err = db.sessionsForAnchor(ctx, a, filters)
		if err != nil {
			return nil, err
		}
		if len(a.Sessions) == 0 && (filters.SessionID != "" || filters.ProjectPath != "" || filters.Provider != "") {
			continue
		}
		result = append(result, a)
	}
	return result, nil
}

func (db *DB) sessionsForAnchor(ctx context.Context, a SessionAnchor, filters SessionAnchorFilters) ([]Session, error) {
	query := `SELECT s.session_id, ` + summaryExpr + `, s.project_path,
		(SELECT COUNT(*) FROM messages WHERE session_id = s.id),
		s.updated_at, s.created_at, COALESCE(s.provider, 'claude'), COALESCE(s.resume_via, '')
		FROM sessions s LEFT JOIN session_summaries ss ON s.id = ss.session_id WHERE `
	var args []any
	if a.SessionID != "" {
		query += `s.session_id = ?`
		args = append(args, a.SessionID)
	} else {
		query += `s.id IN (
			SELECT m.session_id FROM messages_fts_code
			JOIN messages m ON m.id = messages_fts_code.rowid
			WHERE messages_fts_code MATCH ? AND instr(m.text_content, ?) > 0
		)`
		args = append(args, `"`+strings.ReplaceAll(a.Anchor, `"`, `""`)+`"`, a.Anchor)
	}
	if filters.SessionID != "" {
		query += ` AND s.session_id = ?`
		args = append(args, filters.SessionID)
	}
	if filters.ProjectPath != "" {
		query += ` AND instr(s.project_path, ?) > 0`
		args = append(args, filters.ProjectPath)
	}
	if filters.Provider != "" {
		query += ` AND COALESCE(s.provider, 'claude') = ?`
		args = append(args, filters.Provider)
	}
	query += ` ORDER BY s.updated_at DESC, s.session_id`
	rows, err := db.conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	sessions := []Session{}
	for rows.Next() {
		var s Session
		var updatedAt, createdAt sql.NullTime
		if err := rows.Scan(&s.SessionID, &s.Summary, &s.ProjectPath, &s.MessageCount,
			&updatedAt, &createdAt, &s.Provider, &s.ResumeVia); err != nil {
			return nil, err
		}
		s.UpdatedAt, s.CreatedAt = updatedAt.Time, createdAt.Time
		sessions = append(sessions, s)
	}
	return sessions, rows.Err()
}
