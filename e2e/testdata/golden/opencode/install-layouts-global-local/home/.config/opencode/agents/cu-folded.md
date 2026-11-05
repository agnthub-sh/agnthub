---
description: Cursor agent whose description spans several lines in the file.
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
  skill: allow
  todowrite: allow
  webfetch: allow
  websearch: allow
---
Inspect without editing.
