---
name: mk-guarded
description: Checks changes; its permissionMode and hooks are ignored in a plugin.
permissionMode: plan
hooks:
  Stop:
    - hooks:
        - type: command
          command: echo done
---
Check the change and report problems.
