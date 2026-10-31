package sessionevents

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// Block is one content block: its type, and its text for text blocks.
type Block struct {
	Type string
	Text string
}

// IsJSONString reports whether raw is a JSON string. encoding/json decodes
// null into a string without error, so decoders that must reject null use
// this first.
func IsJSONString(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) > 0 && raw[0] == '"'
}

// IsJSONNull reports whether raw is absent or JSON null.
func IsJSONNull(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) == 0 || bytes.Equal(raw, []byte("null"))
}

// ParseBlocks parses a JSON array of content blocks strictly. Every element
// must be an object whose "type" is a string, and blocks of type textType
// must have a string "text". null in any of those places is an error, where
// encoding/json alone would accept it as empty.
func ParseBlocks(raw json.RawMessage, textType string) ([]Block, error) {
	if IsJSONNull(raw) {
		return nil, errors.New("content is missing or null")
	}
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("content: %w", err)
	}
	blocks := make([]Block, 0, len(items))
	for i, item := range items {
		if item == nil {
			return nil, fmt.Errorf("content block %d is null", i)
		}
		if !IsJSONString(item["type"]) {
			return nil, fmt.Errorf("content block %d has no string type", i)
		}
		var b Block
		_ = json.Unmarshal(item["type"], &b.Type)
		if b.Type == textType {
			if !IsJSONString(item["text"]) {
				return nil, fmt.Errorf("content block %d has no string text", i)
			}
			_ = json.Unmarshal(item["text"], &b.Text)
		}
		blocks = append(blocks, b)
	}
	return blocks, nil
}
