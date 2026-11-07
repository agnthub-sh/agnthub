---
# Adapted from .claude/agents/skilled.md in localhost:8443/layouts
# Modified by agnthub: converted to the Gemini CLI agent format
# Copyright (c) 2026 agnthub e2e fixtures: layouts
# Copyright (c) 2026 Second Holder <holder@example.com>
# License: see LICENSE in localhost:8443/layouts
name: skilled
description: Reviews releases with preloaded skills.
tools:
  - read_file
  - list_directory
  - glob
  - grep_search
  - replace
  - write_file
  - run_shell_command
  - activate_skill
---
Review the release with your skills.

<!-- agnthub: preloaded skills (converted from the claude variant) -->
# Skills

Before you start a task, load these skills and follow them: `bug-report`, `release-checklist`. Load each one with the `activate_skill` tool. If one of them isn't available, continue without it.
<!-- /agnthub -->
