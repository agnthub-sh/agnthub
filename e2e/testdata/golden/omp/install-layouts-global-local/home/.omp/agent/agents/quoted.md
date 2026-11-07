---
# Adapted from .claude/agents/quoted.md
# Modified by agnthub: converted to the omp agent format
# Copyright (c) 2026 agnthub e2e fixtures: layouts
# Copyright (c) 2026 Second Holder <holder@example.com>
# License: see LICENSE
name: quoted
description: 'Reviews: the "risky" parts of a change, and it''s careful.'
tools:
  - read
  - grep
  - glob
  - find
  - edit
  - write
  - bash
---
Review the risky parts.
