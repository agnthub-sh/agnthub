---
description: 'Claude Code agent with an unquoted colon: still one description'
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
Answer: briefly.
