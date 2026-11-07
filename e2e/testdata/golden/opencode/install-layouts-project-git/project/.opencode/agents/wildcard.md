---
description: Fetches docs and fixes code; its tools use the whole-tool wildcard.
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
  webfetch: allow
---
Read the docs, then fix the code.
