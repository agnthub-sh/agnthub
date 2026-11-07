---
# Adapted from .claude/agents/issue-reader.md in localhost:8443/layouts
# Modified by agnthub: converted to the Gemini CLI agent format
# Copyright (c) 2026 agnthub e2e fixtures: layouts
# Copyright (c) 2026 Second Holder <holder@example.com>
# License: see LICENSE in localhost:8443/layouts
name: issue-reader
description: Reads GitHub issues through the GitHub MCP server.
tools:
  - read_file
  - list_directory
  - mcp_github_get_issue
  - mcp_github_list_issues
---
Read the issue and summarize it.
