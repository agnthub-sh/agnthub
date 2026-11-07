---
# Adapted from .claude/agents/differ.md
# Modified by agnthub: converted to the OpenCode agent format
# Copyright (c) 2026 agnthub e2e fixtures: layouts
# Copyright (c) 2026 Second Holder <holder@example.com>
# License: see LICENSE
description: Fixes review findings and may only run git diff.
mode: subagent
permission:
  '*': deny
  bash:
    '*': deny
    git diff *: allow
  edit: allow
  glob: allow
  grep: allow
  read:
    '*': allow
    '*.env': ask
    '*.env.*': ask
    '*.env.example': allow
---
Check the diff, then fix each finding.
