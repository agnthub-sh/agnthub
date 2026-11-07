---
# Adapted from .claude/agents/no-shell.md in localhost:8443/layouts
# Modified by agnthub: converted to the GitHub Copilot CLI agent format
# Copyright (c) 2026 agnthub e2e fixtures: layouts
# Copyright (c) 2026 Second Holder <holder@example.com>
# License: see LICENSE in localhost:8443/layouts
name: no-shell
description: May use every tool except the shell.
tools:
  - read
  - search
  - edit
  - web
  - agent
  - todo
---
Work without running commands.
