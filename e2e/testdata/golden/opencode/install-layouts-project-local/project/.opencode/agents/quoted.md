---
description: 'Reviews: the "risky" parts of a change, and it''s careful.'
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
Review the risky parts.
