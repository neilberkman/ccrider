package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/neilberkman/ccrider/cmd/ccrider/mcp"
)

var mcpCmd = &cobra.Command{
	Use:   "serve-mcp",
	Short: "Start MCP server for Claude Code integration",
	Long: `Start an MCP (Model Context Protocol) server that allows Claude Code
to search and retrieve information from your session history.

Configure in Claude Desktop's config file (~/.config/claude/config.json):
  {
    "mcpServers": {
      "ccrider": {
        "command": "ccrider",
        "args": ["serve-mcp"]
      }
    }
  }
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
