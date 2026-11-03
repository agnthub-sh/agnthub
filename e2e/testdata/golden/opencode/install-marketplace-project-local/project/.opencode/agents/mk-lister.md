---
description: Use this agent to list a plugin's files. Packaged in a plugin.json agents array.
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

List the files the user names and say what each one is for.
