package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// mcpRemoved tells MCP client configurations that still start `agnthub mcp` where the server
// went. It is the whole command: the local MCP server no longer exists.
const mcpRemoved = "The local MCP server was removed in v0.5.0. Use the hosted server instead: " +
	"https://mcp.agnthub.sh (setup: https://app.agnthub.sh/mcp)."

// mcpCommand is hidden and only explains the removal, whatever flags an old configuration
// passes, and exits non-zero so the MCP client reports the server as failed.
func (a *App) mcpCommand() *cobra.Command {
	return &cobra.Command{
		Use:                "mcp",
		Hidden:             true,
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE: func(*cobra.Command, []string) error {
			fmt.Fprintln(a.Stderr, mcpRemoved)
			return &ExitError{Code: 1}
		},
	}
}
