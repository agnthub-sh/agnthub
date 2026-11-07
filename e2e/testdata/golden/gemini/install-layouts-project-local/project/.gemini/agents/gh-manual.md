---
# Adapted from .github/agents/gh-manual.agent.md
# Modified by agnthub: converted to the Gemini CLI agent format
# Copyright (c) 2026 agnthub e2e fixtures: layouts
# Copyright (c) 2026 Second Holder <holder@example.com>
# License: see LICENSE
name: gh-manual
description: Use only when the user explicitly asks for this agent. Prepares a release when the user asks for it.
tools:
  - read_file
  - list_directory
  - glob
  - grep_search
  - replace
  - write_file
  - run_shell_command
  - activate_skill
---
Prepare the release the user asked for.
