# CLI contract

The installer behavior the agnthub CLI implements: how sources are discovered, how agents are selected, converted, locked, updated and removed, and how Skill Dependencies are installed. It is the CLI part of the registry design; the registry service that indexes public repositories is developed separately. Terms are defined in the [glossary](../GLOSSARY.md), and the decisions behind them in the [architecture decisions](adr/).

## Repository discovery and authoring

- The portable core is YAML frontmatter `name` and `description`, followed by Markdown instructions. Default portable layouts are a root `AGENT.md`, `agents/<id>.md`, or `agents/<id>/AGENT.md`. `AGENTS.md` project instructions are not automatically treated as an installable custom agent.
- Native discovery recognizes the documented agent directories for the seven selected targets: `.claude/agents`, `.opencode/agents`, `.codex/agents`, `.github/agents`, `.cursor/agents`, `.gemini/agents` and `.omp/agents`, with each target's native suffix/schema rules. Destination resolution also respects documented scope/profile/config-directory conventions; do not install into every profile implicitly.
- With no manifest, discover supported definitions and report ambiguous identity/variant groups instead of taking the first duplicate. If `agnthub.yaml` is present, its declared agents form the authoritative inventory; native files can be referenced without conversion as target variants. This gives authors a deterministic way to narrow indexing/install-all scope.
- Logical public identity is GitHub host + immutable repository ID + declared agent ID. Display routes/names are mutable locators. A new repository at an old slug cannot inherit counts, suppression or trust. Generic Git/local sources use source-qualified local identities without sending private paths to the service.
- Manifest paths are repository-relative inert data; reject traversal, symlinks/submodules and executable/remote includes. Repository source must not execute checkout filters, install hooks, builds or template code merely to discover agents.
- Common dependencies apply to every selected variant; variants may add requirements, not silently subtract common ones. Authors put genuinely target-only dependencies on the variant. Same-repository agent references inherit the selected snapshot; external references name their source and optional Git ref. Conflicting revisions for the same dependency identity fail rather than invoking an undeclared version solver.
- `categories` (optional, repository level and per agent) lists category slugs from the [category taxonomy](../category/category.go) so the registry can group the repository's agents; an agent's own list replaces the repository list for that agent. Slugs must be slug-shaped (lowercase letters, digits and single hyphens, at most 64 bytes) or the manifest is rejected; slugs the CLI does not know are kept as declared, because the taxonomy grows and the registry decides which it recognizes. Categories are listing metadata only: they never change what installs. Manifests that declare `categories` need agnthub 0.4.0 or newer (earlier versions reject unknown manifest keys).

Illustrative manifest only; these example repositories/agents are not claimed to exist:

```yaml
schemaVersion: 1
categories: [code-review, software-engineering]
agents:
  reviewer:
    portable: agents/reviewer/AGENT.md
    categories: [code-review, security]
    variants:
      opencode:
        path: .opencode/agents/reviewer.md
      codex:
        path: .codex/agents/reviewer.toml
    dependencies:
      agents:
        - agent: planner
      skills:
        - source: "example-org/skills#v1.4.0"
          skill: code-review
          minVersion: "1.2.0"
          maxVersion: "2.0.0"
  planner:
    portable: agents/planner/AGENT.md
```

The source ref selects bytes; the skill's own `metadata.version`, not the tag spelling, proves whether bounds are satisfied. Native variants can contain different prompts/configuration while retaining one logical identity. The small portable core inherits a target's session model; preserve explicit native model choices only on a compatible target and never silently substitute providers/models.

## CLI and lifecycle

Command vocabulary is `agnthub add`, `find`, `list`, `check`, `update`, `remove` and `restore`; `add --list` lists source inventory without installation. `--agent` selects logical definitions and `--harness` selects host applications. Support repeated explicit selectors, repeatable `--extra <harness>.<key>=<value>` settings, `--global`, noninteractive `--yes`, machine-readable `--json`, and `--all` with the broad semantics already chosen. Git refs use `#ref`, separate from agent selection.

Illustrative command shape, not an already-published executable:

```text
npx agnthub add owner/repo
agnthub add owner/repo --list
agnthub add owner/repo#v1 --agent reviewer --harness codex
agnthub add owner/repo --harness omp --global --extra omp.profile=hep
agnthub add owner/repo --harness codex --harness omp --extra omp.profile=hep
agnthub add owner/repo --all
```

The normal command is interactive: choose definitions and detected/supported targets, preview paths/variants/dependencies/scope, then apply. `--yes` suppresses prompts but cannot invent a missing ambiguous selection, silently drop targets, change scope or approve conflicts. `--all` does select every logical source agent and supported target and skips prompts; any invalid selected combination must fail before applying that plan.

### Harness-specific settings

- Each `--extra` occurrence supplies one explicitly scoped `<harness>.<key>=<value>` pair. Repeat the flag for different supported settings; no comma-list syntax or unqualified shorthand. `--extra omp.profile=hep` is the canonical form for omp's `profile=hep` setting, including when multiple Harnesses are selected.
- Each adapter declares and validates its supported setting names/types. The initial concrete setting is `omp.profile`, a nonempty native-valid profile name. Future settings require explicit adapter support, not arbitrary native flags/frontmatter or a plugin/configuration execution channel.
- Split on the first equals sign and preserve the remainder literally for adapter validation. Reject malformed/unqualified pairs, unknown Harnesses or keys, duplicate keys, invalid values and settings for unselected targets before any filesystem mutation. Values are never shell-expanded or blindly forwarded to a Harness or Skill installer.
- Explicit settings override detected defaults for that Harness only. When no profile is supplied, retain the documented native current/default-profile resolution or require selection if ambiguous. An extra does not implicitly select its Harness, change project/global scope or weaken Variant/permission checks.
- `--all --extra omp.profile=hep` applies the value only to omp; it does not select every omp profile. There is one selected profile per Harness per plan. Separate invocations may create separate profile-local installations without silently moving existing ones.
- Show effective settings and actual destinations in human and JSON plans. A named-profile global omp install uses the native selected profile's Agent directory, respecting documented config-root behavior. It may prepare the required Agent directory but must not initialize/overwrite auth, settings, aliases or unrelated profile content.
- omp project Agents remain in the shared project Agent directory. A profile option can affect verification/configuration context but does not make a project file private to that profile. Do not create redundant project files or mutate a shared artifact on behalf of one binding without resolving effects on other bindings.
- Use the same selected context when resolving Agent and Skill visibility, profile-specific discovery settings and shadowing. The default skills.sh backend still uses a supported destination mapping; never invent an omp target/profile flag or append raw extras to its command.
- Persist resolved settings/profile destination bindings in user-local ownership records, separate from portable source/generation locks. List/check show recorded contexts. Update/remove must select the intended recorded binding, not silently follow a newly active profile; ambiguous mutating selection requires a prompt or explicit settings and fails noninteractively.
- Restore reuses a recorded local target binding or explicitly resolves the destination context for a fresh environment. Selecting a different profile is a different destination binding, not permission to move/delete the old installation. Shared project artifacts remain protected while another binding requires them.
- Browser command generation may offer an optional omp Profile field, but builds the flag locally with literal-safe shell quoting and adapter validation, without enumerating local profiles or sending values through registry APIs, URLs or analytics. CLI registration/telemetry sends only the allowed Harness identifiers, never extras. Another omp profile under the same local CLI identity adds neither another logical total nor another unique omp counter.

Native reference: [omp CLI profile option](https://omp.sh/docs/cli), [Agent scope/profile locations](https://omp.sh/docs/subagent-authoring), [config-root environment behavior](https://omp.sh/docs/env). These establish the native contract, not runtime-tested agnthub behavior.

### Locked lifecycle and ownership

Use deterministic project lock state for resolved sources/revisions/content, selected variants/dependencies, generated artifacts and converter provenance. Keep anonymous installation identities in separate user-local state, never in committed locks. Explicit updates show content/variant/dependency changes and retain local-edit protection. Frozen restoration requires the recorded content and compatible generation contract; unavailable source/converter evidence is an actionable error, not a substitution.

Only delete unmodified artifacts demonstrably owned by the selected installation. Dependencies remain by default. Cleanup is separately explicit and must not assume an unreferenced skill is unused outside agnthub. No registry outage or suppression can remotely delete local installations or prevent a known direct Git/local source from being installed.

## Skill installer configuration contract

The user-level `~/.config/agnthub/config.yaml` (`$XDG_CONFIG_HOME/agnthub/config.yaml`) supplies `skillInstaller.executable` and `skillInstaller.args`, with optional `projectArgs` and `globalArgs` appended for the selected scope. No repository-local configuration can replace the executable or these argument templates.

The default invocation shape is:

```yaml
skillInstaller:
  executable: npx
  args:
    - --yes
    - skills
    - add
    - "{source}"
    - --skill
    - "{skill}"
    - --agent
    - "{target}"
    - --yes
  projectArgs: []
  globalArgs:
    - --global
```

`{source}` is the resolved source including its selected Git revision, `{skill}` is one required skill identity, and `{target}` is the skill installer's target identifier selected by the harness adapter. Each expanded argument remains one argument even when its value contains whitespace or shell metacharacters. Invalid configuration or unknown placeholders fail before invoking the backend. A custom installer with different target naming can use a trusted wrapper. Independent verification checks effective skill discovery, provenance, requested scope and version requirements; it does not rely on output wording.

Backend target identifiers are not necessarily agnthub harness identifiers. The skills.sh target list has no `omp` entry, while omp discovers the shared `.agents/skills` and `~/.agents/skills` locations. The omp adapter therefore uses skills.sh's `universal` target, which installs into exactly those directories, and verifies omp visibility in the selected profile's context, including discovery settings and shadowing; it never invokes an invented `skills --agent omp` target. The tested mapping and per-Harness discovery rules are recorded in [skill dependency research](research/skill-dependencies.md).

