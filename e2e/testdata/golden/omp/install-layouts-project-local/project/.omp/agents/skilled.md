---
# Adapted from .claude/agents/skilled.md
# Modified by agnthub: converted to the omp agent format
# Copyright (c) 2026 agnthub e2e fixtures: layouts
# Copyright (c) 2026 Second Holder <holder@example.com>
# License: see LICENSE
name: skilled
description: Reviews releases with preloaded skills.
tools:
  - read
  - grep
  - glob
  - find
  - edit
  - write
  - bash
autoload-skills:
  - bug-report
  - release-checklist
---
Review the release with your skills.
