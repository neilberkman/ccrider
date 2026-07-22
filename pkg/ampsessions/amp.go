// Package ampsessions imports Amp threads through Amp's supported CLI.
//
// Amp threads are cloud-backed, so ccrider first lists lightweight thread
// references and only exports threads whose revision changed since the last
// sync. The Amp CLI owns authentication, including AMP_API_KEY handling.
package ampsessions

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/neilberkman/ccrider/pkg/ccsessions"
)

const (
	Provider        = "amp"
	defaultPageSize = 100
	commandTimeout  = 30 * time.Second
)

// ThreadRef is the lightweight metadata returned by `amp threads list`.
type ThreadRef struct {
	ID       string
	Revision string
}

// runFunc invokes the Amp CLI with the supplied arguments. Keeping the runner
// injectable makes pagination and error handling testable without an Amp
// account or network access.
type runFunc func(context.Context, ...string) ([]byte, error)

// Client accesses Amp threads through the installed Amp CLI.
type Client struct {
	run      runFunc
	pageSize int
	timeout  time.Duration
}

// NewClient creates a client backed by the `amp` executable on PATH.
func NewClient() *Client {
	return &Client{
		run:      runAmp,
		pageSize: defaultPageSize,
		timeout:  commandTimeout,
	}
}

func newClient(run runFunc, pageSize int) *Client {
	return &Client{run: run, pageSize: pageSize, timeout: commandTimeout}
}

func runAmp(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "amp", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err == nil {
		return output, nil
	}
	message := strings.TrimSpace(stderr.String())
	if message == "" {
		return nil, fmt.Errorf("run amp command: %w", err)
	}
	return nil, fmt.Errorf("run amp command: %w: %s", err, message)
}

// ListThreads returns every thread visible to the authenticated Amp user,
// including archived threads. Results are paginated and deduplicated. A thread
// moved across offsets during a concurrent account change may appear on the
// next sync; missing list entries never delete cached sessions.
func (c *Client) ListThreads() ([]ThreadRef, error) {
	seen := make(map[string]struct{})
	var refs []ThreadRef

	for offset := 0; ; offset += c.pageSize {
		ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
		output, err := c.run(ctx,
			"threads", "list", "--json", "--include-archived",
			"--limit", strconv.Itoa(c.pageSize), "--offset", strconv.Itoa(offset),
		)
		cancel()
		if err != nil {
			return nil, fmt.Errorf("list Amp threads: %w", err)
		}

		var page []threadListItem
		if err := json.Unmarshal(output, &page); err != nil {
			return nil, fmt.Errorf("parse Amp thread list at offset %d: %w", offset, err)
		}

		added := 0
		for _, item := range page {
			if strings.TrimSpace(item.ID) == "" {
				return nil, fmt.Errorf("parse Amp thread list at offset %d: thread id is missing", offset)
			}
			revision, err := item.revision()
			if err != nil {
				return nil, fmt.Errorf("parse Amp thread list at offset %d: %w", offset, err)
			}
			if _, ok := seen[item.ID]; ok {
				continue
			}
			seen[item.ID] = struct{}{}
			refs = append(refs, ThreadRef{ID: item.ID, Revision: revision})
			added++
		}

		if len(page) < c.pageSize {
			break
		}
		if added == 0 {
			return nil, fmt.Errorf("list Amp threads: pagination made no progress at offset %d", offset)
		}
	}

	return refs, nil
}

// ExportThread downloads and parses one Amp thread by id.
func (c *Client) ExportThread(threadID string) (*ccsessions.ParsedSession, error) {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	output, err := c.run(ctx, "threads", "export", threadID)
	if err != nil {
		return nil, fmt.Errorf("export Amp thread %s: %w", threadID, err)
	}
	session, err := Parse(output)
	if err != nil {
		return nil, fmt.Errorf("parse Amp thread %s: %w", threadID, err)
	}
	if strings.TrimSpace(session.SessionID) != strings.TrimSpace(threadID) {
		return nil, fmt.Errorf("parse Amp thread %s: export contained thread id %q", threadID, session.SessionID)
	}
	return session, nil
}

type threadListItem struct {
	ID           string `json:"id"`
	Updated      string `json:"updated"`
	UpdatedAt    string `json:"updatedAt"`
	MessageCount *int   `json:"messageCount"`
}

func (item threadListItem) revision() (string, error) {
	updated := strings.TrimSpace(item.Updated)
	if updated == "" {
		updated = strings.TrimSpace(item.UpdatedAt)
	}
	if updated == "" {
		return "", fmt.Errorf("thread %s has no updated timestamp", item.ID)
	}
	if item.MessageCount == nil {
		return "", fmt.Errorf("thread %s has no message count", item.ID)
	}
	if *item.MessageCount < 0 {
		return "", fmt.Errorf("thread %s has invalid message count %d", item.ID, *item.MessageCount)
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%d", item.ID, updated, *item.MessageCount)))
	return hex.EncodeToString(sum[:]), nil
}

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
	for sequence, raw := range rawMessages {
		var message rawMessage
		if err := json.Unmarshal(raw, &message); err != nil {
			return nil, fmt.Errorf("decode message %d: %w", sequence+1, err)
		}

		msgType, sender := role(message.Role)
		if msgType == "" {
			continue
		}
		messageID, err := flexibleID(message.MessageID)
		if err != nil {
			return nil, fmt.Errorf("decode message %d id: %w", sequence+1, err)
		}

		text, blockTime, err := messageText(message.Content)
		if err != nil {
			return nil, fmt.Errorf("decode message %d content: %w", sequence+1, err)
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		timestamp := parseFlexibleTime(message.Meta.SentAt)
		if timestamp.IsZero() {
			timestamp = blockTime
		}

		messages = append(messages, ccsessions.ParsedMessage{
			UUID:        ccsessions.DeterministicUUID("amp:" + thread.ID + ":" + messageID),
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
	if len(raw) == 0 || string(raw) == "null" {
		return "", fmt.Errorf("message id is missing")
	}
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
