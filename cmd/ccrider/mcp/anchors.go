package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/neilberkman/ccrider/internal/core/config"
	"github.com/neilberkman/ccrider/internal/core/db"
)

type GenerateSessionAnchorArgs struct {
	Label     string `json:"label,omitempty"`
	SessionID string `json:"session_id,omitempty"`
}

type ListSessionAnchorsArgs struct {
	SessionID string `json:"session_id,omitempty"`
	Project   string `json:"project,omitempty"`
	Provider  string `json:"provider,omitempty"`
	Limit     int    `json:"limit,omitempty"`
	Offset    int    `json:"offset,omitempty"`
}

type SessionAnchorResult struct {
	Anchor            string           `json:"anchor"`
	Label             string           `json:"label,omitempty"`
	CreatedAt         string           `json:"created_at"`
	SessionID         string           `json:"session_id,omitempty"`
	Sessions          []SessionSummary `json:"sessions"`
	SessionCount      int              `json:"session_count"`
	SessionsTruncated bool             `json:"sessions_truncated,omitempty"`
}

type ListSessionAnchorsResponse struct {
	Anchors    []SessionAnchorResult `json:"anchors"`
	Total      int                   `json:"total"`
	NextOffset *int                  `json:"next_offset,omitempty"`
}

func registerSessionAnchorTools(s *server.MCPServer, database *db.DB) {
	s.AddTool(mcp.NewTool("generate_session_anchor",
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithOpenWorldHintAnnotation(false),
		mcp.WithIdempotentHintAnnotation(false),
		mcp.WithDescription("Generate and save a session anchor for context recovery or later retrieval. Supply a short descriptive label to get a readable tag with a random suffix; omit it for four random words. If you know the session_id, supply it to attach the anchor immediately. Otherwise say the returned phrase and wait for a user reply so it is written to the transcript. Use list_session_anchors to retrieve saved anchors and their matching sessions, including older conversations."),
		mcp.WithString("label", mcp.Description("Optional descriptive label, up to 120 characters. Choose a short topic from the conversation, e.g. 'add REST endpoint'.")),
		mcp.WithString("session_id", mcp.Description("Optional known session ID to attach immediately. Full IDs and unambiguous bare UUIDs are accepted. Omit if unknown; do not guess.")),
	), makeGenerateSessionAnchorHandler(database))

	s.AddTool(mcp.NewTool("list_session_anchors",
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithOpenWorldHintAnnotation(false),
		mcp.WithIdempotentHintAnnotation(true),
		mcp.WithDescription("List saved session anchors, newest first, with labels, creation times and matching sessions. Unfiltered results include anchors not yet found in a transcript (empty sessions). Lookup has no date cutoff. If a phrase occurs in multiple conversations, all matches are eligible; use the returned session_id with get_session_messages. Only anchors generated since persistence was added are registered. Follow next_offset to retrieve more anchors."),
		mcp.WithString("session_id", mcp.Description("Only anchors associated with this session. Full IDs and unambiguous bare UUIDs are accepted.")),
		mcp.WithString("project", mcp.Description("Filter associated sessions by project path substring. Unmatched anchors are omitted.")),
		mcp.WithString("provider", mcp.Description(providerFilterDescription())),
		mcp.WithNumber("limit", mcp.Description("Max anchors per page (default: 20, maximum: 100).")),
		mcp.WithNumber("offset", mcp.Description("Skip this many anchors (default: 0). Use next_offset from the previous response.")),
	), makeListSessionAnchorsHandler(database))
}

func makeGenerateSessionAnchorHandler(database *db.DB) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args GenerateSessionAnchorArgs
		argsBytes, err := json.Marshal(request.Params.Arguments)
		if err == nil {
			err = json.Unmarshal(argsBytes, &args)
		}
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid arguments: %v", err)), nil
		}
		if args.SessionID != "" {
			if err := syncDatabase(ctx, database); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("sync failed: %v", err)), nil
			}
		}
		anchor, err := database.GenerateSessionAnchor(ctx, db.GenerateSessionAnchorOptions{
			Label: args.Label, SessionID: args.SessionID,
		})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to generate anchor: %v", err)), nil
		}
		instruction := "Saved. SAY THIS EXACT PHRASE: '" + anchor.Anchor + "' and ask the user to reply so it can be written to the transcript. Then use search_sessions with anchor_phrase='" + anchor.Anchor + "' and query set to the topic you need. Use list_session_anchors later to find this anchor and its sessions."
		if anchor.SessionID != "" {
			instruction = "Saved for session '" + anchor.SessionID + "'. Use get_session_messages with this session_id to retrieve it, or list_session_anchors to find the bookmark later."
		}
		result := map[string]string{
			"anchor": anchor.Anchor, "instruction": instruction,
			"label": anchor.Label, "created_at": anchor.CreatedAt.Format(time.RFC3339Nano),
		}
		if anchor.SessionID != "" {
			result["session_id"] = anchor.SessionID
		}
		return mcp.NewToolResultText(string(mustMarshal(result))), nil
	}
}

func makeListSessionAnchorsHandler(database *db.DB) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args ListSessionAnchorsArgs
		// Normalize only pagination fields: a numeric label/provider/ID is text.
		if raw, ok := request.Params.Arguments.(map[string]interface{}); ok {
			for _, key := range []string{"limit", "offset"} {
				value := map[string]interface{}{key: raw[key]}
				coerceStringNumbers(value)
				if value[key] != nil {
					raw[key] = value[key]
				}
			}
		}
		argsBytes, err := json.Marshal(request.Params.Arguments)
		if err == nil {
			err = json.Unmarshal(argsBytes, &args)
		}
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid arguments: %v", err)), nil
		}
		if args.Offset < 0 || args.Limit < 0 {
			return mcp.NewToolResultError("offset and limit must not be negative"), nil
		}
		if args.Limit == 0 {
			args.Limit = 20
		}
		args.Limit = min(args.Limit, 100)
		if err := syncDatabase(ctx, database); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("sync failed: %v", err)), nil
		}
		anchors, err := database.ListSessionAnchors(ctx, db.SessionAnchorFilters{
			SessionID: args.SessionID, ProjectPath: args.Project, Provider: args.Provider,
		})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("list anchors failed: %v", err)), nil
		}
		cfg, _ := config.Load()
		result := formatAnchorPage(anchors, args.Offset, args.Limit, cfg)
		return mcp.NewToolResultText(string(mustMarshal(result))), nil
	}
}

func formatAnchorPage(anchors []db.SessionAnchor, offset, limit int, cfg *config.Config) ListSessionAnchorsResponse {
	result := ListSessionAnchorsResponse{Anchors: []SessionAnchorResult{}, Total: len(anchors)}
	offset = min(offset, len(anchors))
	end := offset + min(limit, len(anchors)-offset)
	for _, a := range anchors[offset:end] {
		entry := SessionAnchorResult{
			Anchor: a.Anchor, Label: a.Label, CreatedAt: a.CreatedAt.Format(time.RFC3339Nano),
			SessionID: a.SessionID, Sessions: []SessionSummary{}, SessionCount: len(a.Sessions),
		}
		for _, s := range a.Sessions {
			summary := toSessionSummary(s, cfg)
			if text := []rune(summary.Summary); len(text) > 500 {
				summary.Summary = string(text[:500]) + "…"
			}
			entry.Sessions = append(entry.Sessions, summary)
		}
		result.Anchors = append(result.Anchors, entry)
	}
	// Preserve at least one anchor per nonempty page so callers always advance.
	// Large session payloads can be fetched separately with get_session_messages.
	for {
		next := offset + len(result.Anchors)
		result.NextOffset = nil
		if next < len(anchors) {
			result.NextOffset = &next
		}
		if len(mustMarshal(result)) <= maxResponseBytes() {
			break
		}
		if len(result.Anchors) > 1 {
			result.Anchors = result.Anchors[:len(result.Anchors)-1]
			continue
		}
		entry := &result.Anchors[0]
		entry.SessionsTruncated = true
		if len(entry.Sessions) == 0 {
			break
		}
		entry.Sessions = entry.Sessions[:len(entry.Sessions)-1]
	}
	return result
}
