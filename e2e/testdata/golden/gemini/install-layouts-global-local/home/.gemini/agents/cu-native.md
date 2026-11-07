---
# Adapted from .cursor/agents/cu-native.md
# Modified by agnthub: converted to the Gemini CLI agent format
# Copyright (c) 2026 agnthub e2e fixtures: layouts
# Copyright (c) 2026 Second Holder <holder@example.com>
# License: see LICENSE
name: cu-native
description: Cursor-native read-only agent.
tools:
  - read_file
  - list_directory
  - glob
  - grep_search
  - web_fetch
  - google_web_search
  - write_todos
  - activate_skill
---
Inspect without editing.
