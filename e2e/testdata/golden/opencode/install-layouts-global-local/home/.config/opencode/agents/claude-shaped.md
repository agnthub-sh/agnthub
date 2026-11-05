---
description: Portable agent written for Claude Code, with its model, tools and color.
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
Explain how the code fits together.
