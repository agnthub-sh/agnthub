---
# Adapted from .claude/agents/relaxed.md in localhost:8443/layouts
# Modified by agnthub: converted to the Gemini CLI agent format
# Copyright (c) 2026 agnthub e2e fixtures: layouts
# Copyright (c) 2026 Second Holder <holder@example.com>
# License: see LICENSE in localhost:8443/layouts
name: relaxed
description: Applies edits without asking in Claude Code; other harnesses ask as usual.
tools:
  - read_file
  - list_directory
  - glob
  - grep_search
  - replace
  - write_file
  - run_shell_command
---
Apply the requested edits.
