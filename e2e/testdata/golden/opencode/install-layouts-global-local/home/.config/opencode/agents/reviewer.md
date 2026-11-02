---
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
