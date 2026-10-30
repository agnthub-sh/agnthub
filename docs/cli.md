# Using the agnthub CLI

Build and run the CLI from a clone of this repository (or use a [release](../README.md#install)):

```sh
make build
./bin/agnthub add ./path/to/source -l                   # list the source's agents (--list)
./bin/agnthub add ./path/to/source                      # choose agents and harnesses interactively
./bin/agnthub add ./path/to/source --agent reviewer --harness claude --harness codex
./bin/agnthub add owner/repo --agent '*' --harness claude --yes
./bin/agnthub add owner/repo --all --global --extra omp.profile=work
./bin/agnthub ls                                        # list (alias ls), -H claude to filter
./bin/agnthub check                                     # source changes and local drift, no writes
./bin/agnthub update reviewer                           # preview, approve, apply newer content (alias upgrade)
./bin/agnthub restore --yes --extra omp.profile=work    # recreate exactly what the lock records
./bin/agnthub rm                                        # choose installed agents to remove (alias rm)
./bin/agnthub remove reviewer --harness omp --extra omp.profile=work
./bin/agnthub remove --clean-dependencies
```

## Commands and flags

The command surface follows [skills.sh](https://skills.sh) (`npx skills`) where the agnthub contract allows, so the same habits work for agents:

| Command | Alias | What it does |
| --- | --- | --- |
| `add <source>` | `a` | Preview and install agents from a repository, Git URL or local directory |
| `list` | `ls` | List the agents agnthub installed (`-g` global, `-H <harness>` filter, `--json`) |
| `remove [agent...]` | `rm` | Remove installed agents; without names a terminal user picks them from a list |
| `check [agent...]` | | Show available updates and local drift without writing |
| `update [agent...]` | `upgrade` | Preview and apply newer content |
| `restore` | | Recreate exactly what `agnthub-lock.json` records |
| `completion <shell>` | | Print a bash, zsh, fish or PowerShell completion script |

Shared flags: `-g/--global` (user scope instead of the project), `-y/--yes` (no prompts), `--json` (machine-readable output, never prompts), `-a/--agent` (agent definitions), `-H/--harness` (harnesses; skills.sh target names such as `claude-code`, `github-copilot` and `gemini-cli` are accepted), `--extra <harness>.<key>=<value>`, `add -l/--list` and `add --all`. `--agent '*'` selects every agent of the source and `--harness '*'` every supported harness; `--all` is shorthand for `--agent '*' --harness '*' --yes`. `agnthub --help` and `agnthub <command> --help` show grouped commands, flags and examples; `agnthub --version` (or `-v`) prints the version.

Output: in a terminal, plans, tables, errors and help are styled (lipgloss/fang) and slow steps (fetching sources, resolving dependencies, running the skill installer) show a transient spinner on stderr. Redirected output, `NO_COLOR` and `TERM=dumb` give plain text without colors or spinners, errors become a single `error: ...` line on stderr, and `--json` prints only JSON on stdout.

Intentional differences from skills.sh:

- skills.sh's `--agent` names the coding tool to install into; agnthub installs *agents* into *harnesses*, so `-a/--agent` selects agent definitions (skills.sh's `--skill`) and `-H/--harness` the coding tools (skills.sh's `--agent`). `skills add owner/repo -s pr-review -a claude-code` becomes `agnthub add owner/repo -a reviewer -H claude`.
- Several values are given by repeating the flag (`-H claude -H codex`); skills.sh's space-separated lists (`--agent claude-code cursor`) are not accepted because an agnthub source argument could be mistaken for one.
- `--yes` never selects everything implicitly: with more than one agent it needs `--agent` (or `'*'`), and with no `--harness` it fails instead of guessing harnesses. Running inside an agent does not switch agnthub to non-interactive mode; only `--yes`, `--all`, `--json` or a non-terminal stdin/stdout do.
- `--yes`, `--all` and `--json` never reduce a plan: a blocked item fails the whole run before anything is written. skills.sh has no equivalent preview or refusal.
- agnthub writes agent files directly (no `--copy`/symlink choice) and records ownership, so `remove` deletes only unchanged files it installed; `remove --all` is not offered.
- `--extra <harness>.<key>=<value>` (e.g. `omp.profile=work`) has no skills.sh counterpart. `check` and `restore` (skills.sh's `experimental_install`) are first-class; skills.sh's `use`, `init`, `find` and `experimental_sync` have no agnthub command yet (search agents on [agnthub.sh](https://agnthub.sh)).

See the [skills.sh reference](research/skills-reference.md) for the full skills.sh command surface this was compared against.

## Behavior

Sources are explicit local directories (`./dir`), GitHub repositories (`owner/repo[#ref]`) or Git URLs. Harness settings use repeatable `--extra <harness>.<key>=<value>` (for example `--extra omp.profile=work`). The project lock is `agnthub-lock.json`; user-local ownership records live under `$XDG_STATE_HOME/agnthub/`.

In a terminal, `add` asks which agents and which harnesses to install into: every supported harness (Claude Code, OpenCode, Codex, GitHub Copilot CLI, Cursor, Gemini CLI, omp) is offered and the ones detected locally are preselected. It then previews the plan (each agent's Variant, native or generated, dependencies, scope, effective harness settings such as the omp profile, the absolute destination and notes) and writes only after you approve. If some items are blocked (a conflict with an unmanaged or locally edited file, an incompatible conversion, or a destination unavailable in the chosen scope), you may explicitly approve applying only the valid items; the skipped ones are listed. Scope is the project unless `--global` is given, and it is never switched for you. Set `ACCESSIBLE=1` (or `TERM=dumb`) for numbered line prompts instead of the terminal UI; redirected output is plain text.

`--yes`, `--all` and `--json` never prompt and never reduce a plan: any blocked item fails the whole run before anything is written. `--yes` needs an explicit `--agent` when the source has more than one agent. `--all` selects every agent and every supported harness and cannot be combined with `--agent`/`--harness`; `--all --extra omp.profile=work` applies the profile to omp only. `--json` prints only the machine-readable plan (status, scope, harnesses with detection and effective settings, items, summary) and needs `--yes` or `--all` to apply.

Git sources (`owner/repo#v1.2.0`, `https://host/team/agents.git#main`, `git@host:team/agents.git#<full-commit-sha>`) are fetched with the system `git` using your existing credentials (credential helpers, SSH agent); agnthub never stores or prints them. The ref (branch, tag, full commit SHA, or the default branch when omitted) is resolved once to a commit, which the lock records. A source fetched over SSH (`git@host:path` or `ssh://...`, including dependency sources) is locked with `"transport": "ssh"` and its credential-free clone URL, so `check`, `update` and `restore` refetch it over SSH; sources without a recorded transport (https, and locks written before transports were recorded) are refetched over https. Content is read straight from Git objects without a checkout, so repository filters, hooks and LFS never run; symlinks and submodules are skipped. Installing from a source never contacts the agnthub registry.

Agent Dependencies declared in `agnthub.yaml` (common, plus additions on a Harness Variant) install with the selection: the plan shows each companion with origin `dependency`, its pinned source and revision, and the agent that required it. Same-repository references use the selected snapshot; external references name a Git source (`owner/repo#ref` or a Git URL; never a local path), and each source/ref is fetched once. Companions use the same Harnesses, scope and Harness settings (such as `omp.profile`) as the direct selection and are locked under their own source. Cycles, missing or ambiguous agents, one agent required at two revisions, and incompatible companions refuse the plan before anything is written. A dependency is a presence requirement, not a promise of runtime delegation.

Skill Dependencies (`dependencies.skills: [{source, skill, minVersion?, maxVersion?}]`) are preflighted per Harness before anything runs. agnthub looks for a visible copy in that Harness's effective precedence order (in the same scope and Harness settings, e.g. the omp profile) and reuses a compatible one without taking ownership; bounds are inclusive and need a strict semantic `metadata.version` in the skill's own `SKILL.md` (missing or non-semver versions never satisfy a bound; prereleases need a prerelease bound). A higher-priority incompatible copy shadowing a compatible one, incompatible existing content, a skill the Harness configuration disables, or a requested source revision that does not satisfy the bounds refuses the plan. Missing skills install through the skill installer, pinned to the validated commit, and are then verified on disk (identity, exact source content, version, scope, and that the Harness would load that copy) before any dependent agent file is written. If the installer fails or verification fails, no agent file is written and the changes observed in skill locations are reported; agnthub does not roll back the installer or delete pre-existing skills. The lock records each verified skill (source, revision, version, digest, `installed` or `reused`); agnthub only owns skills its own installer run created.

The skill installer is [skills.sh](https://skills.sh) by default. Replace it in `~/.config/agnthub/config.yaml` (`$XDG_CONFIG_HOME/agnthub/config.yaml`); sources can never supply commands:

```yaml
skillInstaller:
  executable: npx            # a command in PATH or an absolute path; run without a shell
  args: [--yes, skills, add, "{source}", --skill, "{skill}", --agent, "{target}", --yes]
  projectArgs: []
  globalArgs: [--global]
```

`{source}` is the source pinned to the validated commit, `{skill}` the skill name and `{target}` the backend target the Harness adapter selects (`claude-code`, `codex`, `opencode`, `github-copilot`, `cursor`, `gemini-cli`; omp uses skills.sh's shared `universal` target, which writes `.agents/skills`). Each template stays one argument; unknown placeholders or invalid configuration fail before anything runs. When you opt out of sharing (below), the skill installer runs with `DO_NOT_TRACK=1` and `DISABLE_TELEMETRY=1`. See [skill dependency research](research/skill-dependencies.md) for each Harness's discovery locations and precedence.

Install reports: after an `add` or `update` that created or replaced files from a GitHub repository that an anonymous check shows is public, the CLI tells the registry (`https://api.agnthub.sh`; override with `AGNTHUB_API_URL` or `apiURL:` in `~/.config/agnthub/config.yaml`) the owner/repo, the installed commit and, per agent, its id, whether it was installed directly or as a dependency, the harness ids it placed and a random identity kept for that repository and agent in `$XDG_STATE_HOME/agnthub/identities.json`. The registry counts these reports only for agents of repositories it already lists; a report never adds a repository to the registry (repositories are submitted on the web app by a GitHub-signed-in user) and reports for unlisted repositories are ignored and not stored. Nothing else is sent: no paths, profiles or other harness settings, credentials, local, private or non-GitHub sources, or parent agents. Reporting is best-effort (at most a few seconds) and never changes the command's result; `restore` and no-op reinstalls report nothing. Set `DO_NOT_TRACK` or `DISABLE_TELEMETRY` to any non-empty value to send nothing at all.

Codex agents install as standalone TOML under `.codex/agents/` (project) or `$CODEX_HOME/agents/` (global, default `~/.codex/agents/`). Authored Codex Variants install byte-for-byte after schema validation; conversion writes `name`/`description`/`developer_instructions`, plus `features.shell_tool = false` when the agent withholds the shell. Codex has no per-agent tool allowlist or read-only mode, so an agent that withholds edits is incompatible with Codex. Variants that try to set parent-owned configuration (sandbox, approvals, provider, MCP) are refused, and `config.toml` is never modified.

Agents published only as one harness's native file (for example `.claude/agents/*.md`) install on the other harnesses by conversion: without an authored Variant for the target, agnthub converts the portable core, else another authored Variant (Claude Code first). Name, description and instructions always carry; tool allowlists translate by capability (read, search, edit, shell, web fetch/search, delegation) into each harness's own form (OpenCode `permission`, Copilot tool aliases, Gemini and omp tool names, Cursor `readonly`); model strings are never carried, so the converted agent uses the harness default model; cosmetic fields are omitted with a note. A target that cannot enforce what the agent declares (MCP tools, hooks, permission modes, approval prompts, delegation for Gemini, web search for Copilot CLI, or an exclusion it cannot withhold) is reported incompatible with the reason. See [ADR 0009](docs/adr/0009-cross-harness-conversion-through-shared-semantics.md).

`remove <agent>...` deletes only the selected agents' files that agnthub can prove it installed (an ownership record or the lock's digest) and that are unchanged since; a modified or replaced file is refused and left in place, unrelated native configuration is never touched, and the lock and ownership records are updated to match what actually happened (a failed deletion keeps its state and fails the run). `--harness` limits the harnesses and `--extra` selects the recorded binding: a file several bindings share (one project `.omp/agents` file installed for omp profiles `hep` and `work`) keeps the file and drops only the selected binding until its last binding goes. Several recorded bindings without `--extra` are asked about in a terminal and refused otherwise. An agent another remaining agent requires is refused. `--yes`/`--json` follow `add`: unattended runs apply only when every selected artifact can be removed.

Removing an agent never removes its companion agents or skills. `remove --clean-dependencies [name...]` is the separate, explicit cleanup: it removes a companion agent or a skill only when agnthub installed it (origin `dependency`, or a skill directory agnthub's skill installer created), it is unchanged, and no remaining agent in the same harness binding requires it. A skill directory also stays while another binding, another project's installation or another visible lock (the global lock, locks of projects this machine installed into) uses it. Reused skills (compatible copies that were already present) are never deleted: once no agent requires them they only leave the lock. Locks written before requirements were recorded cannot prove a dependency unused, so cleanup preserves it. Preserved items are reported with their reason. The skill installer's own files (for example skills.sh's `skills-lock.json`) are left alone.

`check [agent...]` resolves each locked agent's recorded ref (or default branch) to its current commit and compares, per Harness, what agnthub would install now with the lock: each agent is up to date, has an update available (content, Variant or converter change), is a new or no-longer-required dependency, or is unavailable (unreachable source or ref, or an agent the source no longer provides). Local drift (modified or missing files) and the recorded Harness bindings (such as the omp profile) are shown; `--json` prints the same report. Nothing is written.

`update [agent...]` (default: every direct agent) resolves the same newer content with its current dependency closure, previews content, Variant, dependency and skill changes plus the plan, and writes only after approval (`--yes` noninteractively). Locally modified or unmanaged files are never overwritten: the plan is refused (or, in a terminal, you may explicitly apply only the valid items). Dependencies the new revision no longer requires stay installed. Every artifact keeps the binding it was recorded with: an omp agent installed while profile `hep` was active is updated in `hep` even when another profile is active now, `--extra` naming a different binding is refused instead of retargeting, and a shared file with several recorded bindings needs `--extra` or a terminal choice.

`restore` recreates the installation the lock records, e.g. in a fresh clone: each agent is fetched at its locked commit, its source bytes must match the locked `sourceDigest`, it is regenerated with the recorded converter (an unsupported converter id/version is refused) and every artifact must equal its locked digest before anything is written; skills are installed through the skill installer pinned to their locked commit and verified against the locked digest. An unavailable commit, changed bytes or a mismatch is an error, never a reason to substitute newer content (`update` is the explicit way forward). Recorded bindings are reused; on a machine without them, Harnesses with settings need an explicit binding (`--extra omp.profile=work`, or a terminal choice). Restore never moves or deletes another profile's installation, never replaces a locally modified file and never replaces a shared file on behalf of a different binding. The lock never contains user-local state: absolute project paths, Harness settings and installation times stay in the ownership records.

Development: see [CONTRIBUTING](../CONTRIBUTING.md).
