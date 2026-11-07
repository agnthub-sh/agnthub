---
# Adapted from .claude/agents/differ.md
# Modified by agnthub: converted to the GitHub Copilot CLI agent format
# Copyright (c) 2026 agnthub e2e fixtures: layouts
# Copyright (c) 2026 Second Holder <holder@example.com>
# License: see LICENSE
name: differ
description: Fixes review findings and may only run git diff.
tools:
  - read
  - search
  - edit
---
Check the diff, then fix each finding.
