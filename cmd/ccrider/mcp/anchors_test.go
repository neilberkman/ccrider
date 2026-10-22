package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/neilberkman/ccrider/internal/core/db"
)

func TestSessionAnchorMCPWorkflow(t *testing.T) {
	// Keep the automatic transcript sync inside an empty test environment.
	t.Setenv("HOME", t.TempDir())
	database, err := db.New(filepath.Join(t.TempDir(), "anchors.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	s := server.NewMCPServer("anchor-test", "1.0.0")
	registerSessionAnchorTools(s, database)
	c, err := client.NewInProcessClient(s)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	if err := c.Start(ctx); err != nil {
		t.Fatal(err)
	}
	init := mcp.InitializeRequest{}
	init.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	init.Params.ClientInfo = mcp.Implementation{Name: "test", Version: "1.0.0"}
	if _, err := c.Initialize(ctx, init); err != nil {
		t.Fatal(err)
	}
	tools, err := c.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 2 {
		t.Fatalf("registered tools = %+v", tools.Tools)
	}
	for _, tool := range tools.Tools {
		wantReadOnly := tool.Name == "list_session_anchors"
		if tool.Annotations.ReadOnlyHint == nil || *tool.Annotations.ReadOnlyHint != wantReadOnly {
			t.Errorf("incorrect mutation annotation for %s", tool.Name)
		}
	}
	call := func(name string, args any, target any) {
		t.Helper()
		result, err := c.CallTool(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{Name: name, Arguments: args}})
		if err != nil {
			t.Fatal(err)
		}
		if result.IsError || len(result.Content) != 1 {
			t.Fatalf("%s returned %+v", name, result)
		}
		content, ok := result.Content[0].(mcp.TextContent)
		if !ok {
			t.Fatalf("unexpected content: %+v", result.Content[0])
		}
		if err := json.Unmarshal([]byte(content.Text), target); err != nil {
			t.Fatalf("decode %s: %v", content.Text, err)
		}
	}
	var page ListSessionAnchorsResponse
	call("list_session_anchors", nil, &page)
	if page.Anchors == nil || page.Total != 0 || page.NextOffset != nil {
		t.Fatalf("empty page = %+v", page)
	}
	var plain, named map[string]string
	call("generate_session_anchor", nil, &plain)
	call("generate_session_anchor", map[string]any{"label": "Add REST endpoint"}, &named)
	if len(strings.Split(plain["anchor"], "-")) != 4 || plain["instruction"] == "" || !strings.HasPrefix(named["anchor"], "add-rest-endpoint-") {
		t.Fatalf("generated results: %v, %v", plain, named)
	}
	call("list_session_anchors", map[string]any{"limit": "1"}, &page)
	if page.Total != 2 || len(page.Anchors) != 1 || page.Anchors[0].Anchor != named["anchor"] || page.NextOffset == nil || *page.NextOffset != 1 || len(page.Anchors[0].Sessions) != 0 {
		t.Fatalf("pending first page = %+v", page)
	}
	page = ListSessionAnchorsResponse{}
	call("list_session_anchors", map[string]any{"limit": 1, "offset": 1}, &page)
	if len(page.Anchors) != 1 || page.Anchors[0].Anchor != plain["anchor"] || page.NextOffset != nil {
		t.Fatalf("second page = %+v", page)
	}
	// A later import makes the pending bookmark discoverable even years later.
	if _, err := database.Exec(`INSERT INTO sessions (session_id, project_path, provider, summary, created_at, updated_at)
		VALUES ('old-session', '/repo', 'claude', 'REST endpoint', '2020-01-01', '2020-01-01')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO messages (uuid, session_id, type, text_content, timestamp)
		VALUES ('old-message', (SELECT id FROM sessions WHERE session_id = 'old-session'), 'assistant', ?, '2020-01-01')`, named["anchor"]); err != nil {
		t.Fatal(err)
	}
	call("list_session_anchors", map[string]any{"project": "repo", "provider": "claude"}, &page)
	if page.Total != 1 || len(page.Anchors) != 1 || len(page.Anchors[0].Sessions) != 1 || page.Anchors[0].Sessions[0].SessionID != "old-session" || !strings.Contains(page.Anchors[0].Sessions[0].ResumeCommand, "cd '/repo'") {
		t.Fatalf("resolved page = %+v", page)
	}
	var bound map[string]string
	call("generate_session_anchor", map[string]any{"label": "123", "session_id": "old-session"}, &bound)
	if bound["session_id"] != "old-session" || !strings.HasPrefix(bound["anchor"], "123-") {
		t.Fatalf("explicit binding = %v", bound)
	}
	call("list_session_anchors", map[string]any{"session_id": "old-session"}, &page)
	if page.Total != 2 {
		t.Fatalf("bound and discovered bookmarks = %+v", page)
	}
	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"generate_session_anchor", map[string]any{"session_id": "missing"}},
		{"generate_session_anchor", map[string]any{"label": "!!!"}},
		{"generate_session_anchor", map[string]any{"label": 123}},
		{"list_session_anchors", map[string]any{"offset": -1}},
		{"list_session_anchors", map[string]any{"limit": -1}},
		{"list_session_anchors", map[string]any{"limit": 1.5}},
	} {
		result, err := c.CallTool(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{Name: tc.tool, Arguments: tc.args}})
		if err != nil || !result.IsError {
			t.Fatalf("expected tool error for %s %v: %+v, %v", tc.tool, tc.args, result, err)
		}
	}
}

func TestAnchorPaginationRespectsResponseBudgetAndAdvances(t *testing.T) {
	anchors := []db.SessionAnchor{}
	sessions := make([]db.Session, 100)
	for i := range sessions {
		sessions[i] = db.Session{SessionID: fmt.Sprintf("huge-%d", i), Summary: strings.Repeat("x", maxResponseBytes())}
	}
	for i := 0; i < 4; i++ {
		anchors = append(anchors, db.SessionAnchor{
			Anchor: fmt.Sprintf("tag-%d", i), Label: "Large conversation",
			Sessions: sessions,
		})
	}
	for offset := 0; offset < len(anchors); {
		page := formatAnchorPage(anchors, offset, 100, nil)
		if len(mustMarshal(page)) > maxResponseBytes() || len(page.Anchors) == 0 {
			t.Fatalf("page exceeds budget or cannot advance: %+v", page)
		}
		if page.Anchors[0].Anchor != anchors[offset].Anchor || !page.Anchors[0].SessionsTruncated || page.Anchors[0].SessionCount != len(sessions) || len(page.Anchors[0].Sessions) == 0 {
			t.Fatalf("missing anchor or truncation metadata: %+v", page)
		}
		offset += len(page.Anchors)
		if offset < len(anchors) && (page.NextOffset == nil || *page.NextOffset != offset) {
			t.Fatalf("next offset does not advance: %+v", page)
		}
	}
	page := formatAnchorPage(anchors, int(^uint(0)>>1), 100, nil)
	if len(page.Anchors) != 0 || page.NextOffset != nil || page.Total != 4 {
		t.Fatalf("out-of-range page = %+v", page)
	}
}
