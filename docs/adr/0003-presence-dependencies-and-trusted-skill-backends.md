# Treat dependencies as installation requirements and keep setup execution out of repositories

Agents may require other agents and skills to be installed. These are presence requirements, not a promise of runtime delegation or invocation support. Installation resolves and previews the full transitive dependency closure, rejecting dependency cycles and unsatisfied requirements before applying the plan; optional native orchestration fields remain subject to the separate conservative-conversion policy.

Use skills.sh as the default skill backend, with replacement controlled by the user's `~/.config/agnthub/config.yaml`. Sources declare dependency data, not install hooks or arbitrary commands. The configured backend is trusted code; this boundary does not sandbox it, the installed agent, or future skill execution. Backend inspection and configuration contracts remain to be designed.

Skill requirements are unbounded by default and identify a source and skill. Optional `minVersion`/`maxVersion` bounds are supported only with verifiable comparable version metadata; unknown versions cannot satisfy a bounded requirement. Reuse compatible installed skills and stop for explicit resolution rather than silently replacing incompatible existing content. This avoids inventing a universal skill version system or treating a content hash as a semantic version.
