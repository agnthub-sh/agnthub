---
# Adapted from plugins/listed/mk-lister.md
# Modified by agnthub: converted to the omp agent format
# No license file found
name: mk-lister
description: Use this agent to list a plugin's files. Packaged in a plugin.json agents array.
tools:
  - read
  - grep
  - glob
  - find
  - edit
  - write
  - bash
---

List the files the user names and say what each one is for.
