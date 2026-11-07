---
# Adapted from .claude/agents/defaulted.md
# Modified by agnthub: converted to the omp agent format
# Copyright (c) 2026 agnthub e2e fixtures: layouts
# Copyright (c) 2026 Second Holder <holder@example.com>
# License: see LICENSE
name: defaulted
description: Sets Claude Code fields to their defaults or leaves them empty.
tools:
  - read
  - grep
  - glob
  - find
  - edit
  - write
  - bash
---
Improve the developer experience of the project.
