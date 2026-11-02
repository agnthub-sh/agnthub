---
description: Searches the web and fetches pages.
mode: subagent
permission:
  '*': deny
  read:
    '*': allow
    '*.env': ask
    '*.env.*': ask
    '*.env.example': allow
  webfetch: allow
  websearch: allow
---
Search the web, then cite each source.
