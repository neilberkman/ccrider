// Package copilotsessions parses GitHub Copilot CLI sessions.
//
// The Copilot CLI keeps a per-session event log at
// ~/.copilot/session-state/<uuid>/events.jsonl, plus a small workspace.yaml
// with session metadata (name, cwd). It also maintains a derived SQLite index
// at ~/.copilot/session-store.db, but that index collapses each multi-step
// assistant turn into a single, condensed `assistant_response` row — it loses a
// meaningful fraction of the assistant's text. We therefore parse events.jsonl,
// which is the full transcript and the same per-session-file shape as Claude
// Code and Codex, and use workspace.yaml only for the session summary/cwd.
//
// ParseAll walks the session-state directory and returns one
// ccsessions.ParsedSession per session that has a conversation.
package copilotsessions

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/neilberkman/ccrider/pkg/ccsessions"
)

// DefaultStateDir returns the path to the Copilot CLI per-session state
// directory, or "" if the home directory cannot be determined.
func DefaultStateDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".copilot", "session-state")
}

// rawEvent is one line of events.jsonl. Each event carries a globally-unique id
// (used directly as the message UUID), its parent's id, a timestamp, and a
// type-specific data payload.
type rawEvent struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	ParentID  string          `json:"parentId"`
	Timestamp string          `json:"timestamp"`
	Data      json.RawMessage `json:"data"`
}

type sessionStartData struct {
	Context struct {
		CWD string `json:"cwd"`
	} `json:"context"`
}

type messageData struct {
	Content string `json:"content"`
}

// parseTime parses Copilot event timestamps (RFC3339, "...Z").
func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

// ParseAll walks stateDir and parses each session's events.jsonl. Sessions with
// no events.jsonl (or no conversational messages) are skipped. A missing
// stateDir is not an error — it just yields no sessions.
func ParseAll(stateDir string) ([]*ccsessions.ParsedSession, error) {
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read copilot state dir: %w", err)
	}

	var sessions []*ccsessions.ParsedSession
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		sessionID := entry.Name()
		sessionDir := filepath.Join(stateDir, sessionID)
		eventsPath := filepath.Join(sessionDir, "events.jsonl")

		info, err := os.Stat(eventsPath)
		if err != nil {
			// No event log for this session (never had a conversation) — skip.
			continue
		}

		session, err := parseSession(sessionDir, sessionID, eventsPath, info)
		if err != nil {
			// Skip an unreadable/corrupt session rather than failing the whole sync.
			continue
		}
		if session != nil {
			sessions = append(sessions, session)
		}
	}

	return sessions, nil
}

// parseSession parses one session's events.jsonl into a ParsedSession, or nil if
// it has no conversational messages.
func parseSession(sessionDir, sessionID, eventsPath string, info os.FileInfo) (*ccsessions.ParsedSession, error) {
	file, err := os.Open(eventsPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	// workspace.yaml carries the human-friendly session name (used as summary)
	// and the cwd; fall back to the cwd from the session.start event.
	wsName, wsCWD := readWorkspace(filepath.Join(sessionDir, "workspace.yaml"))
	cwd := wsCWD

	// Read events line by line and skip any malformed line, so one corrupt
	// line (e.g. a partially-flushed write on a live session) doesn't drop the
	// rest of the transcript — matching the Claude/Codex parsers. ReadBytes
	// grows to fit arbitrarily long event lines.
	reader := bufio.NewReaderSize(file, 1024*1024)

	var messages []ccsessions.ParsedMessage
	sequence := 0

	for {
		line, readErr := reader.ReadBytes('\n')

		if len(line) > 0 {
			var ev rawEvent
			if json.Unmarshal(line, &ev) == nil {
				switch ev.Type {
				case "session.start", "session.resume":
					// The event stream is authoritative for cwd; the
					// workspace.yaml value is only a fallback for sessions
					// whose start event lacks one.
					var d sessionStartData
					if json.Unmarshal(ev.Data, &d) == nil && d.Context.CWD != "" {
						cwd = d.Context.CWD
					}

				case "user.message":
					var d messageData
					if json.Unmarshal(ev.Data, &d) == nil && strings.TrimSpace(d.Content) != "" {
						sequence++
						messages = append(messages, ccsessions.ParsedMessage{
							UUID:        ev.ID,
							ParentUUID:  ev.ParentID,
							Type:        "user",
							Sender:      "human",
							TextContent: d.Content,
							Timestamp:   parseTime(ev.Timestamp),
							Sequence:    sequence,
							CWD:         cwd,
						})
					}

				case "assistant.message":
					var d messageData
					if json.Unmarshal(ev.Data, &d) == nil && strings.TrimSpace(d.Content) != "" {
						sequence++
						messages = append(messages, ccsessions.ParsedMessage{
							UUID:        ev.ID,
							ParentUUID:  ev.ParentID,
							Type:        "assistant",
							Sender:      "assistant",
							TextContent: d.Content,
							Timestamp:   parseTime(ev.Timestamp),
							Sequence:    sequence,
							CWD:         cwd,
						})
					}
				}
			}
		}

		if readErr != nil {
			break
		}
	}

	if len(messages) == 0 {
		return nil, nil
	}

	summary := wsName
	if summary == "" {
		// Fall back to the first user message, matching the other parsers.
		for _, m := range messages {
			if m.Sender == "human" && m.TextContent != "" {
				runes := []rune(m.TextContent)
				if len(runes) > 120 {
					summary = string(runes[:120])
				} else {
					summary = m.TextContent
				}
				break
			}
		}
	}

	return &ccsessions.ParsedSession{
		SessionID: sessionID,
		Summary:   summary,
		// Synthetic path so the importer derives session_id == the Copilot UUID
		// from the file basename (every events.jsonl is literally "events.jsonl",
		// so the parent dir name is the only per-session identifier).
		FilePath:  filepath.Join(sessionDir, sessionID+".copilot"),
		FileSize:  info.Size(),
		FileMtime: info.ModTime(),
		Messages:  messages,
	}, nil
}

// readWorkspace extracts the session name and cwd from a Copilot workspace.yaml.
// The file is flat (top-level "key: value" scalars), so a minimal line parser
// avoids pulling in a YAML dependency.
func readWorkspace(path string) (name, cwd string) {
	file, err := os.Open(path)
	if err != nil {
		return "", ""
	}
	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		// Only consider top-level keys (no leading whitespace).
		if len(line) == 0 || line[0] == ' ' || line[0] == '\t' || line[0] == '#' {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch key {
		case "name":
			name = unquoteYAML(strings.TrimSpace(value))
		case "cwd":
			cwd = unquoteYAML(strings.TrimSpace(value))
		}
	}
	return name, cwd
}

// unquoteYAML strips a single pair of surrounding quotes from a scalar value.
func unquoteYAML(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
