---
# Adapted from .claude/agents/wildcard.md in localhost:8443/layouts
# Modified by agnthub: converted to the Gemini CLI agent format
# Copyright (c) 2026 agnthub e2e fixtures: layouts
# Copyright (c) 2026 Second Holder <holder@example.com>
# License: see LICENSE in localhost:8443/layouts
name: wildcard
description: Fetches docs and fixes code; its tools use the whole-tool wildcard.
tools:
  - read_file
  - list_directory
  - glob
  - grep_search
  - replace
  - write_file
  - run_shell_command
  - web_fetch
---
Read the docs, then fix the code.
