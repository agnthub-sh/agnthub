---
# Adapted from .claude/agents/allowlisted.md
# Modified by agnthub: converted to the omp agent format
# Copyright (c) 2026 agnthub e2e fixtures: layouts
# Copyright (c) 2026 Second Holder <holder@example.com>
# License: see LICENSE
name: allowlisted
description: Reads code under an allowed-tools key Claude Code ignores.
tools:
  - read
  - grep
  - glob
  - find
---
Read the code.
