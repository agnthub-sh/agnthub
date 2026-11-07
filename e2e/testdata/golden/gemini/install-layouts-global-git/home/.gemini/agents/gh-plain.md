---
# Adapted from .github/agents/gh-plain.md in localhost:8443/layouts
# Modified by agnthub: converted to the Gemini CLI agent format
# Copyright (c) 2026 agnthub e2e fixtures: layouts
# Copyright (c) 2026 Second Holder <holder@example.com>
# License: see LICENSE in localhost:8443/layouts
name: gh-plain
description: Copilot CLI agent in a plain .md file that reads and searches.
tools:
  - read_file
  - list_directory
  - glob
  - grep_search
  - activate_skill
---
Find where things are defined.
