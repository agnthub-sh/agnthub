---
# Adapted from .claude/agents/scripted.md
# Modified by agnthub: converted to the Gemini CLI agent format
# Copyright (c) 2026 agnthub e2e fixtures: layouts
# Copyright (c) 2026 Second Holder <holder@example.com>
# License: see LICENSE
name: scripted
description: Coordinates tasks; its pre and post scripts are hook events Claude Code does not have.
tools:
  - read_file
  - list_directory
  - glob
  - grep_search
  - replace
  - write_file
  - run_shell_command
---
Coordinate the task.
