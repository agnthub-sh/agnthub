---
description: Coordinates tasks; its pre and post scripts are hook events Claude Code does not have.
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
Coordinate the task.
