---
# Adapted from plugins/listed/mk-lister.md
# Modified by agnthub: converted to the Gemini CLI agent format
# No license file found
name: mk-lister
description: Use this agent to list a plugin's files. Packaged in a plugin.json agents array.
tools:
  - read_file
  - list_directory
  - glob
  - grep_search
  - replace
  - write_file
  - run_shell_command
---

List the files the user names and say what each one is for.
