---
# Adapted from .opencode/agents/oc-native.md
# Modified by agnthub: converted to the Gemini CLI agent format
# Copyright (c) 2026 agnthub e2e fixtures: layouts
# Copyright (c) 2026 Second Holder <holder@example.com>
# License: see LICENSE
name: oc-native
description: OpenCode-native subagent that may only read.
tools:
  - read_file
  - list_directory
  - glob
  - grep_search
  - web_fetch
  - google_web_search
  - write_todos
  - activate_skill
  - mcp_*
---
Read and explain.
