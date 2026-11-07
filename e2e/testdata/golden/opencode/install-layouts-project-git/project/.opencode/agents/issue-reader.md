---
# Adapted from .claude/agents/issue-reader.md in localhost:8443/layouts
# Modified by agnthub: converted to the OpenCode agent format
# Copyright (c) 2026 agnthub e2e fixtures: layouts
# Copyright (c) 2026 Second Holder <holder@example.com>
# License: see LICENSE in localhost:8443/layouts
description: Reads GitHub issues through the GitHub MCP server.
mode: subagent
permission:
  '*': deny
  execute: allow
  github_get_issue: allow
  github_list_issues: allow
  read:
    '*': allow
    '*.env': ask
    '*.env.*': ask
    '*.env.example': allow
---
Read the issue and summarize it.
