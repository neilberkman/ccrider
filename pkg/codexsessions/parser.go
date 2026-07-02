package codexsessions

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/neilberkman/ccrider/pkg/ccsessions"
)

type rawLine struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type sessionMetaPayload struct {
	ID         string `json:"id"`
	CWD        string `json:"cwd"`
	CLIVersion string `json:"cli_version"`
}

type turnContextPayload struct {
	CWD   string `json:"cwd"`
	Model string `json:"model"`
}

type eventMsgPayload struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type responseItemPayload struct {
	Type    string          `json:"type"`
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// isSystemBoilerplate detects Codex CLI system instructions that get
// emitted as role=user response_item messages instead of role=developer.
func isSystemBoilerplate(text string) bool {
	return strings.HasPrefix(text, "# AGENTS.md") ||
		strings.HasPrefix(text, "<environment_context>") ||
		strings.HasPrefix(text, "<system-reminder>")
}

func extractTextFromContent(raw json.RawMessage) string {
	return ccsessions.TextFromItems(raw, "input_text", "output_text")
}

// deterministicUUID keys messages by session id + sequence. The key string
// must never change: it determines the stored message UUIDs that re-imports
// upsert against.
func deterministicUUID(sessionID string, sequence int) string {
	return ccsessions.DeterministicUUID(sessionID + ":" + strconv.Itoa(sequence))
}

func ParseFile(path string) (*ccsessions.ParsedSession, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("failed to stat file: %w", err)
	}

	sessionID := filepath.Base(path)
	sessionID = sessionID[:len(sessionID)-len(filepath.Ext(sessionID))]

	session := &ccsessions.ParsedSession{
		SessionID: sessionID,
		FilePath:  path,
		FileSize:  info.Size(),
		FileMtime: info.ModTime(),
		Messages:  make([]ccsessions.ParsedMessage, 0),
	}

	var currentCWD string
	var currentVersion string

	// Dual-buffer: collect messages from both sources, prefer response_item
	var responseItemMsgs []ccsessions.ParsedMessage
	var eventMsgMsgs []ccsessions.ParsedMessage
	riSequence := 0
	emSequence := 0

	if err := ccsessions.ForEachLine(file, func(line []byte) error {
		var raw rawLine
		if err := json.Unmarshal(line, &raw); err != nil {
			return nil
		}

		ts, err := time.Parse(time.RFC3339Nano, raw.Timestamp)
		if err != nil {
			ts = session.FileMtime
		}

		switch raw.Type {
		case "session_meta":
			var meta sessionMetaPayload
			if err := json.Unmarshal(raw.Payload, &meta); err == nil {
				if meta.ID != "" {
					session.SessionID = meta.ID
				}
				if meta.CWD != "" {
					currentCWD = meta.CWD
				}
				if meta.CLIVersion != "" {
					currentVersion = meta.CLIVersion
				}
			}

		case "turn_context":
			var tc turnContextPayload
			if err := json.Unmarshal(raw.Payload, &tc); err == nil {
				if tc.CWD != "" {
					currentCWD = tc.CWD
				}
			}

		case "response_item":
			var ri responseItemPayload
			if err := json.Unmarshal(raw.Payload, &ri); err != nil {
				return nil
			}
			if ri.Type != "message" {
				return nil
			}
			switch ri.Role {
			// "developer" role carries system instructions, not conversation content — skip it
			case "user":
				text := extractTextFromContent(ri.Content)
				if text == "" || isSystemBoilerplate(text) {
					return nil
				}
				riSequence++
				responseItemMsgs = append(responseItemMsgs, ccsessions.ParsedMessage{
					UUID:        deterministicUUID(session.SessionID, riSequence),
					Type:        "user",
					Sender:      "human",
					TextContent: text,
					Timestamp:   ts,
					Sequence:    riSequence,
					CWD:         currentCWD,
					Version:     currentVersion,
				})
			case "assistant":
				text := extractTextFromContent(ri.Content)
				if text == "" {
					return nil
				}
				riSequence++
				responseItemMsgs = append(responseItemMsgs, ccsessions.ParsedMessage{
					UUID:        deterministicUUID(session.SessionID, riSequence),
					Type:        "assistant",
					Sender:      "assistant",
					TextContent: text,
					Timestamp:   ts,
					Sequence:    riSequence,
					CWD:         currentCWD,
					Version:     currentVersion,
				})
			}

		case "event_msg":
			var ev eventMsgPayload
			if err := json.Unmarshal(raw.Payload, &ev); err != nil {
				return nil
			}

			switch ev.Type {
			case "user_message":
				if ev.Message == "" {
					return nil
				}
				emSequence++
				eventMsgMsgs = append(eventMsgMsgs, ccsessions.ParsedMessage{
					UUID:        deterministicUUID(session.SessionID, emSequence),
					Type:        "user",
					Sender:      "human",
					TextContent: ev.Message,
					Timestamp:   ts,
					Sequence:    emSequence,
					CWD:         currentCWD,
					Version:     currentVersion,
				})

			case "agent_message":
				if ev.Message == "" {
					return nil
				}
				emSequence++
				eventMsgMsgs = append(eventMsgMsgs, ccsessions.ParsedMessage{
					UUID:        deterministicUUID(session.SessionID, emSequence),
					Type:        "assistant",
					Sender:      "assistant",
					TextContent: ev.Message,
					Timestamp:   ts,
					Sequence:    emSequence,
					CWD:         currentCWD,
					Version:     currentVersion,
				})
			}
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("error reading file: %w", err)
	}

	// Choose message source: prefer response_item if it captured more messages
	if len(responseItemMsgs) >= len(eventMsgMsgs) && len(responseItemMsgs) > 0 {
		session.Messages = responseItemMsgs
	} else {
		session.Messages = eventMsgMsgs
	}

	if session.Summary == "" {
		session.Summary = ccsessions.FirstUserSummary(session.Messages)
	}

	return session, nil
}
