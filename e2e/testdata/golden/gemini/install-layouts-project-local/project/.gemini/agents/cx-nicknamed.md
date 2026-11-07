---
# Adapted from .codex/agents/cx-nicknamed.toml
# Modified by agnthub: converted to the Gemini CLI agent format
# Copyright (c) 2026 agnthub e2e fixtures: layouts
# Copyright (c) 2026 Second Holder <holder@example.com>
# License: see LICENSE
name: cx-nicknamed
description: Codex agent with display nicknames.
tools:
  - read_file
  - list_directory
  - glob
  - grep_search
  - replace
  - write_file
  - web_fetch
  - google_web_search
  - write_todos
  - activate_skill
  - mcp_*
---
Explain the code without running commands.
