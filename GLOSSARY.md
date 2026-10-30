# Agent Registry

The language of discovering and installing reusable agent definitions across harnesses.

## Language

**Agent**:
A named, reusable definition of an assistant's role and behavior, with one identity across its supported harnesses.
_Avoid_: Skill, harness, variant when referring to the whole agent

**Harness**:
An application that loads and runs an agent definition.
_Avoid_: Agent when referring to the host application

**Harness Settings**:
User-selected installation context specific to a Harness, such as its named profile, distinct from an Agent definition or Variant.
_Avoid_: Agent metadata, arbitrary native flags

**Harness Profile**:
A named configuration context within one Harness under which Agents are discovered or run; it is not another Harness or local CLI installation identity.
_Avoid_: Agent Variant, unique user

**Variant**:
A harness-specific representation of an agent that may differ in instructions or configuration while retaining the same agent identity.
_Avoid_: Version when referring to a harness-specific representation

**Release**:
An identified revision of published agent content over time, distinct from its harness-specific variants.
_Avoid_: Variant when referring to revision history

**Source Repository**:
An author's repository containing one or more agent definitions and their associated content.
_Avoid_: Registry when referring to the author's repository

**Registry**:
The public discovery index of source repositories and their agents; an agent need not be listed to be installed directly.
_Avoid_: Artifact store, source repository

**Registration**:
The inclusion of a source repository in the public registry through an explicit submission by a person signed in with GitHub, recorded with who submitted it; only repositories with detected agents are listed.
_Avoid_: Ownership verification, endorsement, automatic registration

**Repository Writer**:
A person with current effective GitHub Write, Maintain or Admin access to a source repository, eligible to request its registry suppression.
_Avoid_: Commit author, historical contributor, committer when only Git-history attribution is meant

**Registry Administrator**:
An agnthub operator permitted to perform takedowns and clear persistent repository suppression, distinct from a repository's own access roles.
_Avoid_: Repository administrator

**Repository Suppression**:
A persistent registry exclusion created by a repository opt-out or takedown that remains in force until an agnthub administrator clears it.
_Avoid_: Telemetry opt-out, temporary visibility failure

**Visibility Suspension**:
A temporary exclusion from public discovery because the repository cannot currently be confirmed public; fresh public confirmation can restore eligibility only when no repository suppression remains.
_Avoid_: Takedown, repository opt-out

**Sharing Opt-out**:
An installer's preference to disable installation reports, without changing a repository's eligibility for explicit public submission.
_Avoid_: Repository suppression, repository opt-out

**Unique Agent Installation**:
A reported agent installation associated with one agent-scoped local CLI identity, independent of its harnesses, local scopes and reinstalls; it is not a unique person or a verified physical device.
_Avoid_: Unique user, download count

**Harness Installation**:
A reported installation of an agent for one harness under a unique agent installation identity.
_Avoid_: Unique agent installation when counting target-specific adoption

**Conversion**:
The translation of an agent definition into a representation suitable for a different harness.
_Avoid_: Compatibility when only a format translation has been established

**Compatibility**:
The ability to represent an agent for a harness without losing its declared behavioral or safety constraints.
_Avoid_: Conversion success when semantic preservation has not been established

**Skill**:
A reusable capability expressed through instructions and associated resources that an agent can use, distinct from the agent's own role definition.
_Avoid_: Agent when referring to a skill

**Dependency**:
Another agent or skill required to be installed for an agent to function; declaring it does not promise that the agent can invoke or delegate to it.
_Avoid_: Variant when referring to a required companion agent or skill
