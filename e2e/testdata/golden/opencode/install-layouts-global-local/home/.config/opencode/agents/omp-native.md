---
description: omp-native agent with read and search tools.
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
  webfetch: allow
---
Locate the relevant code.
