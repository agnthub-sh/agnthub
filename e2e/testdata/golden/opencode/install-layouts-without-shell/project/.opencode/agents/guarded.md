---
# Adapted from .claude/agents/guarded.md
# Modified by agnthub: converted to the OpenCode agent format
# Copyright (c) 2026 agnthub e2e fixtures: layouts
# Copyright (c) 2026 Second Holder <holder@example.com>
# License: see LICENSE
description: Runs with a Claude Code permission mode no other harness has.
mode: subagent
permission:
  '*': deny
  glob: allow
  grep: allow
  read:
    '*': allow
    '*.env': ask
    '*.env.*': ask
    '*.env.example': allow
  skill: allow
  todowrite: allow
  webfetch: allow
  websearch: allow
---
Plan the requested edits.
