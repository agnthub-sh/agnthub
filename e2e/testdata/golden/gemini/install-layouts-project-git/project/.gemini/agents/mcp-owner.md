---
name: mcp-owner
description: Reads issues through its own GitHub MCP server.
tools:
  - read_file
  - list_directory
  - mcp_github_get_issue
mcp_servers:
  github:
    args:
      - --stdio
    command: /bin/true
---
Read the issue, then the code it mentions.
