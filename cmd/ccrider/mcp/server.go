package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/neilberkman/ccrider/internal/core/db"
	"github.com/neilberkman/ccrider/internal/core/importer"
	"github.com/neilberkman/ccrider/internal/core/search"
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
	defer func() {
		if closeErr := database.Close(); closeErr != nil {
			log.Printf("Error closing database: %v", closeErr)
		}
	}()

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

		// Set defaults (interface concern - pagination)
		limit := args.Limit
		if limit == 0 {
			limit = 10
		}

		// Convert MCP args to core filters
		coreFilters := search.SearchFilters{
			Query:            args.Query,
			ProjectPath:      args.Project,
			CurrentSessionID: args.CurrentSessionID,
			ExcludeCurrent:   args.ExcludeCurrent,
			AfterDate:        args.AfterDate,
			BeforeDate:       args.BeforeDate,
		}

		// Call core search with filters (business logic in core)
		coreResults, err := search.SearchWithFilters(database, coreFilters)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("search failed: %v", err)), nil
		}

		// Convert core types to MCP types (interface concern - presentation)
		var results []SessionMatch
		for _, coreSession := range coreResults {
			result := SessionMatch{
				SessionID: coreSession.SessionID,
				Summary:   coreSession.SessionSummary,
				Project:   coreSession.ProjectPath,
				UpdatedAt: coreSession.UpdatedAt,
				Matches:   []MatchSnippet{},
			}

			// Limit to 3 matches per session for display (interface concern)
			matchLimit := 3
			if len(coreSession.Matches) > matchLimit {
				coreSession.Matches = coreSession.Matches[:matchLimit]
			}

			for _, match := range coreSession.Matches {
				result.Matches = append(result.Matches, MatchSnippet{
					MessageType: "message",
					Snippet:     match.MessageText,
					Sequence:    0,
				})
			}

			result.MatchCount = len(result.Matches)
			results = append(results, result)

			// Apply pagination limit (interface concern)
			if len(results) >= limit {
				break
			}
		}

		// Return results as JSON (interface concern - protocol)
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

		// Use core function to get full session detail
		coreDetail, err := database.GetSessionDetail(args.SessionID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("session not found: %v", err)), nil
		}

		// Convert to MCP format (interface concern - presentation)
		session := SessionDetail{
			SessionID:    coreDetail.SessionID,
			Summary:      coreDetail.Summary,
			Project:      coreDetail.ProjectPath,
			UpdatedAt:    coreDetail.UpdatedAt.Format("2006-01-02 15:04:05"),
			CreatedAt:    coreDetail.UpdatedAt.Format("2006-01-02 15:04:05"), // Use UpdatedAt as fallback
			MessageCount: coreDetail.MessageCount,
		}

		// Extract first and last messages (interface concern - presentation)
		if len(coreDetail.Messages) > 0 {
			first := coreDetail.Messages[0]
			session.FirstMessage = &MessageDetail{
				Type:      first.Type,
				Content:   first.Content,
				Timestamp: first.Timestamp.Format("2006-01-02 15:04:05"),
				Sequence:  0,
			}

			last := coreDetail.Messages[len(coreDetail.Messages)-1]
			session.LastMessage = &MessageDetail{
				Type:      last.Type,
				Content:   last.Content,
				Timestamp: last.Timestamp.Format("2006-01-02 15:04:05"),
				Sequence:  len(coreDetail.Messages) - 1,
			}
		}

		// If search query provided, filter matching messages (interface concern)
		if args.SearchQuery != "" {
			session.MatchingMessages = []MessageDetail{}
			queryLower := strings.ToLower(args.SearchQuery)
			for i, msg := range coreDetail.Messages {
				if strings.Contains(strings.ToLower(msg.Content), queryLower) {
					session.MatchingMessages = append(session.MatchingMessages, MessageDetail{
						Type:      msg.Type,
						Content:   msg.Content,
						Timestamp: msg.Timestamp.Format("2006-01-02 15:04:05"),
						Sequence:  i,
					})
					if len(session.MatchingMessages) >= 5 {
						break
					}
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

		// Use core function to get sessions
		coreSessions, err := database.ListSessions(args.Project)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("query failed: %v", err)), nil
		}

		// Apply limit (interface concern - pagination)
		if len(coreSessions) > limit {
			coreSessions = coreSessions[:limit]
		}

		// Convert core types to MCP types (interface concern - presentation)
		var sessions []SessionSummary
		for _, cs := range coreSessions {
			sessions = append(sessions, SessionSummary{
				SessionID:    cs.SessionID,
				Summary:      cs.Summary,
				Project:      cs.ProjectPath,
				UpdatedAt:    cs.UpdatedAt.Format("2006-01-02 15:04:05"),
				MessageCount: cs.MessageCount,
			})
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
