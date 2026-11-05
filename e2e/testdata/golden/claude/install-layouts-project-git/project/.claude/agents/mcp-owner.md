---
name: mcp-owner
description: Reads issues through its own GitHub MCP server.
tools: Read, mcp__github__get_issue
mcpServers:
  - github:
      command: /bin/true
      args: ["--stdio"]
---
Read the issue, then the code it mentions.
