---
# Adapted from .claude/agents/web-searcher.md in localhost:8443/layouts
# Modified by agnthub: converted to the GitHub Copilot CLI agent format
# Copyright (c) 2026 agnthub e2e fixtures: layouts
# Copyright (c) 2026 Second Holder <holder@example.com>
# License: see LICENSE in localhost:8443/layouts
name: web-searcher
description: Searches the web and fetches pages.
tools:
  - read
  - web
---
Search the web, then cite each source.
