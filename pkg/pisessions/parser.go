package pisessions

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

const Provider = "pi"

type rawLine struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Timestamp string          `json:"timestamp"`
	CWD       string          `json:"cwd"`
	Message   json.RawMessage `json:"message"`
}

type messagePayload struct {
	Role      string          `json:"role"`
	Content   json.RawMessage `json:"content"`
	Provider  string          `json:"provider"`
	Model     string          `json:"model"`
	Timestamp int64           `json:"timestamp"`
}

// ParseFile parses a Pi agent session JSONL file.
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

	fileStem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	session := &ccsessions.ParsedSession{
		SessionID: fileStem,
		FilePath:  path,
		FileSize:  info.Size(),
		FileMtime: info.ModTime(),
		Messages:  make([]ccsessions.ParsedMessage, 0),
	}

	var currentCWD string
	sequence := 0

	if err := ccsessions.ForEachLine(file, func(line []byte) error {
		var raw rawLine
		if err := json.Unmarshal(line, &raw); err != nil {
			return nil
		}

		switch raw.Type {
		case "session":
			if raw.ID != "" {
				session.SessionID = raw.ID
			}
			if raw.CWD != "" {
				currentCWD = raw.CWD
			}
		case "message":
			msg, ok := parseMessage(raw, fileStem, currentCWD, sequence+1, session.FileMtime)
			if !ok {
				return nil
			}
			sequence++
			msg.Sequence = sequence
			session.Messages = append(session.Messages, msg)
		}

		return nil
	}); err != nil {
		return nil, fmt.Errorf("error reading file: %w", err)
	}

	session.Summary = ccsessions.FirstUserSummary(session.Messages)
	return session, nil
}

func parseMessage(raw rawLine, fileStem, cwd string, sequence int, fallbackTime time.Time) (ccsessions.ParsedMessage, bool) {
	if len(raw.Message) == 0 {
		return ccsessions.ParsedMessage{}, false
	}

	var payload messagePayload
	if err := json.Unmarshal(raw.Message, &payload); err != nil {
		return ccsessions.ParsedMessage{}, false
	}

	msgType, sender, ok := mapRole(payload.Role)
	if !ok {
		return ccsessions.ParsedMessage{}, false
	}

	text := ccsessions.TextFromItems(payload.Content, "text")
	if text == "" {
		return ccsessions.ParsedMessage{}, false
	}

	return ccsessions.ParsedMessage{
		UUID:        messageUUID(fileStem, raw.ID, sequence),
		Type:        msgType,
		Sender:      sender,
		Content:     append(json.RawMessage(nil), raw.Message...),
		TextContent: text,
		Timestamp:   messageTime(raw.Timestamp, payload.Timestamp, fallbackTime),
		Sequence:    sequence,
		CWD:         cwd,
		Version:     providerVersion(payload.Provider, payload.Model),
	}, true
}

func mapRole(role string) (msgType, sender string, ok bool) {
	switch role {
	case "user":
		return "user", "human", true
	case "assistant":
		return "assistant", "assistant", true
	case "toolResult":
		return "tool", "tool", true
	default:
		return "", "", false
	}
}

func messageUUID(fileStem, eventID string, sequence int) string {
	key := fileStem + ":" + eventID
	if eventID == "" {
		key = fileStem + ":sequence:" + strconv.Itoa(sequence)
	}
	return ccsessions.DeterministicUUID(key)
}

func messageTime(lineTimestamp string, payloadTimestamp int64, fallback time.Time) time.Time {
	if lineTimestamp != "" {
		if ts, err := time.Parse(time.RFC3339Nano, lineTimestamp); err == nil {
			return ts
		}
	}
	if payloadTimestamp > 0 {
		return time.UnixMilli(payloadTimestamp).UTC()
	}
	return fallback
}

func providerVersion(provider, model string) string {
	switch {
	case provider == "":
		return model
	case model == "":
		return provider
	default:
		return provider + "/" + model
	}
}
