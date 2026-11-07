---
# Adapted from .claude/agents/team/fixer.md
# Modified by agnthub: converted to the omp agent format
# Copyright (c) 2026 agnthub e2e fixtures: layouts
# Copyright (c) 2026 Second Holder <holder@example.com>
# License: see LICENSE
name: fixer
description: Fixes failing tests with minimal edits.
tools:
  - read
  - grep
  - glob
  - find
  - edit
  - write
  - bash
---
Make the failing test pass with the smallest correct change.
