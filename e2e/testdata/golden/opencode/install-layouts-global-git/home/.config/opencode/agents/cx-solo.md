---
# Adapted from .codex/agents/cx-solo.toml in localhost:8443/layouts
# Modified by agnthub: converted to the OpenCode agent format
# Copyright (c) 2026 agnthub e2e fixtures: layouts
# Copyright (c) 2026 Second Holder <holder@example.com>
# License: see LICENSE in localhost:8443/layouts
description: Codex agent that turns off delegation.
mode: subagent
permission:
  task: deny
---
Do the work yourself without spawning other agents.
