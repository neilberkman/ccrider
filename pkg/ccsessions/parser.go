package ccsessions

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ParsedSession represents a fully parsed session file
type ParsedSession struct {
	SessionID string
	Summary   string
	LeafUUID  string
	Messages  []ParsedMessage
	FilePath  string
	FileSize  int64
	FileMtime time.Time
}

// ParsedMessage represents a parsed JSONL message entry
type ParsedMessage struct {
	UUID        string
	ParentUUID  string
	Type        string
	Sender      string
	Content     json.RawMessage
	TextContent string
	Timestamp   time.Time
	Sequence    int
	IsSidechain bool
	CWD         string
	GitBranch   string
	Version     string
}

// rawEntry represents a raw JSONL line
type rawEntry struct {
	Type        string          `json:"type"`
	Summary     string          `json:"summary,omitempty"`
	LeafUUID    string          `json:"leafUuid,omitempty"`
	UUID        string          `json:"uuid,omitempty"`
	ParentUUID  string          `json:"parentUuid,omitempty"`
	SessionID   string          `json:"sessionId,omitempty"`
	Message     json.RawMessage `json:"message,omitempty"`
	Timestamp   string          `json:"timestamp,omitempty"`
	IsSidechain bool            `json:"isSidechain,omitempty"`
	CWD         string          `json:"cwd,omitempty"`
	GitBranch   string          `json:"gitBranch,omitempty"`
	Version     string          `json:"version,omitempty"`
}

// ParseFile parses a Claude Code session JSONL file
func ParseFile(path string) (session *ParsedSession, err error) {
	file, ferr := os.Open(path)
	if ferr != nil {
		return nil, fmt.Errorf("failed to open file: %w", ferr)
	}
	defer func() {
		if cerr := file.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("failed to close file: %w", cerr)
		}
	}()

	// Get file info for metadata
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("failed to stat file: %w", err)
	}

	// Initialize session with filename-based ID
	sessionID := filepath.Base(path)
	sessionID = sessionID[:len(sessionID)-len(filepath.Ext(sessionID))]

	// For agent sessions (agent-*.jsonl), KEEP the filename as the session ID
	// because the sessionId in the file points to the parent session, not the agent
	isAgentSession := len(sessionID) > 6 && sessionID[:6] == "agent-"

	session = &ParsedSession{
		SessionID: sessionID,
		FilePath:  path,
		FileSize:  info.Size(),
		FileMtime: info.ModTime(),
		Messages:  make([]ParsedMessage, 0),
	}

	// Configure scanner with larger buffer for long lines (10MB max)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 10*1024*1024)

	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Bytes()

		var raw rawEntry
		if err := json.Unmarshal(line, &raw); err != nil {
			return nil, fmt.Errorf("line %d: failed to parse JSON: %w", lineNum, err)
		}

		// Handle summary if present (may not be first line, or may not exist)
		if raw.Type == "summary" {
			session.Summary = raw.Summary
			session.LeafUUID = raw.LeafUUID
			// Extract sessionId from summary if available (but NOT for agent sessions
			// since their sessionId points to parent, not themselves)
			if raw.SessionID != "" && !isAgentSession {
				session.SessionID = raw.SessionID
			}
			continue
		}

		// Extract sessionId from messages if we haven't found it yet
		// Skip for agent sessions - their sessionId points to parent session
		if raw.SessionID != "" && session.SessionID == sessionID && !isAgentSession {
			session.SessionID = raw.SessionID
		}

		// Parse message entries
		msg, err := parseMessage(&raw, lineNum)
		if err != nil {
			// Log warning but don't fail - some message types we may not support yet
			fmt.Fprintf(os.Stderr, "Warning: %s line %d: %v\n", path, lineNum, err)
			continue
		}

		session.Messages = append(session.Messages, *msg)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading file: %w", err)
	}

	return session, nil
}

func parseMessage(raw *rawEntry, sequence int) (*ParsedMessage, error) {
	msg := &ParsedMessage{
		UUID:        raw.UUID,
		ParentUUID:  raw.ParentUUID,
		Type:        raw.Type,
		Sequence:    sequence,
		IsSidechain: raw.IsSidechain,
		CWD:         raw.CWD,
		GitBranch:   raw.GitBranch,
		Version:     raw.Version,
		Content:     raw.Message,
	}

	// Parse timestamp
	if raw.Timestamp != "" {
		t, err := time.Parse(time.RFC3339, raw.Timestamp)
		if err != nil {
			return nil, fmt.Errorf("invalid timestamp: %w", err)
		}
		msg.Timestamp = t
	}

	// Extract text content and sender based on type
	switch raw.Type {
	case "user":
		// Try array format first (newer format with tool_use/tool_result)
		var userMsgArray struct {
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text,omitempty"`
			} `json:"content"`
		}
		if err := json.Unmarshal(raw.Message, &userMsgArray); err == nil {
			// Extract text from text blocks only
			for _, block := range userMsgArray.Content {
				if block.Type == "text" {
					msg.TextContent += block.Text + "\n"
				}
			}
			msg.Sender = "human"
		} else {
			// Fall back to string format (older format)
			var userMsgString struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			}
			if err := json.Unmarshal(raw.Message, &userMsgString); err == nil {
				msg.TextContent = userMsgString.Content
				msg.Sender = "human"
			}
		}

	case "assistant":
		var assistantMsg struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text,omitempty"`
			} `json:"content"`
		}
		if err := json.Unmarshal(raw.Message, &assistantMsg); err == nil {
			for _, block := range assistantMsg.Content {
				if block.Type == "text" {
					msg.TextContent += block.Text + "\n"
				}
			}
			msg.Sender = "assistant"
		}

	case "system", "file-history-snapshot", "queue-operation":
		// These types don't have extractable text content
		msg.TextContent = ""

	default:
		// Unknown message type - warn but don't fail
		// Just store the type and continue processing
		msg.TextContent = ""
		msg.Sender = "unknown"
	}

	return msg, nil
}
