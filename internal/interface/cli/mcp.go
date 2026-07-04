package cli

import (
	"fmt"

	"github.com/neilberkman/ccrider/cmd/ccrider/mcp"
	"github.com/spf13/cobra"
)

var mcpCmd = &cobra.Command{
	Use:   "serve-mcp",
	Short: "Start MCP server for coding agents",
	Long: `Start an MCP (Model Context Protocol) server that allows coding agents
to search and retrieve information from your session history.

For Claude Code:
  claude mcp add --scope user ccrider $(which ccrider) serve-mcp

For Claude Desktop (~/Library/Application Support/Claude/claude_desktop_config.json):
  {
    "mcpServers": {
      "ccrider": {
        "command": "ccrider",
        "args": ["serve-mcp"]
      }
    }
  }

For other MCP clients, configure command "ccrider" with args ["serve-mcp"].
`,
	RunE: runMCP,
}

func init() {
	rootCmd.AddCommand(mcpCmd)
}

func runMCP(cmd *cobra.Command, args []string) error {
	if err := mcp.StartServer(dbPath); err != nil {
		return fmt.Errorf("MCP server failed: %w", err)
	}
	return nil
}
