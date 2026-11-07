---
# Adapted from .claude/agents/recaller.md
# Modified by agnthub: converted to the GitHub Copilot CLI agent format
# Copyright (c) 2026 agnthub e2e fixtures: layouts
# Copyright (c) 2026 Second Holder <holder@example.com>
# License: see LICENSE
name: recaller
description: Reads code with memory but cannot write files.
tools:
  - read
  - search
---
Answer questions about the code.
