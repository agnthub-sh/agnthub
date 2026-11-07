---
# Adapted from .claude/agents/relaxed.md in localhost:8443/layouts
# Modified by agnthub: converted to the GitHub Copilot CLI agent format
# Copyright (c) 2026 agnthub e2e fixtures: layouts
# Copyright (c) 2026 Second Holder <holder@example.com>
# License: see LICENSE in localhost:8443/layouts
name: relaxed
description: Applies edits without asking in Claude Code; other harnesses ask as usual.
tools:
  - read
  - search
  - edit
  - execute
---
Apply the requested edits.
