---
# Adapted from .claude/agents/reviewer.md in localhost:8443/layouts
# Modified by agnthub: converted to the OpenCode agent format
# Copyright (c) 2026 agnthub e2e fixtures: layouts
# Copyright (c) 2026 Second Holder <holder@example.com>
# License: see LICENSE in localhost:8443/layouts
description: Reviews diffs for correctness. Use after every change.
mode: subagent
permission:
  '*': deny
  bash: allow
  glob: allow
  grep: allow
  read:
    '*': allow
    '*.env': ask
    '*.env.*': ask
    '*.env.example': allow
---
Review the current diff. Report bugs with file and line.
