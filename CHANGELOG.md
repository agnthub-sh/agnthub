# Changelog

## v0.6.0

### New

- **`agnthub mcp install`** sets up agnthub's hosted MCP server (`https://mcp.agnthub.sh`) in
  every harness on this machine: Claude Code, OpenCode, Codex, GitHub Copilot CLI, Cursor,
  Gemini CLI and omp, in every omp profile. In a terminal it lists the detected harnesses and
  profiles (the ones without agnthub preselected, installed ones marked, older agnthub setups
  offered as updates); otherwise choose with `--harness claude,codex` / `--harness all` and
  `--profile <omp profile>`, with `--yes` and `--json`. It writes an `agnthub` Streamable HTTP
  entry at user scope (`--scope project` for the project's file), with Claude Code's, Copilot
  CLI's and Gemini CLI's own `mcp add` when they are on `PATH` and otherwise by editing the
  file: comments and every other entry are kept, the previous file is backed up under
  `~/.local/state/agnthub/mcp-backups`, the new one is written atomically with the old mode,
  and a file that does not parse is never changed. Older agnthub entries (the
  `https://api.agnthub.sh/mcp` and `https://mcp.agnthub.sh/mcp` URLs, and the local server
  `npx agnthub-cli mcp` removed in v0.5.0) are replaced; an `agnthub` entry agnthub did not
  write is kept unless confirmed or `--force`. It then prints how to sign in with GitHub in
  each harness; `--api-key` writes your agnthub API key as an `Authorization: Bearer` header
  instead (with a warning that it is stored in plain text). Exit status 0, 1 (error) or 2 (a
  foreign entry was skipped).
- **`agnthub mcp uninstall`** finds agnthub's entries, in any of those forms, in every
  harness and omp profile (and the project with `--scope project`), lists them all selected
  (`--yes` removes every one) and removes only those; other servers are never touched. With
  none it prints `No agnthub MCP installs detected.`
- Bare `agnthub mcp`, which configurations from before v0.5.0 still start as a stdio server,
  keeps printing the removal notice and exiting 1 without a terminal, now followed by
  ``Run `npx agnthub-cli mcp install` to configure the hosted server.``; in a terminal it shows
  the help of the `mcp` commands, which `agnthub --help` now lists.
- The end-to-end suite proves both commands in each harness image with the harness's own
  listing (`claude mcp list`, `codex mcp list --json`, `gemini mcp list`, `opencode mcp
  list`, `copilot mcp list`, `cursor-agent mcp list`, omp's MCP loader), an omp named profile
  included (see `e2e/COVERAGE.md`).

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
