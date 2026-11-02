---
description: Claude Code agent saved with a UTF-8 byte order mark.
mode: subagent
permission:
  '*': deny
  read:
    '*': allow
    '*.env': ask
    '*.env.*': ask
    '*.env.example': allow
---
Read the file and quote its first line.
