---
# Adapted from .claude/agents/web-searcher.md in localhost:8443/layouts
# Modified by agnthub: converted to the OpenCode agent format
# Copyright (c) 2026 agnthub e2e fixtures: layouts
# Copyright (c) 2026 Second Holder <holder@example.com>
# License: see LICENSE in localhost:8443/layouts
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
