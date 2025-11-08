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
	SessionID   string
	Summary     string
	LeafUUID    string
	Messages    []ParsedMessage
	FilePath    string
	FileSize    int64
	FileMtime   time.Time
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
func ParseFile(path string) (*ParsedSession, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	// Get file info for metadata
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("failed to stat file: %w", err)
	}

	// Extract session ID from filename
	sessionID := filepath.Base(path)
	sessionID = sessionID[:len(sessionID)-len(filepath.Ext(sessionID))]

	session := &ParsedSession{
		SessionID: sessionID,
		FilePath:  path,
		FileSize:  info.Size(),
		FileMtime: info.ModTime(),
		Messages:  make([]ParsedMessage, 0),
	}

	scanner := bufio.NewScanner(file)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Bytes()

		var raw rawEntry
		if err := json.Unmarshal(line, &raw); err != nil {
			return nil, fmt.Errorf("line %d: failed to parse JSON: %w", lineNum, err)
		}

		// First line is always summary
		if lineNum == 1 {
			if raw.Type != "summary" {
				return nil, fmt.Errorf("first line must be summary, got %s", raw.Type)
			}
			session.Summary = raw.Summary
			session.LeafUUID = raw.LeafUUID
			continue
		}

		// Parse message entries
		msg, err := parseMessage(&raw, lineNum)
		if err != nil {
			// Log warning but don't fail - some message types we may not support yet
			fmt.Fprintf(os.Stderr, "Warning: line %d: %v\n", lineNum, err)
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
		var userMsg struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal(raw.Message, &userMsg); err == nil {
			msg.TextContent = userMsg.Content
			msg.Sender = "human"
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

	case "system", "file-history-snapshot":
		// These types don't have extractable text content
		msg.TextContent = ""

	default:
		return nil, fmt.Errorf("unknown message type: %s", raw.Type)
	}

	return msg, nil
}
