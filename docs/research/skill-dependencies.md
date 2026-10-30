# Skill Dependencies: backend mapping and Harness skill discovery

Researched and runtime-tested 2026-10-29 with skills.sh 1.7.4 (`npx skills`), Claude Code 2.1.309, Codex CLI 0.164.0, OpenCode 2.1.3 (and 1.19.5 from source), GitHub Copilot CLI 1.0.105, Cursor CLI 2026.10.22-27dcf5f, Gemini CLI 0.66.0 and omp 18.13.3. Every experiment used a temporary `HOME` and project. **[RUN]** = observed at runtime, **[SRC]** = read in the shipped code, **[DOC]** = vendor documentation. Harness behavior changes between releases; this is the tested matrix, not a compatibility promise.

## skills.sh destinations [RUN][SRC]

`skills add <src> --skill <name> --agent <target> --yes [--global]` with a single target copies the skill directory (no symlink) and excludes `.git`, `__pycache__`, `__pypackages__` and `metadata.json`:

| `--agent` | project | global |
| --- | --- | --- |
| `claude-code` | `.claude/skills/<name>` | `${CLAUDE_CONFIG_DIR:-~/.claude}/skills/<name>` |
| `codex`, `opencode`, `github-copilot`, `cursor`, `gemini-cli`, `universal`, `pi` | `.agents/skills/<name>` | `~/.agents/skills/<name>` (always `$HOME`, never the Harness home) |

Side effects: project `skills-lock.json`, global `~/.agents/.skill-lock.json`. A commit SHA after `#` is accepted for `owner/repo#<sha>` and Git URLs, so agnthub passes the validated commit as `{source}`. There is **no `omp` target**: agnthub maps omp to the `universal` target, whose `.agents/skills` destinations omp's `agents` provider reads in every profile.

## Harness discovery (highest precedence first)

| Harness | Project | User | Identity | Duplicates |
| --- | --- | --- | --- | --- |
| Claude Code | `.claude/skills` from cwd up to the git root (non-git: to `/`) | managed `/etc/claude-code/.claude/skills` (unless `CLAUDE_CODE_DISABLE_POLICY_SKILLS`), then `${CLAUDE_CONFIG_DIR:-~/.claude}/skills`; managed and user **beat** project | directory name | first wins |
| Codex | `.agents/skills` from cwd up to the git root (non-git: cwd only), `.codex/skills` | `~/.agents/skills`, deprecated `$CODEX_HOME/skills`, `/etc/codex/skills` | frontmatter `name` | every copy loads and `$name` then selects none: any second copy is a conflict |
| OpenCode 2 | `.opencode/skill(s)` cwd→`/`, then config dir `skill(s)` (`OPENCODE_CONFIG_DIR` or `$XDG_CONFIG_HOME/opencode`), `.agents/skills` cwd→`/`, `~/.agents/skills`, `.claude/skills` cwd→`/`, `~/.claude/skills` | | directory name (v2), frontmatter (v1) | v1 picks nondeterministically: every copy must be compatible |
| Copilot CLI | `.github/skills`, then `.agents/skills`, then `.claude/skills`, each from cwd to the git root | `$COPILOT_HOME/skills`, `~/.agents/skills` (only with the default `COPILOT_HOME`), `COPILOT_SKILLS_DIRS`, `skillDirectories` | frontmatter `name` | first wins |
| Cursor CLI | cwd only: `.cursor`, `.claude`, `.codex`, `.grok`, `.agents` `/skills` | the same five under `$HOME` | directory name | project and user copies both reach the model; `/name` resolves to the user copy: every copy must be compatible |
| Gemini CLI | `.agents/skills`, `.gemini/skills` in cwd, **only in a trusted folder** | `${GEMINI_CLI_HOME:-$HOME}/.agents/skills`, `.gemini/skills`, extensions | frontmatter `name` | later tier loses |
| omp | `skills.customDirectories`, `.omp/skills` (cwd→git root), profile `agent/skills`, Claude user (opt-in), `.claude/skills`, `.agent(s)/skills` (cwd→git root, then `~`), `.codex/skills`, `.opencode/skills`, `.github/skills` (cwd) | profile-dependent only for `~/.omp[/profiles/<p>]/agent/skills` and `config.yml` | frontmatter `name` | the winner keeps the bare name; differing losers load as `<ns>/<name>` |

Settings agnthub honors as "disabled" (never reported satisfied): Claude `skillOverrides: {<dir>: "off"}` (user, project, local, managed settings); Codex `[[skills.config]]` `name`/`path` with `enabled = false` in `$CODEX_HOME/config.toml`; OpenCode `policies` denying `integration.use` on `skill:<glob>`, `permissions` rules and `permission.skill` denials, `OPENCODE_DISABLE_CLAUDE_CODE[_SKILLS]`, `OPENCODE_DISABLE_PROJECT_CONFIG`; Copilot `disabledSkills` and `ignoredSkillsLocations`; Gemini `skills.enabled: false`, `skills.disabled`, folder trust (`GEMINI_CLI_TRUST_WORKSPACE`, `trustedFolders.json`, `security.folderTrust.enabled`); omp `skills.enabled`, `disabledExtensions: [skill:<name>]`, `skills.ignoredSkills`, `skills.includeSkills`, `skills.enable{Pi,Claude,Agents,Codex}{User,Project}`, `disabledProviders`, `enabledProviders`, from the selected profile's `config.yml` plus the project `.omp/config.yml`, `.omp/settings.json`, `.claude/settings.json`. Cursor has no setting that disables a skill. Not modeled: plugin, marketplace, skillshare and managed skills; per-skill frontmatter switches (`enabled: false`, `user-invocable: false`, `paths`); Codex `project_root_markers`; JSONC comments in settings files (such files are ignored).

## Offline visibility surfaces used for proof

| Harness | Command (temporary `HOME`, cwd = project) |
| --- | --- |
| Claude Code | `claude -p --input-format stream-json --output-format stream-json --verbose --no-session-persistence` fed one `initialize` control request: lists skills with `(project)`/`(user)`; `claude -p --agent does-not-exist x` lists agents |
| Codex | `codex app-server` JSON-RPC `initialize`, `initialized`, `skills/list {"cwds":[proj],"forceReload":true}` |
| OpenCode 2 | `OPENCODE_PASSWORD=pw opencode serve --port N`, then `GET /api/skill?location[directory]=<proj>` (poll until populated); OpenCode 1: `opencode debug skill` |
| Copilot CLI | `COPILOT_AUTO_UPDATE=false copilot skill list --json` |
| Cursor CLI | none (every surface needs login); proof runs Cursor's own bundled discovery classes from the installed build in node |
| Gemini CLI | `gemini skills list` (`GEMINI_CLI_TRUST_WORKSPACE=true` for project skills) |
| omp | `omp [--profile <p>] skill list <proj> --json` |

## End-to-end results (agnthub + real skills.sh)

Fixtures: [`heynemann/agnthub-fixture-skills`](https://github.com/heynemann/agnthub-fixture-skills) (`v1`: `fixture-greeter` 1.2.0, `fixture-unversioned` without a version, `fixture-loose` `1.0`, `fixture-preview` `2.0.0-rc.1`) and `heynemann/agnthub-fixture-agents#skills-v1` (`reviewer` → agent `greeter` + `fixture-unversioned`; `greeter` → `fixture-greeter` `>=1.0.0 <=1.9.9`; `greeter-next`, `unversioned`, `loose`, `preview`).

- Project scope, all seven Harnesses, `--agent reviewer` (closure: two agents, two skills): every run installed both skills through `npx skills` pinned to `c1b4060`, verified them, then wrote both agents. Each Harness's own surface listed both skills from the installed path (Claude `(project)`; Codex `scope: repo`; OpenCode `/api/skill`; Copilot `source: project`; Gemini `[Enabled]` in a trusted folder; omp `agents:project`; Cursor model-facing list and `/fixture-greeter`). Claude also listed the `reviewer` and `greeter` agents.
- Global scope, all seven, `--agent greeter` (omp with `--extra omp.profile=hep`): each surface listed `fixture-greeter` from the global destination (Claude `(user)`, Codex `scope: user`, Copilot `personal-agents`, Gemini user tier, omp `agents:user` under `--profile hep`, Cursor `source= user`, OpenCode `~/.agents`).
- Refusals before any write: `greeter-next` (1.2.0 outside `>=2.0.0`), `unversioned` (no `metadata.version`), `loose` (`1.0` is not strict semver), Claude user copy 3.0.0 shadowing a compatible project copy, and an omp profile whose `config.yml` disables the skill while the default profile installs and `omp skill list` agrees (default lists it, `--profile hep` lists nothing). `preview` installs: its `2.0.0-rc.1` meets the explicit prerelease bound.
- Backend failures: a configured executable that creates a partial directory and exits 1, and one that exits 0 with tampered content, both ended `Failed`, wrote no agent file, reported the created skill directory, and left a pre-existing skill untouched.
