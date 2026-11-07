---
# Adapted from .claude/agents/issue-reader.md in localhost:8443/layouts
# Modified by agnthub: converted to the omp agent format
# Copyright (c) 2026 agnthub e2e fixtures: layouts
# Copyright (c) 2026 Second Holder <holder@example.com>
# License: see LICENSE in localhost:8443/layouts
name: issue-reader
description: Reads GitHub issues through the GitHub MCP server.
tools:
  - read
---
Read the issue and summarize it.
