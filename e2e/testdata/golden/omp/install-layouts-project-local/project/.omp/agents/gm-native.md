---
# Adapted from .gemini/agents/gm-native.md
# Modified by agnthub: converted to the omp agent format
# Copyright (c) 2026 agnthub e2e fixtures: layouts
# Copyright (c) 2026 Second Holder <holder@example.com>
# License: see LICENSE
name: gm-native
description: Gemini CLI native agent that reads and searches.
tools:
  - read
  - grep
  - glob
  - find
---
Find the answer in the files.
