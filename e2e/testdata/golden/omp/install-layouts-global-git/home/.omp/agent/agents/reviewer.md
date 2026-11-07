---
# Adapted from .claude/agents/reviewer.md in localhost:8443/layouts
# Modified by agnthub: converted to the omp agent format
# Copyright (c) 2026 agnthub e2e fixtures: layouts
# Copyright (c) 2026 Second Holder <holder@example.com>
# License: see LICENSE in localhost:8443/layouts
name: reviewer
description: Reviews diffs for correctness. Use after every change.
tools:
  - read
  - grep
  - glob
  - find
  - bash
---
Review the current diff. Report bugs with file and line.
