---
# Adapted from .github/agents/gh-colored.agent.md in localhost:8443/layouts
# Modified by agnthub: converted to the Gemini CLI agent format
# Copyright (c) 2026 agnthub e2e fixtures: layouts
# Copyright (c) 2026 Second Holder <holder@example.com>
# License: see LICENSE in localhost:8443/layouts
name: gh-colored
description: Copilot CLI agent with keys Copilot CLI ignores.
tools:
  - read_file
  - list_directory
  - glob
  - grep_search
  - activate_skill
---
Answer questions by reading code.
