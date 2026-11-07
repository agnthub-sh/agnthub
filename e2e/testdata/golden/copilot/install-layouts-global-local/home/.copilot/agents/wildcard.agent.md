---
# Adapted from .claude/agents/wildcard.md
# Modified by agnthub: converted to the GitHub Copilot CLI agent format
# Copyright (c) 2026 agnthub e2e fixtures: layouts
# Copyright (c) 2026 Second Holder <holder@example.com>
# License: see LICENSE
name: wildcard
description: Fetches docs and fixes code; its tools use the whole-tool wildcard.
tools:
  - read
  - search
  - edit
  - execute
  - web
---
Read the docs, then fix the code.
