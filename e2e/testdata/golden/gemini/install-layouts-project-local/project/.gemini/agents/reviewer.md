---
# Adapted from .claude/agents/reviewer.md
# Modified by agnthub: converted to the Gemini CLI agent format
# Copyright (c) 2026 agnthub e2e fixtures: layouts
# Copyright (c) 2026 Second Holder <holder@example.com>
# License: see LICENSE
name: reviewer
description: Reviews diffs for correctness. Use after every change.
tools:
  - read_file
  - list_directory
  - glob
  - grep_search
  - run_shell_command
---
Review the current diff. Report bugs with file and line.
