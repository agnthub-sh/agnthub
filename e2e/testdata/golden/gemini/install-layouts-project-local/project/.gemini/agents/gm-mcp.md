---
name: gm-mcp
description: Gemini CLI agent with its own MCP server and one of its tools.
mcp_servers:
  github:
    command: /bin/true
tools:
  - read_file
  - mcp_github_get_issue
---
Read the issue.
