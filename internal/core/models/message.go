package models

import (
	"encoding/json"
	"time"
)

// MessageType represents the type of JSONL entry
type MessageType string

const (
	MessageTypeSummary             MessageType = "summary"
	MessageTypeUser                MessageType = "user"
	MessageTypeAssistant           MessageType = "assistant"
	MessageTypeSystem              MessageType = "system"
	MessageTypeFileHistorySnapshot MessageType = "file-history-snapshot"
)

// Message represents a single entry in a session JSONL file
type Message struct {
	ID          int64
	UUID        string
	SessionID   int64
	ParentUUID  string
	Type        MessageType
	Sender      string          // "human" or "assistant" for user/assistant types
	Content     json.RawMessage // Full message content as JSON
	TextContent string          // Extracted text for FTS
	Timestamp   time.Time
	Sequence    int
	IsSidechain bool
	CWD         string
	GitBranch   string
	Version     string
}

// UserMessage represents a parsed user message
type UserMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// AssistantMessage represents a parsed assistant message
type AssistantMessage struct {
	Model      string          `json:"model"`
	ID         string          `json:"id"`
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"` // Array of content blocks
	StopReason string          `json:"stop_reason"`
	Usage      TokenUsage      `json:"usage"`
}

// TokenUsage tracks API token usage
type TokenUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// ContentBlock represents a content block in assistant messages
type ContentBlock struct {
	Type  string          `json:"type"` // "text" or "tool_use"
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

// SummaryEntry is the first line of every session file
type SummaryEntry struct {
	Type     string `json:"type"`
	Summary  string `json:"summary"`
	LeafUUID string `json:"leafUuid"`
}
