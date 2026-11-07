---
# Adapted from .codex/agents/cx-sandboxed.toml in localhost:8443/layouts
# Modified by agnthub: converted to the OpenCode agent format
# Copyright (c) 2026 agnthub e2e fixtures: layouts
# Copyright (c) 2026 Second Holder <holder@example.com>
# License: see LICENSE in localhost:8443/layouts
description: Codex agent that asks for a read-only sandbox.
mode: subagent
permission:
  edit: deny
---
Inspect the code and report findings without changing files.
