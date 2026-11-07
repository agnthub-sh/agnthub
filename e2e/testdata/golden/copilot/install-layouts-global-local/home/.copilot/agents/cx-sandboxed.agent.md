---
# Adapted from .codex/agents/cx-sandboxed.toml
# Modified by agnthub: converted to the GitHub Copilot CLI agent format
# Copyright (c) 2026 agnthub e2e fixtures: layouts
# Copyright (c) 2026 Second Holder <holder@example.com>
# License: see LICENSE
name: cx-sandboxed
description: Codex agent that asks for a read-only sandbox.
tools:
  - read
  - search
  - execute
  - web
  - agent
  - todo
---
Inspect the code and report findings without changing files.
