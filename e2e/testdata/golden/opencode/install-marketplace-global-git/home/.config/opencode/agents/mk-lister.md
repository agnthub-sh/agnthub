---
# Adapted from plugins/listed/mk-lister.md in localhost:8443/marketplace
# Modified by agnthub: converted to the OpenCode agent format
# No license file found in localhost:8443/marketplace
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
