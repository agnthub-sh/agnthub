---
# Adapted from .claude/agents/annotated.md in localhost:8443/layouts
# Modified by agnthub: converted to the omp agent format
# Copyright (c) 2026 agnthub e2e fixtures: layouts
# Copyright (c) 2026 Second Holder <holder@example.com>
# License: see LICENSE in localhost:8443/layouts
name: annotated
description: Reviews code; its catalog keys mean nothing to Claude Code.
tools:
  - read
  - grep
  - glob
  - find
  - edit
  - write
  - bash
---
Review the change.
