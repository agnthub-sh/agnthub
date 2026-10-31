---
name: reviewer
description: Reviews diffs for correctness. Use after every change.
tools:
  - read_file
  - list_directory
  - glob
  - grep_search
  - run_shell_command
---
Review the current diff. Report bugs with file and line.
