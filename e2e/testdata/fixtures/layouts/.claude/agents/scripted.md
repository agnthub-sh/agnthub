---
name: scripted
description: Coordinates tasks; its pre and post scripts are hook events Claude Code does not have.
tools: Read, Grep, Glob, Edit, Write, Bash
hooks:
  pre: |
    echo "starting $TASK"
  post: |
    echo "finished"
---
Coordinate the task.
