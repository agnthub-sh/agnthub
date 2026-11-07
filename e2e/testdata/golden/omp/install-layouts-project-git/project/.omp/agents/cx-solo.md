---
# Adapted from .codex/agents/cx-solo.toml in localhost:8443/layouts
# Modified by agnthub: converted to the omp agent format
# Copyright (c) 2026 agnthub e2e fixtures: layouts
# Copyright (c) 2026 Second Holder <holder@example.com>
# License: see LICENSE in localhost:8443/layouts
name: cx-solo
description: Codex agent that turns off delegation.
tools:
  - read
  - grep
  - glob
  - find
  - edit
  - write
  - bash
  - web_search
  - todo
---
Do the work yourself without spawning other agents.
