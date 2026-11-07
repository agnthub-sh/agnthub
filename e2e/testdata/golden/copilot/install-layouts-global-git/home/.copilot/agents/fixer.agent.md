---
# Adapted from .claude/agents/team/fixer.md in localhost:8443/layouts
# Modified by agnthub: converted to the GitHub Copilot CLI agent format
# Copyright (c) 2026 agnthub e2e fixtures: layouts
# Copyright (c) 2026 Second Holder <holder@example.com>
# License: see LICENSE in localhost:8443/layouts
name: fixer
description: Fixes failing tests with minimal edits.
tools:
  - read
  - search
  - edit
  - execute
---
Make the failing test pass with the smallest correct change.
