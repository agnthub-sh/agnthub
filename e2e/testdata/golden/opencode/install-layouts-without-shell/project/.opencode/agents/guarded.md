---
description: Runs with a Claude Code permission mode no other harness has.
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
Plan the requested edits.
