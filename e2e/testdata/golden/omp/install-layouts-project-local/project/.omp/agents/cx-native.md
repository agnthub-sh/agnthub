---
# Adapted from .codex/agents/cx-native.toml
# Modified by agnthub: converted to the omp agent format
# Copyright (c) 2026 agnthub e2e fixtures: layouts
# Copyright (c) 2026 Second Holder <holder@example.com>
# License: see LICENSE
name: cx-native
description: Codex-native agent without the shell.
tools:
  - read
  - grep
  - glob
  - find
  - edit
  - write
  - web_search
  - task
  - todo
---
Explain the code without running commands.
