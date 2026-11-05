---
name: relaxed
description: Applies edits without asking in Claude Code; other harnesses ask as usual.
tools:
  - read_file
  - list_directory
  - glob
  - grep_search
  - replace
  - write_file
  - run_shell_command
---
Apply the requested edits.
