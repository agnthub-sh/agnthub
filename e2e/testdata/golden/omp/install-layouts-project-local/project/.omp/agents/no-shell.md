---
# Adapted from .claude/agents/no-shell.md
# Modified by agnthub: converted to the omp agent format
# Copyright (c) 2026 agnthub e2e fixtures: layouts
# Copyright (c) 2026 Second Holder <holder@example.com>
# License: see LICENSE
name: no-shell
description: May use every tool except the shell.
tools:
  - read
  - grep
  - glob
  - find
  - edit
  - write
  - web_search
  - task
  - todo
---
Work without running commands.
