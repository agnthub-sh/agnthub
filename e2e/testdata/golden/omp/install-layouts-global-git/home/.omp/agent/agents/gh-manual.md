---
# Adapted from .github/agents/gh-manual.agent.md in localhost:8443/layouts
# Modified by agnthub: converted to the omp agent format
# Copyright (c) 2026 agnthub e2e fixtures: layouts
# Copyright (c) 2026 Second Holder <holder@example.com>
# License: see LICENSE in localhost:8443/layouts
name: gh-manual
description: Use only when the user explicitly asks for this agent. Prepares a release when the user asks for it.
tools:
  - read
  - grep
  - glob
  - find
  - edit
  - write
  - bash
---
Prepare the release the user asked for.
