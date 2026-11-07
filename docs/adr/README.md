# Architecture decisions

The decisions that shape the CLI. Numbers are shared with the registry service's own records, so gaps are decisions about the hosted registry rather than the CLI.

- [0002 Keep one agent identity across harness variants and require conservative conversion](0002-agent-variants-and-conservative-conversion.md)
- [0003 Treat dependencies as installation requirements and keep setup execution out of repositories](0003-presence-dependencies-and-trusted-skill-backends.md)
- [0005 Count agent-scoped local CLI identities, with separate harness adoption](0005-agent-scoped-install-identities.md)
- [0009 Convert native agents across harnesses through a shared semantics model](0009-cross-harness-conversion-through-shared-semantics.md)
- [0011 Discover the agents of Claude Code plugins a repository packages](0011-claude-code-plugin-layouts.md)
- [0012 Read portable cores as Claude Code agents and install reduced capability visibly](0012-portable-cores-as-claude-and-reduced-capability.md)
- [0014 Emulate worktree isolation and manual-only invocation where a harness lacks them](0014-emulate-worktree-isolation-and-manual-only-invocation.md)
