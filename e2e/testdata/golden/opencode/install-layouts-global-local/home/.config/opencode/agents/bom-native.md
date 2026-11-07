---
# Adapted from .claude/agents/bom-native.md
# Modified by agnthub: converted to the OpenCode agent format
# Copyright (c) 2026 agnthub e2e fixtures: layouts
# Copyright (c) 2026 Second Holder <holder@example.com>
# License: see LICENSE
description: Claude Code agent saved with a UTF-8 byte order mark.
mode: subagent
permission:
  '*': deny
  read:
    '*': allow
    '*.env': ask
    '*.env.*': ask
    '*.env.example': allow
---
Read the file and quote its first line.
