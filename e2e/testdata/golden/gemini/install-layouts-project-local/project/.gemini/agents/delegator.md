---
# Adapted from .claude/agents/delegator.md
# Modified by agnthub: converted to the Gemini CLI agent format
# Copyright (c) 2026 agnthub e2e fixtures: layouts
# Copyright (c) 2026 Second Holder <holder@example.com>
# License: see LICENSE
name: delegator
description: Reads code and delegates subtasks to other agents.
tools:
  - read_file
  - list_directory
  - glob
  - grep_search
---
Split the work and delegate each part.
