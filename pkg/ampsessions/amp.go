// Package ampsessions parses Amp thread exports into the shared session model.
package ampsessions

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/neilberkman/ccrider/pkg/ccsessions"
)

type rawThread struct {
	ID        string             `json:"id"`
	Title     string             `json:"title"`
	Created   json.RawMessage    `json:"created"`
	UpdatedAt json.RawMessage    `json:"updatedAt"`
	Messages  *[]json.RawMessage `json:"messages"`
	Env       struct {
		Initial struct {
			Trees []struct {
				URI        string `json:"uri"`
				Repository struct {
					Ref string `json:"ref"`
				} `json:"repository"`
			} `json:"trees"`
			Platform struct {
				ClientVersion string `json:"clientVersion"`
			} `json:"platform"`
		} `json:"initial"`
	} `json:"env"`
}

type rawMessage struct {
	Role      string            `json:"role"`
	MessageID json.RawMessage   `json:"messageId"`
	Content   []json.RawMessage `json:"content"`
	Meta      struct {
		SentAt json.RawMessage `json:"sentAt"`
	} `json:"meta"`
}

type blockEnvelope struct {
	Type      string          `json:"type"`
	StartTime json.RawMessage `json:"startTime"`
}

type textBlock struct {
	Text *string `json:"text"`
}

// Parse converts one `amp threads export` document into ccrider's shared
// session model. Unknown fields and content block types are intentionally
// ignored so additive Amp schema changes do not break imports.
func Parse(data []byte) (*ccsessions.ParsedSession, error) {
	var thread rawThread
	if err := json.Unmarshal(data, &thread); err != nil {
		return nil, fmt.Errorf("decode export JSON: %w", err)
	}
	if strings.TrimSpace(thread.ID) == "" {
		return nil, fmt.Errorf("decode export JSON: thread id is missing")
	}
	if thread.Messages == nil {
		return nil, fmt.Errorf("decode export JSON: messages are missing or null")
	}

	projectPath, gitBranch := initialTree(thread)
	version := thread.Env.Initial.Platform.ClientVersion
	updatedAt := parseFlexibleTime(thread.UpdatedAt)
	createdAt := parseFlexibleTime(thread.Created)

	rawMessages := *thread.Messages
	messages := make([]ccsessions.ParsedMessage, 0, len(rawMessages))
	seenIDs := make(map[string]struct{}, len(rawMessages))
	for sequence, raw := range rawMessages {
		var message rawMessage
		if err := json.Unmarshal(raw, &message); err != nil {
			return nil, fmt.Errorf("decode message %d: %w", sequence+1, err)
		}

		msgType, sender := role(message.Role)
		if msgType == "" {
			continue
		}

		text, blockTime, err := messageText(message.Content)
		if err != nil {
			return nil, fmt.Errorf("decode message %d content: %w", sequence+1, err)
		}
		if strings.TrimSpace(text) == "" {
			continue
		}

		messageID, err := messageIdentity(message.MessageID, sequence+1)
		if err != nil {
			return nil, fmt.Errorf("decode message %d id: %w", sequence+1, err)
		}
		uuid := ccsessions.DeterministicUUID("amp:" + thread.ID + ":" + messageID)
		if _, exists := seenIDs[uuid]; exists {
			return nil, fmt.Errorf("decode message %d id: duplicate message id %q", sequence+1, messageID)
		}
		seenIDs[uuid] = struct{}{}

		timestamp := parseFlexibleTime(message.Meta.SentAt)
		if timestamp.IsZero() {
			timestamp = blockTime
		}

		messages = append(messages, ccsessions.ParsedMessage{
			UUID:        uuid,
			Type:        msgType,
			Sender:      sender,
			Content:     append(json.RawMessage(nil), raw...),
			TextContent: text,
			Timestamp:   timestamp,
			Sequence:    sequence + 1,
			CWD:         projectPath,
			GitBranch:   gitBranch,
			Version:     version,
		})
	}
	if len(rawMessages) > 0 && len(messages) == 0 {
		return nil, fmt.Errorf("decode export JSON: thread has no text-bearing conversation messages")
	}

	summary := strings.TrimSpace(thread.Title)
	if summary == "" {
		summary = ccsessions.FirstUserSummary(messages)
	}
	mtime := updatedAt
	if mtime.IsZero() {
		mtime = createdAt
	}

	return &ccsessions.ParsedSession{
		SessionID:   thread.ID,
		ImportID:    thread.ID,
		ProjectPath: projectPath,
		Summary:     summary,
		FilePath:    "amp://threads/" + url.PathEscape(thread.ID),
		FileSize:    int64(len(data)),
		FileMtime:   mtime,
		Messages:    messages,
	}, nil
}

func initialTree(thread rawThread) (projectPath, gitBranch string) {
	if len(thread.Env.Initial.Trees) == 0 {
		return "", ""
	}
	tree := thread.Env.Initial.Trees[0]
	if parsed, err := url.Parse(tree.URI); err == nil && parsed.Scheme == "file" {
		projectPath = parsed.Path
	}
	gitBranch = strings.TrimPrefix(tree.Repository.Ref, "refs/heads/")
	return projectPath, gitBranch
}

func role(value string) (msgType, sender string) {
	switch value {
	case "user":
		return "user", "human"
	case "assistant":
		return "assistant", "assistant"
	default:
		return "", ""
	}
}

func messageText(content []json.RawMessage) (string, time.Time, error) {
	var parts []string
	var earliest time.Time
	for index, raw := range content {
		var envelope blockEnvelope
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return "", time.Time{}, fmt.Errorf("block %d: %w", index+1, err)
		}
		start := parseFlexibleTime(envelope.StartTime)
		if !start.IsZero() && (earliest.IsZero() || start.Before(earliest)) {
			earliest = start
		}
		if envelope.Type == "text" {
			var block textBlock
			if err := json.Unmarshal(raw, &block); err != nil {
				return "", time.Time{}, fmt.Errorf("text block %d: %w", index+1, err)
			}
			if block.Text == nil {
				return "", time.Time{}, fmt.Errorf("text block %d: text is missing", index+1)
			}
			if strings.TrimSpace(*block.Text) != "" {
				parts = append(parts, *block.Text)
			}
		}
	}
	return strings.Join(parts, "\n"), earliest, nil
}

func flexibleID(raw json.RawMessage) (string, error) {
	var value string
	if err := json.Unmarshal(raw, &value); err == nil {
		if value == "" {
			return "", fmt.Errorf("message id is empty")
		}
		return value, nil
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err == nil {
		return number.String(), nil
	}
	return "", fmt.Errorf("unsupported message id %s", string(raw))
}

func messageIdentity(raw json.RawMessage, sequence int) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		// Legacy Amp exports omit messageId entirely. Array position is the only
		// identity available in that schema; the NUL namespace cannot collide
		// with any existing ID-based UUID generated by earlier ccrider versions.
		return "\x00sequence:" + strconv.Itoa(sequence), nil
	}
	return flexibleID(raw)
}

func parseFlexibleTime(raw json.RawMessage) time.Time {
	if len(raw) == 0 || string(raw) == "null" {
		return time.Time{}
	}
	var millis int64
	if err := json.Unmarshal(raw, &millis); err == nil {
		return time.UnixMilli(millis).UTC()
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC()
		}
	}
	return time.Time{}
}
