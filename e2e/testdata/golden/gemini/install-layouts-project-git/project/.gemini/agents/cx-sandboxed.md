---
# Adapted from .codex/agents/cx-sandboxed.toml in localhost:8443/layouts
# Modified by agnthub: converted to the Gemini CLI agent format
# Copyright (c) 2026 agnthub e2e fixtures: layouts
# Copyright (c) 2026 Second Holder <holder@example.com>
# License: see LICENSE in localhost:8443/layouts
name: cx-sandboxed
description: Codex agent that asks for a read-only sandbox.
tools:
  - read_file
  - list_directory
  - glob
  - grep_search
  - run_shell_command
  - web_fetch
  - google_web_search
  - write_todos
  - activate_skill
  - mcp_*
---
Inspect the code and report findings without changing files.
