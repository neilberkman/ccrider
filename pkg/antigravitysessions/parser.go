// Package antigravitysessions parses Antigravity conversation transcripts
// written by the Antigravity CLI, the Antigravity IDE, and the Antigravity
// desktop app. All three store the same transcript.jsonl layout under their
// own data directory.
package antigravitysessions

import (
	"database/sql"
	"encoding/binary"
	"encoding/json"
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

// Provider ids, one per Antigravity app. The apps keep separate conversation
// stores: `agy --conversation <id>` only opens conversations from the CLI
// store, so sessions from the IDE and the desktop app carry their own ids.
const (
	Provider        = "antigravity"
	ProviderIDE     = "antigravity-ide"
	ProviderDesktop = "antigravity-desktop"
)

// Root is one Antigravity data directory and the provider id its sessions
// are imported under.
type Root struct {
	Path     string
	Provider string
}

type rawStep struct {
	StepIndex int             `json:"step_index"`
	Source    string          `json:"source"`
	Type      string          `json:"type"`
	Status    string          `json:"status"`
	CreatedAt string          `json:"created_at"`
	Content   json.RawMessage `json:"content"`
}

type historyEntry struct {
	Display   string `json:"display"`
	Timestamp int64  `json:"timestamp"`
	Workspace string `json:"workspace"`
}

type workspaceIndex struct {
	byConversation map[string]string
	history        []historyEntry
	// summaries maps every conversation listed in conversation_summaries.db
	// to its first workspace, "" when the app recorded none.
	summaries map[string]string
	// conversationsDir holds the per-conversation <id>.db files, consulted
	// for conversations conversation_summaries.db does not list.
	conversationsDir string
}

// DefaultRoots returns the data directories of the Antigravity CLI, IDE, and
// desktop app, in that order.
func DefaultRoots() []Root {
	base := ".gemini"
	if home, err := os.UserHomeDir(); err == nil {
		base = filepath.Join(home, ".gemini")
	}
	return []Root{
		{Path: filepath.Join(base, "antigravity-cli"), Provider: Provider},
		{Path: filepath.Join(base, "antigravity-ide"), Provider: ProviderIDE},
		{Path: filepath.Join(base, "antigravity"), Provider: ProviderDesktop},
	}
}

// DefaultRoot returns Antigravity CLI's local application data directory.
func DefaultRoot() string {
	return DefaultRoots()[0].Path
}

// ParseAll imports canonical, user-visible Antigravity transcripts from one
// data directory (see DefaultRoots). The
// companion transcript_full.jsonl is intentionally excluded to avoid duplicate
// sessions and excess tool-output indexing.
func ParseAll(root string) ([]*ccsessions.ParsedSession, error) {
	brainRoot := filepath.Join(root, "brain")
	if _, err := os.Stat(brainRoot); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("stat Antigravity brain directory: %w", err)
	}

	index := loadWorkspaceIndex(root)
	var paths []string
	err := filepath.Walk(brainRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || filepath.Base(path) != "transcript.jsonl" {
			return nil
		}
		if _, ok := conversationIDFromTranscript(path, brainRoot); ok {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk Antigravity transcripts: %w", err)
	}
	sort.Strings(paths)

	sessions := make([]*ccsessions.ParsedSession, 0, len(paths))
	for _, path := range paths {
		session, err := parseFile(path, brainRoot, index)
		if err != nil {
			return nil, err
		}
		if session != nil {
			sessions = append(sessions, session)
		}
	}
	return sessions, nil
}

func parseFile(path, brainRoot string, index workspaceIndex) (*ccsessions.ParsedSession, error) {
	conversationID, ok := conversationIDFromTranscript(path, brainRoot)
	if !ok {
		return nil, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open Antigravity transcript: %w", err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat Antigravity transcript: %w", err)
	}

	session := &ccsessions.ParsedSession{
		SessionID: conversationID,
		ImportID:  conversationID,
		FilePath:  path,
		FileSize:  info.Size(),
		FileMtime: info.ModTime(),
		Messages:  make([]ccsessions.ParsedMessage, 0),
	}

	var firstUserText string
	var firstUserTime time.Time
	sequence := 0
	if err := ccsessions.ForEachLine(file, func(line []byte) error {
		var raw rawStep
		if err := json.Unmarshal(line, &raw); err != nil {
			return nil
		}
		if raw.Status != "DONE" {
			return nil
		}

		text, ok := textContent(raw.Content)
		if !ok || strings.TrimSpace(text) == "" {
			return nil
		}
		msgType, sender := "", ""
		switch {
		case raw.Source == "USER_EXPLICIT" && raw.Type == "USER_INPUT":
			text = userRequest(text)
			msgType, sender = "user", "human"
		case raw.Source == "MODEL" && raw.Type == "PLANNER_RESPONSE":
			msgType, sender = "assistant", "assistant"
		default:
			return nil
		}
		if text == "" {
			return nil
		}

		ts := parseTime(raw.CreatedAt, info.ModTime())
		if firstUserText == "" && msgType == "user" {
			firstUserText = text
			firstUserTime = ts
		}
		sequence++
		session.Messages = append(session.Messages, ccsessions.ParsedMessage{
			UUID:        ccsessions.DeterministicUUID("antigravity:" + conversationID + ":" + fmt.Sprint(raw.StepIndex)),
			Type:        msgType,
			Sender:      sender,
			Content:     append(json.RawMessage(nil), line...),
			TextContent: text,
			Timestamp:   ts,
			Sequence:    sequence,
		})
		return nil
	}); err != nil {
		return nil, fmt.Errorf("read Antigravity transcript: %w", err)
	}
	if len(session.Messages) == 0 {
		return nil, nil
	}

	session.Summary = ccsessions.FirstUserSummary(session.Messages)
	session.ProjectPath = index.workspaceFor(conversationID, firstUserText, firstUserTime)
	for i := range session.Messages {
		session.Messages[i].CWD = session.ProjectPath
	}
	return session, nil
}

func conversationIDFromTranscript(path, brainRoot string) (string, bool) {
	rel, err := filepath.Rel(brainRoot, path)
	if err != nil {
		return "", false
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) != 4 || parts[1] != ".system_generated" || parts[2] != "logs" || parts[3] != "transcript.jsonl" || parts[0] == "" {
		return "", false
	}
	return parts[0], true
}

func textContent(raw json.RawMessage) (string, bool) {
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return "", false
	}
	return strings.TrimSpace(text), true
}

func userRequest(text string) string {
	const start = "<USER_REQUEST>"
	const end = "</USER_REQUEST>"
	startAt := strings.Index(text, start)
	endAt := strings.Index(text, end)
	if startAt >= 0 && endAt > startAt {
		return strings.TrimSpace(text[startAt+len(start) : endAt])
	}
	return strings.TrimSpace(text)
}

func parseTime(value string, fallback time.Time) time.Time {
	if value != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
			return parsed
		}
	}
	return fallback
}

func loadWorkspaceIndex(root string) workspaceIndex {
	index := workspaceIndex{
		byConversation:   make(map[string]string),
		summaries:        loadSummaryWorkspaces(filepath.Join(root, "conversation_summaries.db")),
		conversationsDir: filepath.Join(root, "conversations"),
	}
	cachePath := filepath.Join(root, "cache", "last_conversations.json")
	if data, err := os.ReadFile(cachePath); err == nil {
		var latest map[string]string
		if json.Unmarshal(data, &latest) == nil {
			for workspace, conversationID := range latest {
				index.byConversation[conversationID] = workspace
			}
		}
	}

	file, err := os.Open(filepath.Join(root, "history.jsonl"))
	if err != nil {
		return index
	}
	defer func() { _ = file.Close() }()
	_ = ccsessions.ForEachLine(file, func(line []byte) error {
		var entry historyEntry
		if json.Unmarshal(line, &entry) == nil && entry.Workspace != "" {
			index.history = append(index.history, entry)
		}
		return nil
	})
	return index
}

// workspaceFor resolves a conversation's project directory. The CLI's own
// index files come first. The IDE and the desktop app write neither of them,
// so their conversations resolve through conversation_summaries.db (CLI and
// desktop) or the conversation's own database (the only record the IDE keeps).
func (index workspaceIndex) workspaceFor(conversationID, firstUserText string, firstUserTime time.Time) string {
	if workspace := index.byConversation[conversationID]; workspace != "" {
		return workspace
	}
	if !firstUserTime.IsZero() && firstUserText != "" {
		for _, entry := range index.history {
			if entry.Timestamp == firstUserTime.UnixMilli() && strings.TrimSpace(entry.Display) == firstUserText {
				return entry.Workspace
			}
		}
	}
	if workspace, listed := index.summaries[conversationID]; listed {
		return workspace
	}
	if index.conversationsDir == "" {
		return ""
	}
	return conversationWorkspace(filepath.Join(index.conversationsDir, conversationID+".db"))
}

// loadSummaryWorkspaces reads conversation_id -> first workspace from an
// Antigravity conversation_summaries.db. A missing or unreadable database
// yields no entries: workspace association is best effort.
func loadSummaryWorkspaces(dbPath string) map[string]string {
	conn, err := openReadOnly(dbPath)
	if err != nil {
		return nil
	}
	defer func() { _ = conn.Close() }()

	rows, err := conn.Query(`SELECT conversation_id, workspace_uris FROM conversation_summaries`)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()

	workspaces := make(map[string]string)
	for rows.Next() {
		var conversationID, rawURIs string
		if rows.Scan(&conversationID, &rawURIs) != nil {
			continue
		}
		var uris []string
		_ = json.Unmarshal([]byte(rawURIs), &uris)
		workspace := ""
		for _, uri := range uris {
			if workspace = fileURIPath(uri); workspace != "" {
				break
			}
		}
		workspaces[conversationID] = workspace
	}
	return workspaces
}

// conversationWorkspace reads the first workspace recorded in one
// conversation's own database. Its trajectory metadata is a protobuf message
// whose field 1 lists workspaces, each with its URI in field 1.
func conversationWorkspace(dbPath string) string {
	conn, err := openReadOnly(dbPath)
	if err != nil {
		return ""
	}
	defer func() { _ = conn.Close() }()

	var metadata []byte
	if err := conn.QueryRow(`SELECT data FROM trajectory_metadata_blob WHERE id = 'main'`).Scan(&metadata); err != nil {
		return ""
	}
	for _, workspace := range protoBytesFields(metadata, 1) {
		for _, uri := range protoBytesFields(workspace, 1) {
			if path := fileURIPath(string(uri)); path != "" {
				return path
			}
		}
	}
	return ""
}

// protoBytesFields returns the payloads of every length-delimited field with
// the given number in a protobuf message, stopping at the first malformed
// field.
func protoBytesFields(message []byte, field uint64) [][]byte {
	var payloads [][]byte
	for len(message) > 0 {
		tag, n := binary.Uvarint(message)
		if n <= 0 {
			return payloads
		}
		message = message[n:]
		switch tag & 7 {
		case 0: // varint
			_, n := binary.Uvarint(message)
			if n <= 0 {
				return payloads
			}
			message = message[n:]
		case 1: // 64-bit
			if len(message) < 8 {
				return payloads
			}
			message = message[8:]
		case 2: // length-delimited
			size, n := binary.Uvarint(message)
			if n <= 0 || size > uint64(len(message)-n) {
				return payloads
			}
			payload := message[n : n+int(size)]
			message = message[n+int(size):]
			if tag>>3 == field {
				payloads = append(payloads, payload)
			}
		case 5: // 32-bit
			if len(message) < 4 {
				return payloads
			}
			message = message[4:]
		default:
			return payloads
		}
	}
	return payloads
}

// fileURIPath returns the local path of a file:// URI, or "" for anything else.
func fileURIPath(uri string) string {
	parsed, err := url.Parse(uri)
	if err != nil || parsed.Scheme != "file" {
		return ""
	}
	return parsed.Path
}

// openReadOnly opens one of Antigravity's SQLite databases without writing to
// it. The apps use WAL journaling and remove the -wal and -shm files on a
// clean exit; a plain read-only open then fails because SQLite may not create
// the -shm file, so that case falls back to an immutable open, which is safe
// exactly when no -wal file holds unmerged pages.
func openReadOnly(dbPath string) (*sql.DB, error) {
	if _, err := os.Stat(dbPath); err != nil {
		return nil, err
	}
	conn, err := openWithQuery(dbPath, url.Values{"mode": {"ro"}, "_pragma": {"busy_timeout(1000)"}})
	if err == nil {
		return conn, nil
	}
	if _, walErr := os.Stat(dbPath + "-wal"); walErr == nil {
		return nil, err
	}
	return openWithQuery(dbPath, url.Values{"mode": {"ro"}, "immutable": {"1"}})
}

func openWithQuery(dbPath string, query url.Values) (*sql.DB, error) {
	u := url.URL{Scheme: "file", Path: dbPath, RawQuery: query.Encode()}
	conn, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	// Ping alone succeeds on a WAL database whose -shm file is missing; the
	// failure only surfaces on the first read.
	if _, err := conn.Exec(`SELECT 1 FROM sqlite_master LIMIT 1`); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}
