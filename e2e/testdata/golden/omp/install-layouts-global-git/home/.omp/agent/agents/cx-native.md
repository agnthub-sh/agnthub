---
# Adapted from .codex/agents/cx-native.toml in localhost:8443/layouts
# Modified by agnthub: converted to the omp agent format
# Copyright (c) 2026 agnthub e2e fixtures: layouts
# Copyright (c) 2026 Second Holder <holder@example.com>
# License: see LICENSE in localhost:8443/layouts
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
