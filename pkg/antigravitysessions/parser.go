// Package antigravitysessions parses Antigravity CLI conversation transcripts.
package antigravitysessions

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/neilberkman/ccrider/pkg/ccsessions"
)

const Provider = "antigravity"

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
}

// DefaultRoot returns Antigravity CLI's local application data directory.
func DefaultRoot() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".gemini", "antigravity-cli")
	}
	return filepath.Join(home, ".gemini", "antigravity-cli")
}

// ParseAll imports canonical, user-visible Antigravity CLI transcripts. The
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
	index := workspaceIndex{byConversation: make(map[string]string)}
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

func (index workspaceIndex) workspaceFor(conversationID, firstUserText string, firstUserTime time.Time) string {
	if workspace := index.byConversation[conversationID]; workspace != "" {
		return workspace
	}
	if firstUserTime.IsZero() || firstUserText == "" {
		return ""
	}
	for _, entry := range index.history {
		if entry.Timestamp == firstUserTime.UnixMilli() && strings.TrimSpace(entry.Display) == firstUserText {
			return entry.Workspace
		}
	}
	return ""
}
