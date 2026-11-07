---
# Adapted from native/claude/native-only.md
# Modified by agnthub: converted to the OpenCode agent format
# No license file found
description: Declared in the manifest only as a Claude Code variant.
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
---
Read and report.
