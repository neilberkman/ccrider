// Package opencodesessions parses OpenCode sessions from OpenCode's SQLite DB.
//
// Current OpenCode stores durable sessions in the XDG data directory,
// typically ~/.local/share/opencode/opencode.db or an opencode-<channel>.db
// variant. The public `opencode export` command reads the same session,
// message, and part tables this package reads directly.
package opencodesessions

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/neilberkman/ccrider/pkg/ccsessions"
	_ "modernc.org/sqlite"
)

const Provider = "opencode"

type sessionRow struct {
	ID          string
	Title       string
	Directory   string
	Version     string
	CreatedMS   int64
	UpdatedMS   int64
	ProjectRoot string
}

type messageRow struct {
	ID        string
	CreatedMS int64
	Data      jsonValue
}

type jsonValue []byte

func (j *jsonValue) Scan(src any) error {
	switch value := src.(type) {
	case nil:
		*j = nil
	case string:
		*j = append((*j)[:0], value...)
	case []byte:
		*j = append((*j)[:0], value...)
	default:
		return fmt.Errorf("scan opencode json: unsupported source type %T", src)
	}
	return nil
}

func (j jsonValue) RawMessage() json.RawMessage {
	return json.RawMessage(j)
}

type messageInfo struct {
	Role     string `json:"role"`
	ParentID string `json:"parentID"`
}

type partInfo struct {
	Type        string          `json:"type"`
	Text        string          `json:"text"`
	Prompt      string          `json:"prompt"`
	Description string          `json:"description"`
	State       json.RawMessage `json:"state"`
	Source      json.RawMessage `json:"source"`
}

type toolState struct {
	Status string          `json:"status"`
	Title  string          `json:"title"`
	Output json.RawMessage `json:"output"`
	Error  string          `json:"error"`
}

type textSpan struct {
	Value string `json:"value"`
}

// DefaultDBPaths returns existing OpenCode DBs in the order ccrider should
// import them. OPENCODE_DB follows OpenCode's own relative-path convention:
// relative values are resolved under the OpenCode data directory.
func DefaultDBPaths() []string {
	dataDir := defaultDataDir()
	if env := os.Getenv("OPENCODE_DB"); env != "" {
		if env == ":memory:" {
			return nil
		}
		if !filepath.IsAbs(env) {
			env = filepath.Join(dataDir, env)
		}
		if fileExists(env) {
			return []string{env}
		}
		return nil
	}

	matches, err := filepath.Glob(filepath.Join(dataDir, "opencode*.db"))
	if err != nil {
		return nil
	}
	sort.Slice(matches, func(i, j int) bool {
		if filepath.Base(matches[i]) == "opencode.db" {
			return true
		}
		if filepath.Base(matches[j]) == "opencode.db" {
			return false
		}
		return matches[i] < matches[j]
	})
	return matches
}

func defaultDataDir() string {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "opencode")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".local", "share", "opencode")
	}
	return filepath.Join(home, ".local", "share", "opencode")
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// ParseAll parses all root OpenCode sessions in dbPath. Child sessions are
// subtask internals in current OpenCode; forked sessions created with
// `opencode --fork` are separate root sessions, so they are included.
func ParseAll(dbPath string) ([]*ccsessions.ParsedSession, error) {
	info, err := os.Stat(dbPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("stat opencode db: %w", err)
	}

	conn, err := openReadOnly(dbPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()

	if ok, err := hasTables(conn, "session", "project", "message", "part"); err != nil {
		return nil, err
	} else if !ok {
		return nil, nil
	}

	rows, err := conn.Query(`
		SELECT
			s.id,
			COALESCE(s.title, ''),
			COALESCE(s.directory, ''),
			COALESCE(s.version, ''),
			COALESCE(s.time_created, 0),
			COALESCE(s.time_updated, 0),
			COALESCE(p.worktree, '')
		FROM session s
		LEFT JOIN project p ON p.id = s.project_id
		WHERE s.parent_id IS NULL
		ORDER BY s.time_updated DESC, s.id DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("query opencode sessions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var sessions []*ccsessions.ParsedSession
	for rows.Next() {
		var row sessionRow
		if err := rows.Scan(&row.ID, &row.Title, &row.Directory, &row.Version, &row.CreatedMS, &row.UpdatedMS, &row.ProjectRoot); err != nil {
			return nil, fmt.Errorf("scan opencode session: %w", err)
		}
		session, err := parseSession(conn, dbPath, info, row)
		if err != nil {
			return nil, err
		}
		if session != nil {
			sessions = append(sessions, session)
		}
	}
	return sessions, rows.Err()
}

func openReadOnly(dbPath string) (*sql.DB, error) {
	u := url.URL{Scheme: "file", Path: dbPath}
	q := u.Query()
	q.Set("mode", "ro")
	q.Set("_pragma", "busy_timeout(5000)")
	u.RawQuery = q.Encode()

	conn, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, fmt.Errorf("open opencode db: %w", err)
	}
	if err := conn.Ping(); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("open opencode db: %w", err)
	}
	return conn, nil
}

func hasTables(conn *sql.DB, names ...string) (bool, error) {
	for _, name := range names {
		var count int
		if err := conn.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&count); err != nil {
			return false, fmt.Errorf("inspect opencode schema: %w", err)
		}
		if count == 0 {
			return false, nil
		}
	}
	return true, nil
}

func parseSession(conn *sql.DB, dbPath string, dbInfo os.FileInfo, row sessionRow) (*ccsessions.ParsedSession, error) {
	rows, err := conn.Query(`
		SELECT id, COALESCE(time_created, 0), data
		FROM message
		WHERE session_id = ?
		ORDER BY time_created ASC, id ASC
	`, row.ID)
	if err != nil {
		return nil, fmt.Errorf("query opencode messages for %s: %w", row.ID, err)
	}
	defer func() { _ = rows.Close() }()

	var messages []ccsessions.ParsedMessage
	sequence := 0
	for rows.Next() {
		var msgRow messageRow
		if err := rows.Scan(&msgRow.ID, &msgRow.CreatedMS, &msgRow.Data); err != nil {
			return nil, fmt.Errorf("scan opencode message for %s: %w", row.ID, err)
		}
		msg, ok, err := parseMessage(conn, row, msgRow, sequence+1)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		sequence++
		msg.Sequence = sequence
		messages = append(messages, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read opencode messages for %s: %w", row.ID, err)
	}
	if len(messages) == 0 {
		return nil, nil
	}

	summary := row.Title
	if isDefaultTitle(summary) || strings.TrimSpace(summary) == "" {
		summary = ccsessions.FirstUserSummary(messages)
	}

	return &ccsessions.ParsedSession{
		SessionID: row.ID,
		Summary:   summary,
		// Synthetic path so the shared importer derives session_id from the
		// OpenCode session ID while retaining source-db context for debugging.
		FilePath:  filepath.Join(dbPath+".sessions", row.ID+".opencode"),
		FileSize:  dbInfo.Size(),
		FileMtime: dbInfo.ModTime(),
		Messages:  messages,
	}, nil
}

func parseMessage(conn *sql.DB, session sessionRow, row messageRow, sequence int) (ccsessions.ParsedMessage, bool, error) {
	var info messageInfo
	if err := json.Unmarshal(row.Data.RawMessage(), &info); err != nil {
		return ccsessions.ParsedMessage{}, false, nil
	}

	msgType, sender := roleToMessage(info.Role)
	if msgType == "" {
		return ccsessions.ParsedMessage{}, false, nil
	}

	parts, err := loadParts(conn, row.ID)
	if err != nil {
		return ccsessions.ParsedMessage{}, false, err
	}

	text := textFromParts(parts, msgType)
	if strings.TrimSpace(text) == "" {
		// In-flight live-session turns can be present before their text/tool
		// output is durable. Skipping them self-heals on the next sync because
		// enumerated providers use a content hash.
		return ccsessions.ParsedMessage{}, false, nil
	}

	content, _ := json.Marshal(map[string]any{
		"info":  row.Data.RawMessage(),
		"parts": parts,
	})

	cwd := session.Directory
	if cwd == "" {
		cwd = session.ProjectRoot
	}

	return ccsessions.ParsedMessage{
		UUID:        row.ID,
		ParentUUID:  info.ParentID,
		Type:        msgType,
		Sender:      sender,
		Content:     content,
		TextContent: text,
		Timestamp:   timeFromMillis(row.CreatedMS),
		Sequence:    sequence,
		CWD:         cwd,
		Version:     session.Version,
	}, true, nil
}

func loadParts(conn *sql.DB, messageID string) ([]json.RawMessage, error) {
	rows, err := conn.Query(`
		SELECT data
		FROM part
		WHERE message_id = ?
		ORDER BY id ASC
	`, messageID)
	if err != nil {
		return nil, fmt.Errorf("query opencode parts for %s: %w", messageID, err)
	}
	defer func() { _ = rows.Close() }()

	var parts []json.RawMessage
	for rows.Next() {
		var raw jsonValue
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("scan opencode part for %s: %w", messageID, err)
		}
		parts = append(parts, raw.RawMessage())
	}
	return parts, rows.Err()
}

func roleToMessage(role string) (msgType, sender string) {
	switch role {
	case "user":
		return "user", "human"
	case "assistant":
		return "assistant", "assistant"
	default:
		return "", ""
	}
}

func textFromParts(parts []json.RawMessage, msgType string) string {
	var texts []string
	for _, raw := range parts {
		var part partInfo
		if err := json.Unmarshal(raw, &part); err != nil {
			continue
		}
		switch part.Type {
		case "text":
			appendText(&texts, part.Text)
		case "tool":
			if msgType == "assistant" {
				appendText(&texts, textFromToolState(part.State))
			}
		case "subtask":
			appendText(&texts, part.Description)
			appendText(&texts, part.Prompt)
		case "file":
			appendText(&texts, textFromFileSource(part.Source))
		}
	}
	return strings.TrimSpace(strings.Join(texts, "\n\n"))
}

func appendText(texts *[]string, text string) {
	text = strings.TrimSpace(text)
	if text != "" {
		*texts = append(*texts, text)
	}
}

func textFromToolState(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var state toolState
	if err := json.Unmarshal(raw, &state); err != nil {
		return ""
	}
	var texts []string
	appendText(&texts, state.Title)
	if state.Status == "completed" {
		appendText(&texts, textFromOutput(state.Output))
	}
	if state.Status == "error" {
		appendText(&texts, state.Error)
	}
	return strings.Join(texts, "\n\n")
}

func textFromOutput(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var obj struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil {
		return obj.Text
	}
	return ""
}

func textFromFileSource(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var obj struct {
		Text json.RawMessage `json:"text"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return ""
	}
	if len(obj.Text) == 0 || string(obj.Text) == "null" {
		return ""
	}
	var text string
	if err := json.Unmarshal(obj.Text, &text); err == nil {
		return text
	}
	var span textSpan
	if err := json.Unmarshal(obj.Text, &span); err == nil {
		return span.Value
	}
	return ""
}

func isDefaultTitle(title string) bool {
	if strings.HasPrefix(title, "New session - ") || strings.HasPrefix(title, "Child session - ") {
		_, err := time.Parse(time.RFC3339Nano, strings.TrimPrefix(strings.TrimPrefix(title, "New session - "), "Child session - "))
		return err == nil
	}
	return false
}

func timeFromMillis(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}

// ErrNoDatabase is returned by ParseFirst when no OpenCode DB path exists.
var ErrNoDatabase = errors.New("opencode database not found")

// ParseFirst parses the first discovered OpenCode DB. It is a small convenience
// for tests and one-off tooling; ccrider sync imports every DefaultDBPaths entry.
func ParseFirst() ([]*ccsessions.ParsedSession, error) {
	paths := DefaultDBPaths()
	if len(paths) == 0 {
		return nil, ErrNoDatabase
	}
	return ParseAll(paths[0])
}
