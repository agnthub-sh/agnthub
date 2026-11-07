---
# Adapted from .claude/agents/isolated.md in localhost:8443/layouts
# Modified by agnthub: converted to the OpenCode agent format
# Copyright (c) 2026 agnthub e2e fixtures: layouts
# Copyright (c) 2026 Second Holder <holder@example.com>
# License: see LICENSE in localhost:8443/layouts
description: Implements a change in its own git worktree.
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
---
IMPORTANT: Work only inside a dedicated git worktree. Before changing anything, create one from the repository root with your shell tool, e.g. `git worktree add ../<repo>-isolated-<short-id> -b agent/isolated-<short-id>`. Make every edit, command and commit inside it: give file tools paths inside the worktree, and start each shell command with `cd <worktree> &&` unless your shell keeps its directory. Never modify files in the user's original working tree. When done, report the worktree path and branch so the user can review and merge.

Implement the requested change and test it.
