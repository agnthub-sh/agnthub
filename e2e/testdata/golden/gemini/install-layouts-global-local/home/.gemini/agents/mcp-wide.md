---
# Adapted from .claude/agents/mcp-wide.md
# Modified by agnthub: converted to the Gemini CLI agent format
# Copyright (c) 2026 agnthub e2e fixtures: layouts
# Copyright (c) 2026 Second Holder <holder@example.com>
# License: see LICENSE
name: mcp-wide
description: Uses every tool of the GitHub MCP server.
tools:
  - read_file
  - list_directory
  - mcp_github_*
---
Answer questions about the repository on GitHub.
