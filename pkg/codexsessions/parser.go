package codexsessions

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

type contentItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// isSystemBoilerplate detects Codex CLI system instructions that get
// emitted as role=user response_item messages instead of role=developer.
func isSystemBoilerplate(text string) bool {
	return strings.HasPrefix(text, "# AGENTS.md") ||
		strings.HasPrefix(text, "<environment_context>") ||
		strings.HasPrefix(text, "<system-reminder>")
}

func extractTextFromContent(raw json.RawMessage) string {
	var items []contentItem
	if err := json.Unmarshal(raw, &items); err != nil {
		return ""
	}
	var texts []string
	for _, item := range items {
		if item.Text != "" && (item.Type == "input_text" || item.Type == "output_text") {
			texts = append(texts, item.Text)
		}
	}
	return strings.Join(texts, "\n\n")
}

func deterministicUUID(sessionID string, sequence int) string {
	h := blake3.New()
	_, _ = h.Write([]byte(sessionID + ":" + strconv.Itoa(sequence)))
	sum := h.Sum(nil)
	return hex.EncodeToString(sum[:16])
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

	reader := bufio.NewReaderSize(file, 1024*1024)
	var lineBuffer bytes.Buffer
	var currentCWD string
	var currentVersion string

	// Dual-buffer: collect messages from both sources, prefer response_item
	var responseItemMsgs []ccsessions.ParsedMessage
	var eventMsgMsgs []ccsessions.ParsedMessage
	riSequence := 0
	emSequence := 0

	for {
		lineBuffer.Reset()

		for {
			chunk, err := reader.ReadBytes('\n')
			lineBuffer.Write(chunk)
			if err == io.EOF {
				if lineBuffer.Len() == 0 {
					// Choose message source: prefer response_item if it captured more messages
					if len(responseItemMsgs) >= len(eventMsgMsgs) && len(responseItemMsgs) > 0 {
						session.Messages = responseItemMsgs
					} else {
						session.Messages = eventMsgMsgs
					}

					if session.Summary == "" && len(session.Messages) > 0 {
						for _, m := range session.Messages {
							if m.Sender == "human" && m.TextContent != "" {
								summary := m.TextContent
								runes := []rune(summary)
								if len(runes) > 120 {
									summary = string(runes[:120])
								}
								session.Summary = summary
								break
							}
						}
					}
					return session, nil
				}
				break
			}
			if err != nil {
				return nil, fmt.Errorf("error reading file: %w", err)
			}
			break
		}

		line := lineBuffer.Bytes()
		if len(line) > 0 && line[len(line)-1] == '\n' {
			line = line[:len(line)-1]
		}
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
		if len(line) == 0 {
			continue
		}

		var raw rawLine
		if err := json.Unmarshal(line, &raw); err != nil {
			continue
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
				continue
			}
			if ri.Type != "message" {
				continue
			}
			switch ri.Role {
			// "developer" role carries system instructions, not conversation content — skip it
			case "user":
				text := extractTextFromContent(ri.Content)
				if text == "" || isSystemBoilerplate(text) {
					continue
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
					continue
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
				continue
			}

			switch ev.Type {
			case "user_message":
				if ev.Message == "" {
					continue
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
					continue
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
	}
}
