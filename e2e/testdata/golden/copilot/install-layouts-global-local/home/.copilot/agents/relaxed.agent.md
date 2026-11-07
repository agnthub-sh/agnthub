---
# Adapted from .claude/agents/relaxed.md
# Modified by agnthub: converted to the GitHub Copilot CLI agent format
# Copyright (c) 2026 agnthub e2e fixtures: layouts
# Copyright (c) 2026 Second Holder <holder@example.com>
# License: see LICENSE
name: relaxed
description: Applies edits without asking in Claude Code; other harnesses ask as usual.
tools:
  - read
  - search
  - edit
  - execute
---
Apply the requested edits.
