---
# Adapted from .claude/agents/allowlisted.md in localhost:8443/layouts
# Modified by agnthub: converted to the Gemini CLI agent format
# Copyright (c) 2026 agnthub e2e fixtures: layouts
# Copyright (c) 2026 Second Holder <holder@example.com>
# License: see LICENSE in localhost:8443/layouts
name: allowlisted
description: Reads code under an allowed-tools key Claude Code ignores.
tools:
  - read_file
  - list_directory
  - glob
  - grep_search
---
Read the code.
