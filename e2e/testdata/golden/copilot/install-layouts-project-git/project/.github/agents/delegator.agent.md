---
# Adapted from .claude/agents/delegator.md in localhost:8443/layouts
# Modified by agnthub: converted to the GitHub Copilot CLI agent format
# Copyright (c) 2026 agnthub e2e fixtures: layouts
# Copyright (c) 2026 Second Holder <holder@example.com>
# License: see LICENSE in localhost:8443/layouts
name: delegator
description: Reads code and delegates subtasks to other agents.
tools:
  - read
  - search
  - agent
---
Split the work and delegate each part.
