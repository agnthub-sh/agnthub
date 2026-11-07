---
# Adapted from .claude/agents/no-shell.md
# Modified by agnthub: converted to the OpenCode agent format
# Copyright (c) 2026 agnthub e2e fixtures: layouts
# Copyright (c) 2026 Second Holder <holder@example.com>
# License: see LICENSE
description: May use every tool except the shell.
mode: subagent
permission:
  bash: deny
---
Work without running commands.
