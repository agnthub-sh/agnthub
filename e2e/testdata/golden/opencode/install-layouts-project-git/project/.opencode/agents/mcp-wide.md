---
# Adapted from .claude/agents/mcp-wide.md in localhost:8443/layouts
# Modified by agnthub: converted to the OpenCode agent format
# Copyright (c) 2026 agnthub e2e fixtures: layouts
# Copyright (c) 2026 Second Holder <holder@example.com>
# License: see LICENSE in localhost:8443/layouts
description: Uses every tool of the GitHub MCP server.
mode: subagent
permission:
  '*': deny
  execute: allow
  github_*: allow
  read:
    '*': allow
    '*.env': ask
    '*.env.*': ask
    '*.env.example': allow
---
Answer questions about the repository on GitHub.
