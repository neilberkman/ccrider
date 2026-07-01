package pisessions

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/neilberkman/ccrider/pkg/ccsessions"
	"github.com/zeebo/blake3"
)

const Provider = "pi"

type rawLine struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	ParentID  string          `json:"parentId"`
	Timestamp string          `json:"timestamp"`
	Version   int             `json:"version"`
	CWD       string          `json:"cwd"`
	Provider  string          `json:"provider"`
	ModelID   string          `json:"modelId"`
	Message   *messagePayload `json:"message"`
}

type messagePayload struct {
	Role       string        `json:"role"`
	Content    []contentItem `json:"content"`
	Timestamp  int64         `json:"timestamp"`
	ToolName   string        `json:"toolName"`
	ToolCallID string        `json:"toolCallId"`
	IsError    bool          `json:"isError"`
	Provider   string        `json:"provider"`
	Model      string        `json:"model"`
}

type contentItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// ParseFile parses a Pi agent session JSONL file.
//
// Pi session files include durable conversation events (`type=message`) plus
// extension/UI events (`custom`, `custom_message`, etc.). ccrider indexes only
// the core message stream in v1 and deliberately skips custom/plugin payloads so
// workflow state does not pollute search results.
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

	sessionID := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	session := &ccsessions.ParsedSession{
		SessionID: sessionID,
		FilePath:  path,
		FileSize:  info.Size(),
		FileMtime: info.ModTime(),
		Messages:  make([]ccsessions.ParsedMessage, 0),
	}

	reader := bufio.NewReaderSize(file, 1024*1024)
	var lineBuffer bytes.Buffer
	var currentCWD string
	sequence := 0

	for {
		lineBuffer.Reset()
		for {
			chunk, rerr := reader.ReadBytes('\n')
			lineBuffer.Write(chunk)
			if rerr == io.EOF {
				if lineBuffer.Len() == 0 {
					finishSession(session)
					return session, nil
				}
				break
			}
			if rerr != nil {
				return nil, fmt.Errorf("error reading file: %w", rerr)
			}
			break
		}

		line := lineBuffer.Bytes()
		line = bytes.TrimSuffix(line, []byte("\n"))
		line = bytes.TrimSuffix(line, []byte("\r"))
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}

		var raw rawLine
		if err := json.Unmarshal(line, &raw); err != nil {
			continue
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
			msg, ok := piLineToMessage(&raw, line, session.SessionID, currentCWD, sequence+1, session.FileMtime)
			if !ok {
				continue
			}
			sequence++
			msg.Sequence = sequence
			session.Messages = append(session.Messages, msg)
		}
	}
}

func piLineToMessage(raw *rawLine, line []byte, sessionID, cwd string, sequence int, fallbackTime time.Time) (ccsessions.ParsedMessage, bool) {
	if raw.Message == nil {
		return ccsessions.ParsedMessage{}, false
	}
	text := extractText(raw.Message.Content)
	if text == "" {
		return ccsessions.ParsedMessage{}, false
	}

	msgType, sender, ok := mapRole(raw.Message.Role)
	if !ok {
		return ccsessions.ParsedMessage{}, false
	}

	ts := fallbackTime
	if raw.Timestamp != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, raw.Timestamp); err == nil {
			ts = parsed
		}
	}

	uuid := piDeterministicUUID(sessionID, raw.ID, sequence)
	parentUUID := ""
	if raw.ParentID != "" {
		parentUUID = piDeterministicUUID(sessionID, raw.ParentID, 0)
	}

	return ccsessions.ParsedMessage{
		UUID:        uuid,
		ParentUUID:  parentUUID,
		Type:        msgType,
		Sender:      sender,
		Content:     append(json.RawMessage(nil), line...),
		TextContent: text,
		Timestamp:   ts,
		Sequence:    sequence,
		CWD:         cwd,
		Version:     raw.ProviderVersion(),
	}, true
}

func (r *rawLine) ProviderVersion() string {
	if r.Provider == "" && r.ModelID == "" {
		return ""
	}
	if r.Provider == "" {
		return r.ModelID
	}
	if r.ModelID == "" {
		return r.Provider
	}
	return r.Provider + "/" + r.ModelID
}

func mapRole(role string) (msgType, sender string, ok bool) {
	switch role {
	case "user":
		return "user", "human", true
	case "assistant":
		return "assistant", "assistant", true
	case "tool", "tool_result", "toolResult", "tool_use", "toolUse":
		return "tool", "tool", true
	default:
		return "", "", false
	}
}

func extractText(items []contentItem) string {
	texts := make([]string, 0, len(items))
	for _, item := range items {
		if item.Type == "text" && item.Text != "" {
			texts = append(texts, item.Text)
		}
	}
	return strings.Join(texts, "\n\n")
}

func finishSession(session *ccsessions.ParsedSession) {
	if session.Summary != "" {
		return
	}
	for _, msg := range session.Messages {
		if msg.Sender != "human" || msg.TextContent == "" {
			continue
		}
		runes := []rune(msg.TextContent)
		if len(runes) > 120 {
			runes = runes[:120]
		}
		session.Summary = string(runes)
		return
	}
}

func piDeterministicUUID(sessionID, rawID string, sequence int) string {
	key := sessionID + ":" + rawID
	if rawID == "" {
		key = sessionID + ":sequence:" + strconv.Itoa(sequence)
	}
	h := blake3.New()
	_, _ = h.Write([]byte(key))
	sum := h.Sum(nil)
	return hex.EncodeToString(sum[:16])
}
