---
# Adapted from .github/agents/gh-plain.md in localhost:8443/layouts
# Modified by agnthub: converted to the omp agent format
# Copyright (c) 2026 agnthub e2e fixtures: layouts
# Copyright (c) 2026 Second Holder <holder@example.com>
# License: see LICENSE in localhost:8443/layouts
name: gh-plain
description: Copilot CLI agent in a plain .md file that reads and searches.
tools:
  - read
  - grep
  - glob
  - find
---
Find where things are defined.
