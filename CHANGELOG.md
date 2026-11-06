# Changelog

## v0.5.0

### Breaking changes

- **The local MCP server is removed.** `agnthub mcp` (`npx -y agnthub-cli mcp`), its tools
  (`search_agents`, `get_agent`, `get_repository`, `install_command`, `install_agents`,
  `auth_status`) and the public `mcpserver` Go package are gone. AI agents use the hosted MCP
  server instead: `https://mcp.agnthub.sh` (setup for every harness:
  https://app.agnthub.sh/mcp). Its `install_agents` tool returns the exact
  `npx -y agnthub-cli@latest add …` command, which the agent runs with its shell tool after the
  user confirms. A configuration that still starts `agnthub mcp` fails with:
  `The local MCP server was removed in v0.5.0. Use the hosted server instead:
  https://mcp.agnthub.sh (setup: https://app.agnthub.sh/mcp).` Replace that entry with the
  hosted server's URL.
- `agnthub auth` stays: the API key raises the CLI's own registry API limits.
