package opencodesessions

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/neilberkman/ccrider/pkg/ccsessions"
)

// OpenCode's v2 runtime writes each conversation to session_message instead
// of message/part. It ships two ways, in the same opencode*.db file:
//
//   - OpenCode 1.18's opt-in desktop sidecar (OPENCODE_SIDECAR_V2=1) keeps
//     sessions in v1's session table.
//   - OpenCode 2.0 (the @opencode-ai/cli preview) keeps them in session_v2,
//     added next to v1's session table. A DB it creates from scratch has no
//     v1 tables at all.
//
// Neither copies v1 history into session_message, so one DB can hold
// sessions of both kinds; each session is read from whichever table has its
// messages.
//
// session_message columns: id, session_id, type, seq, time_created,
// time_updated, data. type is the message kind and is stripped from data;
// seq is the session's event sequence and is the canonical order.
// Shapes are from packages/schema/src/session-message.ts in OpenCode.

type v2MessageRow struct {
	ID        string
	Type      string
	Seq       int64
	CreatedMS int64
	Data      jsonValue
}

type v2FileAttachment struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      *struct {
		Text string `json:"text"`
	} `json:"source"`
}

type v2Message struct {
	// user, synthetic, system
	Text  string             `json:"text"`
	Files []v2FileAttachment `json:"files"`
	// assistant
	Content []v2AssistantContent `json:"content"`
	// shell
	Command string `json:"command"`
	Output  string `json:"output"`
	// compaction
	Summary string `json:"summary"`
}

type v2AssistantContent struct {
	Type  string           `json:"type"` // text, reasoning, tool
	Text  string           `json:"text"`
	Name  string           `json:"name"`
	State *v2ToolStateJSON `json:"state"`
}

type v2ToolStateJSON struct {
	Status  string          `json:"status"` // pending, running, completed, error
	Content []v2ToolContent `json:"content"`
	Error   json.RawMessage `json:"error"`
}

type v2ToolContent struct {
	Type string `json:"type"` // text, file
	Text string `json:"text"`
	URI  string `json:"uri"`
	Name string `json:"name"`
}

// hasV2Messages reports whether the DB has the v2 session_message table in
// its current shape. The table has existed since OpenCode 1.16, but seq was
// added later; earlier rows were deleted by OpenCode's own migrations.
func hasV2Messages(conn *sql.DB) (bool, error) {
	rows, err := conn.Query(`SELECT name FROM pragma_table_info('session_message')`)
	if err != nil {
		return false, fmt.Errorf("inspect opencode session_message: %w", err)
	}
	defer func() { _ = rows.Close() }()

	need := map[string]bool{"session_id": false, "type": false, "seq": false, "data": false}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return false, fmt.Errorf("inspect opencode session_message: %w", err)
		}
		if _, ok := need[name]; ok {
			need[name] = true
		}
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("inspect opencode session_message: %w", err)
	}
	for _, found := range need {
		if !found {
			return false, nil
		}
	}
	return true, nil
}

// parseV2Messages reads a session's v2 messages in seq order. It returns nil
// when the session has none, so the caller falls back to the v1 tables.
func parseV2Messages(conn *sql.DB, session sessionRow) ([]ccsessions.ParsedMessage, error) {
	rows, err := conn.Query(`
		SELECT id, type, seq, COALESCE(time_created, 0), data
		FROM session_message
		WHERE session_id = ?
		ORDER BY seq ASC, id ASC
	`, session.ID)
	if err != nil {
		return nil, fmt.Errorf("query opencode v2 messages for %s: %w", session.ID, err)
	}
	defer func() { _ = rows.Close() }()

	cwd := session.Directory
	if cwd == "" {
		cwd = session.ProjectRoot
	}

	var messages []ccsessions.ParsedMessage
	var parentID string
	for rows.Next() {
		var row v2MessageRow
		if err := rows.Scan(&row.ID, &row.Type, &row.Seq, &row.CreatedMS, &row.Data); err != nil {
			return nil, fmt.Errorf("scan opencode v2 message for %s: %w", session.ID, err)
		}
		msgType, sender, text := v2MessageText(row)
		if msgType == "" || strings.TrimSpace(text) == "" {
			// Model/agent switches and system text aren't conversation, and an
			// in-flight assistant row may have no content yet; it self-heals on
			// the next sync because enumerated providers use a content hash.
			continue
		}

		content, _ := json.Marshal(map[string]any{
			"type": row.Type,
			"seq":  row.Seq,
			"data": row.Data.RawMessage(),
		})
		messages = append(messages, ccsessions.ParsedMessage{
			UUID:        row.ID,
			ParentUUID:  parentID,
			Type:        msgType,
			Sender:      sender,
			Content:     content,
			TextContent: text,
			Timestamp:   timeFromMillis(row.CreatedMS),
			Sequence:    len(messages) + 1,
			CWD:         cwd,
			Version:     session.Version,
		})
		parentID = row.ID
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read opencode v2 messages for %s: %w", session.ID, err)
	}
	return messages, nil
}

// v2MessageText maps one session_message row to ccrider's message type,
// sender and searchable text. Unknown or non-conversation types return "".
func v2MessageText(row v2MessageRow) (msgType, sender, text string) {
	var msg v2Message
	if err := json.Unmarshal(row.Data.RawMessage(), &msg); err != nil {
		return "", "", ""
	}

	var texts []string
	switch row.Type {
	case "user":
		appendText(&texts, msg.Text)
		for _, file := range msg.Files {
			if file.Source != nil {
				appendText(&texts, file.Source.Text)
			}
		}
		return "user", "human", strings.Join(texts, "\n\n")

	case "synthetic":
		// Text OpenCode adds to the user's turn, as v1 did with synthetic parts.
		return "user", "human", strings.TrimSpace(msg.Text)

	case "shell":
		// A command the user ran with `!` in the prompt.
		if strings.TrimSpace(msg.Command) != "" {
			appendText(&texts, "$ "+strings.TrimSpace(msg.Command))
		}
		appendText(&texts, msg.Output)
		return "user", "human", strings.Join(texts, "\n\n")

	case "assistant":
		for _, part := range msg.Content {
			switch part.Type {
			case "text":
				appendText(&texts, part.Text)
			case "tool":
				appendText(&texts, v2ToolText(part.State))
			}
			// reasoning is skipped, matching the v1 parser.
		}
		return "assistant", "assistant", strings.Join(texts, "\n\n")

	case "compaction":
		// v1 stored the compaction summary as an assistant message.
		return "assistant", "assistant", strings.TrimSpace(msg.Summary)
	}
	return "", "", ""
}

func v2ToolText(state *v2ToolStateJSON) string {
	if state == nil {
		return ""
	}
	var texts []string
	switch state.Status {
	case "completed", "error":
		for _, c := range state.Content {
			if c.Type == "text" {
				appendText(&texts, c.Text)
			}
		}
	}
	if state.Status == "error" {
		appendText(&texts, v2ErrorText(state.Error))
	}
	return strings.Join(texts, "\n\n")
}

// v2ErrorText reads a tool error, which OpenCode stores either as a string
// or as an object with a message.
func v2ErrorText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var obj struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil {
		return obj.Message
	}
	return ""
}
