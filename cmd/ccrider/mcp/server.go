package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/yourusername/ccrider/internal/core/db"
	"github.com/yourusername/ccrider/internal/core/importer"
)

// SearchSessionsArgs defines arguments for the search_sessions tool
type SearchSessionsArgs struct {
	Query            string `json:"query" jsonschema:"description=Search term to match against message content,required"`
	Limit            int    `json:"limit,omitempty" jsonschema:"description=Max number of sessions to return (default: 10)"`
	Project          string `json:"project,omitempty" jsonschema:"description=Filter by project path"`
	CurrentSessionID string `json:"current_session_id,omitempty" jsonschema:"description=Current session ID to search within (searches only this session)"`
	ExcludeCurrent   bool   `json:"exclude_current,omitempty" jsonschema:"description=Exclude current session from results (searches only other sessions)"`
	AfterDate        string `json:"after_date,omitempty" jsonschema:"description=Only sessions updated after this date (ISO 8601 format, e.g. 2025-01-01)"`
	BeforeDate       string `json:"before_date,omitempty" jsonschema:"description=Only sessions updated before this date (ISO 8601 format)"`
}

// GetSessionDetailArgs defines arguments for the get_session_detail tool
type GetSessionDetailArgs struct {
	SessionID   string `json:"session_id" jsonschema:"description=Session UUID to retrieve,required"`
	SearchQuery string `json:"search_query,omitempty" jsonschema:"description=Optional search term to find matching messages"`
}

// ListRecentSessionsArgs defines arguments for the list_recent_sessions tool
type ListRecentSessionsArgs struct {
	Limit   int    `json:"limit,omitempty" jsonschema:"description=Max sessions to return (default: 20)"`
	Project string `json:"project,omitempty" jsonschema:"description=Filter by project path"`
}

// SessionMatch represents a session search result
type SessionMatch struct {
	SessionID  string         `json:"session_id"`
	Summary    string         `json:"summary"`
	Project    string         `json:"project"`
	UpdatedAt  string         `json:"updated_at"`
	MatchCount int            `json:"match_count"`
	Matches    []MatchSnippet `json:"matches"`
}

// MatchSnippet represents a message match within a session
type MatchSnippet struct {
	MessageType string `json:"message_type"`
	Snippet     string `json:"snippet"`
	Sequence    int    `json:"sequence"`
}

// SessionDetail represents a session with key messages (not full conversation)
type SessionDetail struct {
	SessionID        string          `json:"session_id"`
	Summary          string          `json:"summary"`
	Project          string          `json:"project"`
	CreatedAt        string          `json:"created_at"`
	UpdatedAt        string          `json:"updated_at"`
	MessageCount     int             `json:"message_count"`
	FirstMessage     *MessageDetail  `json:"first_message,omitempty"`
	LastMessage      *MessageDetail  `json:"last_message,omitempty"`
	MatchingMessages []MessageDetail `json:"matching_messages,omitempty"`
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
		mcp.WithDescription("Search Claude Code sessions for a query string across all message content. Can search current session only, exclude current session, or search all sessions. Supports date and project filtering."),
		mcp.WithString("query",
			mcp.Required(),
			mcp.Description("Search term to match against message content")),
		mcp.WithNumber("limit",
			mcp.Description("Max number of sessions to return (default: 10)")),
		mcp.WithString("project",
			mcp.Description("Filter by project path")),
		mcp.WithString("current_session_id",
			mcp.Description("Current session ID - if provided, searches ONLY within this session (useful for finding earlier parts of current conversation)")),
		mcp.WithBoolean("exclude_current",
			mcp.Description("If true, excludes current session from results (searches only other sessions). Requires current_session_id to be set.")),
		mcp.WithString("after_date",
			mcp.Description("Only sessions updated after this date (ISO 8601 format, e.g. '2025-01-01' or '2025-01-08T10:00:00Z')")),
		mcp.WithString("before_date",
			mcp.Description("Only sessions updated before this date (ISO 8601 format)")),
	)
	s.AddTool(searchTool, makeSearchSessionsHandler(database))

	// Register get_session_detail tool
	detailTool := mcp.NewTool("get_session_detail",
		mcp.WithDescription("Retrieve session info with first message, last message, and optionally matching messages for a specific Claude Code session"),
		mcp.WithString("session_id",
			mcp.Required(),
			mcp.Description("Session UUID to retrieve")),
		mcp.WithString("search_query",
			mcp.Description("Optional search term to find matching messages in the session")),
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

// syncDatabase ensures the database is up-to-date before running tool queries
func syncDatabase(ctx context.Context, database *db.DB) error {
	// Get Claude Code projects directory
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home dir: %w", err)
	}
	sourcePath := filepath.Join(home, ".claude", "projects")

	// Import from Claude directory (silent, no progress output for MCP)
	imp := importer.New(database)
	if err := imp.ImportDirectory(sourcePath, nil); err != nil {
		return fmt.Errorf("failed to sync: %w", err)
	}

	return nil
}

func makeSearchSessionsHandler(database *db.DB) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// Sync database before running query (fast incremental check)
		if err := syncDatabase(ctx, database); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("sync failed: %v", err)), nil
		}

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

		// Build query with filters
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

		queryArgs := []interface{}{args.Query}

		// Filter by current session only (compact cycles use case)
		if args.CurrentSessionID != "" {
			if args.ExcludeCurrent {
				query += " AND s.session_id != ?"
			} else {
				query += " AND s.session_id = ?"
			}
			queryArgs = append(queryArgs, args.CurrentSessionID)
		}

		// Filter by project
		if args.Project != "" {
			query += " AND s.project_path = ?"
			queryArgs = append(queryArgs, args.Project)
		}

		// Filter by date range
		if args.AfterDate != "" {
			query += " AND s.updated_at > ?"
			queryArgs = append(queryArgs, args.AfterDate)
		}
		if args.BeforeDate != "" {
			query += " AND s.updated_at < ?"
			queryArgs = append(queryArgs, args.BeforeDate)
		}

		query += " ORDER BY s.updated_at DESC, m.sequence ASC LIMIT 200"

		// Execute query
		rows, err := database.Query(query, queryArgs...)
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
		// Sync database before running query
		if err := syncDatabase(ctx, database); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("sync failed: %v", err)), nil
		}

		var args GetSessionDetailArgs
		argsBytes, _ := json.Marshal(request.Params.Arguments)
		if err := json.Unmarshal(argsBytes, &args); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid arguments: %v", err)), nil
		}

		// Get session info
		var session SessionDetail
		var sessionInternalID int64
		err := database.QueryRow(`
			SELECT
				id,
				session_id,
				COALESCE(summary, ''),
				project_path,
				created_at,
				updated_at,
				(SELECT COUNT(*) FROM messages WHERE session_id = sessions.id) as message_count
			FROM sessions
			WHERE session_id = ?
		`, args.SessionID).Scan(&sessionInternalID, &session.SessionID, &session.Summary, &session.Project,
			&session.CreatedAt, &session.UpdatedAt, &session.MessageCount)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("session not found: %v", err)), nil
		}

		// Get first message
		var firstMsg MessageDetail
		err = database.QueryRow(`
			SELECT type, COALESCE(text_content, ''), timestamp, sequence
			FROM messages
			WHERE session_id = ?
			ORDER BY sequence ASC
			LIMIT 1
		`, sessionInternalID).Scan(&firstMsg.Type, &firstMsg.Content, &firstMsg.Timestamp, &firstMsg.Sequence)
		if err == nil {
			session.FirstMessage = &firstMsg
		}

		// Get last message
		var lastMsg MessageDetail
		err = database.QueryRow(`
			SELECT type, COALESCE(text_content, ''), timestamp, sequence
			FROM messages
			WHERE session_id = ?
			ORDER BY sequence DESC
			LIMIT 1
		`, sessionInternalID).Scan(&lastMsg.Type, &lastMsg.Content, &lastMsg.Timestamp, &lastMsg.Sequence)
		if err == nil {
			session.LastMessage = &lastMsg
		}

		// If search query provided, get matching messages
		if args.SearchQuery != "" {
			rows, err := database.Query(`
				SELECT type, COALESCE(text_content, ''), timestamp, sequence
				FROM messages
				WHERE session_id = ?
				AND text_content LIKE '%' || ? || '%'
				ORDER BY sequence ASC
				LIMIT 5
			`, sessionInternalID, args.SearchQuery)
			if err == nil {
				defer rows.Close()
				session.MatchingMessages = []MessageDetail{}
				for rows.Next() {
					var msg MessageDetail
					if err := rows.Scan(&msg.Type, &msg.Content, &msg.Timestamp, &msg.Sequence); err != nil {
						continue
					}
					session.MatchingMessages = append(session.MatchingMessages, msg)
				}
			}
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
		// Sync database before running query
		if err := syncDatabase(ctx, database); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("sync failed: %v", err)), nil
		}

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
