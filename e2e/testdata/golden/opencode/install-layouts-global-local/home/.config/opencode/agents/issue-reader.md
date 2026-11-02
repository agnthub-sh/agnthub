---
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
