---
# Adapted from .codex/agents/cx-solo.toml
# Modified by agnthub: converted to the Gemini CLI agent format
# Copyright (c) 2026 agnthub e2e fixtures: layouts
# Copyright (c) 2026 Second Holder <holder@example.com>
# License: see LICENSE
name: cx-solo
description: Codex agent that turns off delegation.
tools:
  - read_file
  - list_directory
  - glob
  - grep_search
  - replace
  - write_file
  - run_shell_command
  - web_fetch
  - google_web_search
  - write_todos
  - activate_skill
  - mcp_*
---
Do the work yourself without spawning other agents.
