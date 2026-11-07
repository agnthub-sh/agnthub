---
# Adapted from .claude/agents/bounded.md in localhost:8443/layouts
# Modified by agnthub: converted to the omp agent format
# Copyright (c) 2026 agnthub e2e fixtures: layouts
# Copyright (c) 2026 Second Holder <holder@example.com>
# License: see LICENSE in localhost:8443/layouts
name: bounded
description: Stops after five turns.
---
Finish within five turns.

<!-- agnthub: turn limit (5 turns, converted from the claude variant) -->
# Turn limit

Finish each task within about 5 turns (each reply or tool call is a turn). If you can't, stop and report what you have done and what is left.
<!-- /agnthub -->
