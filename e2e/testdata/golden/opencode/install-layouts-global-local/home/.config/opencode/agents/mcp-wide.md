---
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
