---
description: Prepares a release when the user asks for it.
mode: primary
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
  skill: allow
---
Prepare the release the user asked for.
