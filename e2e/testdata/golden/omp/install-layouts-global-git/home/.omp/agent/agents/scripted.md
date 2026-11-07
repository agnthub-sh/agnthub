---
# Adapted from .claude/agents/scripted.md in localhost:8443/layouts
# Modified by agnthub: converted to the omp agent format
# Copyright (c) 2026 agnthub e2e fixtures: layouts
# Copyright (c) 2026 Second Holder <holder@example.com>
# License: see LICENSE in localhost:8443/layouts
name: scripted
description: Coordinates tasks; its pre and post scripts are hook events Claude Code does not have.
tools:
  - read
  - grep
  - glob
  - find
  - edit
  - write
  - bash
---
Coordinate the task.
