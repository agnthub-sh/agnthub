---
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
