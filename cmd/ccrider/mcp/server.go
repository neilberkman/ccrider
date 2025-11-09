package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/yourusername/ccrider/internal/core/db"
)

// SearchSessionsArgs defines arguments for the search_sessions tool
type SearchSessionsArgs struct {
	Query   string `json:"query" jsonschema:"description=Search term to match against message content,required"`
	Limit   int    `json:"limit,omitempty" jsonschema:"description=Max number of sessions to return (default: 10)"`
	Project string `json:"project,omitempty" jsonschema:"description=Filter by project path"`
}

// GetSessionDetailArgs defines arguments for the get_session_detail tool
type GetSessionDetailArgs struct {
	SessionID string `json:"session_id" jsonschema:"description=Session UUID to retrieve,required"`
}

// ListRecentSessionsArgs defines arguments for the list_recent_sessions tool
type ListRecentSessionsArgs struct {
	Limit   int    `json:"limit,omitempty" jsonschema:"description=Max sessions to return (default: 20)"`
	Project string `json:"project,omitempty" jsonschema:"description=Filter by project path"`
}

// SessionMatch represents a session search result
type SessionMatch struct {
	SessionID  string        `json:"session_id"`
	Summary    string        `json:"summary"`
	Project    string        `json:"project"`
	UpdatedAt  string        `json:"updated_at"`
	MatchCount int           `json:"match_count"`
	Matches    []MatchSnippet `json:"matches"`
}

// MatchSnippet represents a message match within a session
type MatchSnippet struct {
	MessageType string `json:"message_type"`
	Snippet     string `json:"snippet"`
	Sequence    int    `json:"sequence"`
}

// SessionDetail represents a full session with messages
type SessionDetail struct {
	SessionID    string            `json:"session_id"`
	Summary      string            `json:"summary"`
	Project      string            `json:"project"`
	CreatedAt    string            `json:"created_at"`
	UpdatedAt    string            `json:"updated_at"`
	MessageCount int               `json:"message_count"`
	Messages     []MessageDetail   `json:"messages"`
}

// MessageDetail represents a single message in a session
type MessageDetail struct {
	Type      string `json:"type"`
	Content   string `json:"content"`
	Timestamp string `json:"timestamp"`
	Sequence  int    `json:"sequence"`
}

// SessionSummary represents a session in the list view
type SessionSummary struct {
	SessionID    string `json:"session_id"`
	Summary      string `json:"summary"`
	Project      string `json:"project"`
	UpdatedAt    string `json:"updated_at"`
	MessageCount int    `json:"message_count"`
}

// StartServer starts the MCP server
func StartServer(dbPath string) error {
	// Open database
	database, err := db.New(dbPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer database.Close()

	// Create MCP server
	s := server.NewMCPServer(
		"CCRider",
		"1.0.0",
	)

	// Register search_sessions tool
	searchTool := mcp.NewTool("search_sessions",
		mcp.WithDescription("Search Claude Code sessions for a query string across all message content"),
		mcp.WithString("query",
			mcp.Required(),
			mcp.Description("Search term to match against message content")),
		mcp.WithNumber("limit",
			mcp.Description("Max number of sessions to return (default: 10)")),
		mcp.WithString("project",
			mcp.Description("Filter by project path")),
	)
	s.AddTool(searchTool, makeSearchSessionsHandler(database))

	// Register get_session_detail tool
	detailTool := mcp.NewTool("get_session_detail",
		mcp.WithDescription("Retrieve full conversation for a specific Claude Code session"),
		mcp.WithString("session_id",
			mcp.Required(),
			mcp.Description("Session UUID to retrieve")),
	)
	s.AddTool(detailTool, makeGetSessionDetailHandler(database))

	// Register list_recent_sessions tool
	listTool := mcp.NewTool("list_recent_sessions",
		mcp.WithDescription("Get recent Claude Code sessions, optionally filtered by project"),
		mcp.WithNumber("limit",
			mcp.Description("Max sessions to return (default: 20)")),
		mcp.WithString("project",
			mcp.Description("Filter by project path")),
	)
	s.AddTool(listTool, makeListRecentSessionsHandler(database))

	return server.ServeStdio(s)
}

func makeSearchSessionsHandler(database *db.DB) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args SearchSessionsArgs
		argsBytes, _ := json.Marshal(request.Params.Arguments)
		if err := json.Unmarshal(argsBytes, &args); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid arguments: %v", err)), nil
		}

		// Set defaults
		limit := args.Limit
		if limit == 0 {
			limit = 10
		}

		// Build query
		query := `
			SELECT
				s.session_id,
				COALESCE(s.summary, '') as summary,
				s.project_path,
				s.updated_at,
				m.type as message_type,
				SUBSTR(m.text_content, 1, 200) as snippet,
				m.sequence
			FROM messages m
			JOIN sessions s ON m.session_id = s.id
			WHERE m.text_content LIKE '%' || ? || '%'
		`

		if args.Project != "" {
			query += " AND s.project_path = ?"
		}

		query += " ORDER BY s.updated_at DESC, m.sequence ASC LIMIT 200"

		// Execute query
		var rows *sql.Rows
		var err error
		if args.Project != "" {
			rows, err = database.Query(query, args.Query, args.Project)
		} else {
			rows, err = database.Query(query, args.Query)
		}
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("search failed: %v", err)), nil
		}
		defer rows.Close()

		// Group matches by session
		sessionMap := make(map[string]*SessionMatch)
		seenMessages := make(map[string]map[int]bool)
		var sessionOrder []string

		for rows.Next() {
			var sessionID, summary, project, updatedAt, msgType, snippet string
			var sequence int
			if err := rows.Scan(&sessionID, &summary, &project, &updatedAt,
				&msgType, &snippet, &sequence); err != nil {
				continue
			}

			// Create or get existing session result
			result, exists := sessionMap[sessionID]
			if !exists {
				result = &SessionMatch{
					SessionID: sessionID,
					Summary:   summary,
					Project:   project,
					UpdatedAt: updatedAt,
					Matches:   []MatchSnippet{},
				}
				sessionMap[sessionID] = result
				sessionOrder = append(sessionOrder, sessionID)
				seenMessages[sessionID] = make(map[int]bool)
			}

			// Skip if we've already seen this message
			if seenMessages[sessionID][sequence] {
				continue
			}

			// Add this match (limit to 3 distinct messages per session)
			if len(result.Matches) < 3 {
				result.Matches = append(result.Matches, MatchSnippet{
					MessageType: msgType,
					Snippet:     snippet,
					Sequence:    sequence,
				})
				seenMessages[sessionID][sequence] = true
			}
		}

		// Convert map to slice with match counts
		var results []SessionMatch
		for _, sessionID := range sessionOrder {
			match := sessionMap[sessionID]
			match.MatchCount = len(match.Matches)
			results = append(results, *match)
			if len(results) >= limit {
				break
			}
		}

		// Return results as JSON
		resultJSON, err := json.Marshal(map[string]interface{}{
			"sessions": results,
		})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal results: %v", err)), nil
		}

		return mcp.NewToolResultText(string(resultJSON)), nil
	}
}

func makeGetSessionDetailHandler(database *db.DB) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args GetSessionDetailArgs
		argsBytes, _ := json.Marshal(request.Params.Arguments)
		if err := json.Unmarshal(argsBytes, &args); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid arguments: %v", err)), nil
		}

		// Get session info
		var session SessionDetail
		err := database.QueryRow(`
			SELECT
				session_id,
				COALESCE(summary, ''),
				project_path,
				created_at,
				updated_at,
				(SELECT COUNT(*) FROM messages WHERE session_id = sessions.id) as message_count
			FROM sessions
			WHERE session_id = ?
		`, args.SessionID).Scan(&session.SessionID, &session.Summary, &session.Project,
			&session.CreatedAt, &session.UpdatedAt, &session.MessageCount)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("session not found: %v", err)), nil
		}

		// Get messages
		rows, err := database.Query(`
			SELECT type, COALESCE(text_content, ''), timestamp, sequence
			FROM messages
			WHERE session_id = (SELECT id FROM sessions WHERE session_id = ?)
			ORDER BY sequence ASC
		`, args.SessionID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to get messages: %v", err)), nil
		}
		defer rows.Close()

		session.Messages = []MessageDetail{}
		for rows.Next() {
			var msg MessageDetail
			if err := rows.Scan(&msg.Type, &msg.Content, &msg.Timestamp, &msg.Sequence); err != nil {
				continue
			}
			session.Messages = append(session.Messages, msg)
		}

		// Return result as JSON
		resultJSON, err := json.Marshal(session)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal result: %v", err)), nil
		}

		return mcp.NewToolResultText(string(resultJSON)), nil
	}
}

func makeListRecentSessionsHandler(database *db.DB) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args ListRecentSessionsArgs
		argsBytes, _ := json.Marshal(request.Params.Arguments)
		if err := json.Unmarshal(argsBytes, &args); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid arguments: %v", err)), nil
		}

		// Set defaults
		limit := args.Limit
		if limit == 0 {
			limit = 20
		}

		// Build query
		query := `
			SELECT
				s.session_id,
				COALESCE(s.summary, '') as summary,
				s.project_path,
				s.updated_at,
				(SELECT COUNT(*) FROM messages WHERE session_id = s.id) as message_count
			FROM sessions s
			WHERE (SELECT COUNT(*) FROM messages WHERE session_id = s.id) > 0
		`

		if args.Project != "" {
			query += " AND s.project_path = ?"
		}

		query += " ORDER BY s.updated_at DESC LIMIT ?"

		// Execute query
		var rows *sql.Rows
		var err error
		if args.Project != "" {
			rows, err = database.Query(query, args.Project, limit)
		} else {
			rows, err = database.Query(query, limit)
		}
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("query failed: %v", err)), nil
		}
		defer rows.Close()

		var sessions []SessionSummary
		for rows.Next() {
			var s SessionSummary
			if err := rows.Scan(&s.SessionID, &s.Summary, &s.Project,
				&s.UpdatedAt, &s.MessageCount); err != nil {
				continue
			}
			sessions = append(sessions, s)
		}

		// Return results as JSON
		resultJSON, err := json.Marshal(map[string]interface{}{
			"sessions": sessions,
		})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal results: %v", err)), nil
		}

		return mcp.NewToolResultText(string(resultJSON)), nil
	}
}
