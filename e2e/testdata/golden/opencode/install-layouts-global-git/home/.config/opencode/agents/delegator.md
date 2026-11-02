---
description: Reads code and delegates subtasks to other agents.
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
  task: allow
---
Split the work and delegate each part.
