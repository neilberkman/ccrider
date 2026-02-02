package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/neilberkman/ccrider/internal/core/db"
	"github.com/sethvargo/go-diceware/diceware"
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
	AnchorPhrase     string `json:"anchor_phrase,omitempty" jsonschema:"description=Exact phrase that must exist in the session (used to find current session - pick a unique phrase you just said/saw)"`
	ExactMatch       bool   `json:"exact_match,omitempty" jsonschema:"description=If true, query is treated as an exact phrase (auto-quoted for you)"`
}

// ListRecentSessionsArgs defines arguments for the list_recent_sessions tool
type ListRecentSessionsArgs struct {
	Limit   int    `json:"limit,omitempty" jsonschema:"description=Max sessions to return (default: 20)"`
	Project string `json:"project,omitempty" jsonschema:"description=Filter by project path"`
}

// GetSessionMessagesArgs defines arguments for the get_session_messages tool
type GetSessionMessagesArgs struct {
	SessionID      string `json:"session_id" jsonschema:"description=Session UUID to retrieve messages from,required"`
	LastN          int    `json:"last_n,omitempty" jsonschema:"description=Return last N messages (tail mode)"`
	AroundSequence int    `json:"around_sequence,omitempty" jsonschema:"description=Return messages around this sequence number (from search results)"`
	ContextSize    int    `json:"context_size,omitempty" jsonschema:"description=Messages before/after around_sequence (default: 10)"`
}

// MaxResponseBytes is the hard limit on response size to prevent context overflow
// ~50KB is roughly 12-15k tokens, a reasonable chunk that won't blow up context
const MaxResponseBytes = 50000

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

// SessionMessagesResponse represents the response from get_session_messages
type SessionMessagesResponse struct {
	SessionID        string          `json:"session_id"`
	TotalCount       int             `json:"total_count"`
	ReturnedFrom     int             `json:"returned_from"` // First sequence in response
	ReturnedTo       int             `json:"returned_to"`   // Last sequence in response
	Messages         []MessageDetail `json:"messages"`
	Truncated        bool            `json:"truncated,omitempty"`         // True if response was truncated
	TruncatedMessage string          `json:"truncated_message,omitempty"` // Explanation when truncated
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
		mcp.WithDescription("Search Claude Code sessions for a query string across all message content. Can search current session only, exclude current session, or search all sessions. Supports date and project filtering.\n\nTO FIND EARLIER CONTEXT IN YOUR CURRENT SESSION (disappeared due to context compaction): Use anchor_phrase with a unique phrase you just said or saw - this identifies your session. Then query searches only within that session. The database syncs before every search so even recent messages are searchable."),
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
		mcp.WithString("anchor_phrase",
			mcp.Description("Exact phrase that must exist in the session. Use this to find your current session: pick a unique phrase you just said or saw, and the search will only return sessions containing that phrase. Combined with recency, this reliably identifies the current conversation.")),
		mcp.WithBoolean("exact_match",
			mcp.Description("If true, treats the query as an exact phrase match (auto-quoted). Use this instead of trying to add quotes yourself.")),
	)
	s.AddTool(searchTool, makeSearchSessionsHandler(database))

	// Register list_recent_sessions tool
	listTool := mcp.NewTool("list_recent_sessions",
		mcp.WithDescription("Get recent Claude Code sessions, optionally filtered by project"),
		mcp.WithNumber("limit",
			mcp.Description("Max sessions to return (default: 20)")),
		mcp.WithString("project",
			mcp.Description("Filter by project path")),
	)
	s.AddTool(listTool, makeListRecentSessionsHandler(database))

	// Register get_session_messages tool
	messagesTool := mcp.NewTool("get_session_messages",
		mcp.WithDescription("Get messages from a Claude Code session. Use last_n for tail (e.g., 'where were we'), around_sequence for context around a search match, or neither for full transcript."),
		mcp.WithString("session_id",
			mcp.Required(),
			mcp.Description("Session UUID to retrieve messages from")),
		mcp.WithNumber("last_n",
			mcp.Description("Return last N messages (tail mode, for 'where were we' or 'refresh memory')")),
		mcp.WithNumber("around_sequence",
			mcp.Description("Return messages around this sequence number (use with search results that include sequence)")),
		mcp.WithNumber("context_size",
			mcp.Description("Messages before/after around_sequence (default: 10)")),
	)
	s.AddTool(messagesTool, makeGetSessionMessagesHandler(database))

	// Register generate_session_anchor tool
	anchorTool := mcp.NewTool("generate_session_anchor",
		mcp.WithDescription("USE THIS when the user asks about something from earlier in THIS conversation that you can't see (context compaction removed it). Generates a unique phrase you say aloud to 'tag' your session, then search for it. Two-step: 1) Call this, say the phrase, ask user to reply 'go', 2) After they reply, search with anchor_phrase to find your session's earlier context."),
	)
	s.AddTool(anchorTool, makeGenerateSessionAnchorHandler())

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
	if _, err := imp.ImportDirectory(sourcePath, nil, false, true); err != nil {
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

		// Handle exact_match: wrap query in quotes for phrase search
		query := args.Query
		if args.ExactMatch && query != "" && !strings.HasPrefix(query, "\"") {
			query = "\"" + query + "\""
		}

		// Handle anchor_phrase: find sessions containing anchor, then filter
		// Uses retry logic because Claude Code may not have flushed to disk yet
		var anchorSessionIDs map[string]bool
		if args.AnchorPhrase != "" {
			// Search for anchor phrase (always exact match)
			anchorQuery := "\"" + args.AnchorPhrase + "\""

			// Default to last hour if no date filters - most likely to hit current session
			afterDate := args.AfterDate
			if afterDate == "" && args.BeforeDate == "" {
				afterDate = time.Now().Add(-1 * time.Hour).Format(time.RFC3339)
			}

			anchorFilters := search.SearchFilters{
				Query:       anchorQuery,
				ProjectPath: args.Project,
				AfterDate:   afterDate,
				BeforeDate:  args.BeforeDate,
			}

			// Retry up to 5 times with delays - Claude Code may not have flushed yet
			var anchorResults []search.SessionSearchResult
			var lastErr error
			for attempt := 0; attempt < 5; attempt++ {
				if attempt > 0 {
					// Wait before retry, re-sync to pick up any new writes
					time.Sleep(1 * time.Second)
					if err := syncDatabase(ctx, database); err != nil {
						lastErr = err
						continue
					}
				}
				anchorResults, lastErr = search.SearchWithFilters(database, anchorFilters)
				if lastErr != nil {
					continue
				}
				if len(anchorResults) > 0 {
					break // Found it!
				}
			}

			if lastErr != nil {
				return mcp.NewToolResultError(fmt.Sprintf("anchor search failed: %v", lastErr)), nil
			}
			if len(anchorResults) == 0 {
				// No sessions contain anchor phrase after retries
				resultJSON, _ := json.Marshal(map[string]interface{}{
					"sessions": []SessionMatch{},
					"note":     "No sessions found containing anchor phrase after 5 attempts (~5s): " + args.AnchorPhrase + ". The phrase may not have been written to disk yet.",
				})
				return mcp.NewToolResultText(string(resultJSON)), nil
			}
			// Build set of session IDs that contain anchor
			anchorSessionIDs = make(map[string]bool)
			for _, s := range anchorResults {
				anchorSessionIDs[s.SessionID] = true
			}
		}

		// Convert MCP args to core filters
		coreFilters := search.SearchFilters{
			Query:            query,
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
			// If anchor_phrase was used, filter to only sessions that contained it
			if anchorSessionIDs != nil && !anchorSessionIDs[coreSession.SessionID] {
				continue
			}

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
					Sequence:    match.Sequence,
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

func makeGetSessionMessagesHandler(database *db.DB) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// Sync database before running query
		if err := syncDatabase(ctx, database); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("sync failed: %v", err)), nil
		}

		var args GetSessionMessagesArgs
		argsBytes, _ := json.Marshal(request.Params.Arguments)
		if err := json.Unmarshal(argsBytes, &args); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid arguments: %v", err)), nil
		}

		// Convert to core options
		opts := db.GetSessionMessagesOptions{
			LastN:          args.LastN,
			AroundSequence: args.AroundSequence,
			ContextSize:    args.ContextSize,
		}

		// Call core function
		messages, totalCount, err := database.GetSessionMessages(args.SessionID, opts)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to get messages: %v", err)), nil
		}

		// Convert to MCP format and calculate total size
		var mcpMessages []MessageDetail
		totalBytes := 0
		for _, msg := range messages {
			mcpMessages = append(mcpMessages, MessageDetail{
				Type:      msg.Type,
				Content:   msg.Content,
				Timestamp: msg.Timestamp.Format("2006-01-02 15:04:05"),
				Sequence:  msg.Sequence,
			})
			totalBytes += len(msg.Content)
		}

		// Truncate evenly from beginning/end if over byte limit
		truncated := false
		originalCount := len(mcpMessages)
		for totalBytes > MaxResponseBytes && len(mcpMessages) > 2 {
			truncated = true
			// Remove from both ends evenly
			frontSize := len(mcpMessages[0].Content)
			backSize := len(mcpMessages[len(mcpMessages)-1].Content)

			if frontSize >= backSize {
				totalBytes -= frontSize
				mcpMessages = mcpMessages[1:]
			} else {
				totalBytes -= backSize
				mcpMessages = mcpMessages[:len(mcpMessages)-1]
			}
		}

		var returnedFrom, returnedTo int
		if len(mcpMessages) > 0 {
			returnedFrom = mcpMessages[0].Sequence
			returnedTo = mcpMessages[len(mcpMessages)-1].Sequence
		}

		response := SessionMessagesResponse{
			SessionID:    args.SessionID,
			TotalCount:   totalCount,
			ReturnedFrom: returnedFrom,
			ReturnedTo:   returnedTo,
			Messages:     mcpMessages,
		}

		// Add truncation warning if applicable
		if truncated {
			response.Truncated = true
			response.TruncatedMessage = fmt.Sprintf("Response truncated to ~%dKB (%d of %d messages). Use last_n or around_sequence for targeted retrieval.", totalBytes/1000, len(mcpMessages), originalCount)
		}

		resultJSON, err := json.Marshal(response)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal results: %v", err)), nil
		}

		return mcp.NewToolResultText(string(resultJSON)), nil
	}
}

func makeGenerateSessionAnchorHandler() func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// Generate a memorable diceware phrase (4 words)
		words, err := diceware.Generate(4)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to generate anchor: %v", err)), nil
		}

		anchor := strings.Join(words, "-")

		result := map[string]string{
			"anchor":      anchor,
			"instruction": "SAY THIS EXACT PHRASE: '" + anchor + "' - then ask the user to reply 'go' (this writes your response to disk so it gets indexed). After they reply, call search_sessions with anchor_phrase='" + anchor + "' to find earlier context.",
		}

		resultJSON, _ := json.Marshal(result)
		return mcp.NewToolResultText(string(resultJSON)), nil
	}
}
