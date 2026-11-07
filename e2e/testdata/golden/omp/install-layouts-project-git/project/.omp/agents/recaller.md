---
# Adapted from .claude/agents/recaller.md in localhost:8443/layouts
# Modified by agnthub: converted to the omp agent format
# Copyright (c) 2026 agnthub e2e fixtures: layouts
# Copyright (c) 2026 Second Holder <holder@example.com>
# License: see LICENSE in localhost:8443/layouts
name: recaller
description: Reads code with memory but cannot write files.
tools:
  - read
  - grep
  - glob
  - find
---
Answer questions about the code.
