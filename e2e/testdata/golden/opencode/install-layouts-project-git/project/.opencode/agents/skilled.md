---
# Adapted from .claude/agents/skilled.md in localhost:8443/layouts
# Modified by agnthub: converted to the OpenCode agent format
# Copyright (c) 2026 agnthub e2e fixtures: layouts
# Copyright (c) 2026 Second Holder <holder@example.com>
# License: see LICENSE in localhost:8443/layouts
description: Reviews releases with preloaded skills.
mode: subagent
permission:
  '*': deny
  bash: allow
  edit: allow
  glob: allow
  grep: allow
  read:
    '*': allow
    '*.env': ask
    '*.env.*': ask
    '*.env.example': allow
  skill: allow
---
Review the release with your skills.

<!-- agnthub: preloaded skills (converted from the claude variant) -->
# Skills

Before you start a task, load these skills and follow them: `bug-report`, `release-checklist`. Load each one with the `skill` tool. If one of them isn't available, continue without it.
<!-- /agnthub -->
