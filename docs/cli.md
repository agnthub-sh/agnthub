# Using the agnthub CLI

Build and run the CLI from a clone of this repository (or use a [release](../README.md#install)):

```sh
make build
./bin/agnthub add ./path/to/source -l                   # list the source's agents (--list)
./bin/agnthub add ./path/to/source                      # choose agents and harnesses interactively
./bin/agnthub add ./path/to/source --agent reviewer --harness claude --harness codex
./bin/agnthub add owner/repo --agent '*' --harness claude --yes
./bin/agnthub add owner/repo --agent reviewer --harness all --yes
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
| `mcp` | | Serve agnthub to AI agents over the Model Context Protocol (stdio) |
| `completion <shell>` | | Print a bash, zsh, fish or PowerShell completion script |

Shared flags: `-g/--global` (user scope instead of the project), `-y/--yes` (no prompts), `--json` (machine-readable output, never prompts), `-a/--agent` (agent definitions), `-H/--harness` (harnesses; skills.sh target names such as `claude-code`, `github-copilot` and `gemini-cli` are accepted), `--extra <harness>.<key>=<value>`, `add -l/--list` and `add --all`. `--agent '*'` selects every agent of the source and `--harness all` (or `--harness '*'`) every supported harness; either cannot be combined with other values of the same flag. `--agent` has no `all` alias because an agent can be named `all`. `--all` is shorthand for `--agent '*' --harness all --yes`. `agnthub --help` and `agnthub <command> --help` show grouped commands, flags and examples; `agnthub --version` (or `-v`) prints the version.

Output: in a terminal, plans, tables, errors and help are styled (lipgloss/fang) and slow steps (fetching sources, resolving dependencies, running the skill installer) show a transient spinner on stderr. Redirected output, `NO_COLOR` and `TERM=dumb` give plain text without colors or spinners, errors become a single `error: ...` line on stderr, and `--json` prints only JSON on stdout.

Plans (`add`, `update`, `restore`, `remove`), their results, `check` and `list` print as tables. A plan starts with a status line (emoji, status, the source in bold with its `@commit` dimmed, and the scope as a cyan tag), followed by the harnesses with their settings such as the omp profile (settings and undetected harnesses dimmed). Then come one `AGENT  HARNESS  ACTION  PATH` row per file that changes (agents in bold, `+ create` green, `~ update` yellow, `- delete` red, the directory of each path dimmed), sorted by agent then harness, the notes under the table, refusals in a red section of their own (`AGENT  HARNESS  REASON`), and a totals line such as `12 to create · 0 to update · 0 to delete` with only non-zero counts colored. Everything under the status line shares one indent, the measured width of the status emoji and its space, so it lines up with the status text, and these blocks are separated by exactly one blank line (also before the prompt). Agent × harness pairs that need no action are not listed; a sentence under the table counts them (`16 agents were already correct in their harnesses, so no action was needed.`, dimmed). Reused skills are counted the same way under the skills table, and `check` lists only artifacts with an update or local drift. Paths are relative to the project inside it, `~/...` under your home directory and absolute elsewhere. A note shared by several agents (for example `converted from the claude variant`) prints once with the listed rows it applies to; a note about a single agent marks its rows with `[1]`, `[2]`... and is listed under the table; a note about a whole harness (omp's project directory shared by every profile) prints once in the header; notes of unlisted rows are dropped. When `add`, `update` or `restore` has nothing to do (no file to create or update, no skill to install, no receipt to record), there is no prompt: it prints `✅ Up to date`, the harnesses and the sentence in green, writes nothing and exits 0, with or without `--yes` and in or out of a terminal (`--json` is unchanged). After you approve a plan shown in the terminal, the result is only the status line (`✅ Installed · source@commit · scope`) and the totals, plus anything new such as skill installer runs or failures; without a preview (`--yes`, or no terminal to prompt in) the result prints the table once. Choosing Cancel prints a dimmed `Cancelled; nothing was written.` on stderr and exits 1. Emoji mark the status line (📋 plan, ✅ done or up to date, ❌ refused or failed) and the refusal section, never an aligned column. Plain output (redirected, `NO_COLOR`, `TERM=dumb`) has no escape sequences and no emoji, without ASCII stand-ins, and therefore no indent under the status line; the `+`, `~` and `-` action symbols stay. Tables fit the terminal width (or `COLUMNS`): paths are shortened in the middle with `…`, reasons wrap, and when even that does not fit each row stacks onto its own lines. Redirected output without `COLUMNS` is never shortened.

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

Without `agnthub.yaml`, a source's agents are its portable definitions (root `AGENT.md`, `agents/<id>.md`, `agents/<id>/AGENT.md`), the files in each harness's native agent directory, and the agents of the Claude Code plugins it packages: every `.claude-plugin/marketplace.json` entry whose `source` is a directory inside the repository, and the repository itself when it has a root `.claude-plugin/plugin.json`. A plugin's agents are the files its `agents` field lists (paths starting with `./`, inside the plugin) or, without that field, every `.md` file under its `agents/` directory; they are Claude Code Variants, and `--list` names the plugin each comes from. Plugin sources in other repositories (`github`, `url`, `git-subdir`, `npm`, `archive`, `command`) are listed as problems and never fetched. Claude Code ignores `permissionMode`, `hooks`, `mcpServers` and `initialPrompt` in plugin agents, so agnthub installs a plugin agent without them and the plan notes each omission; an agent whose instructions use `${CLAUDE_PLUGIN_ROOT}`, `${CLAUDE_PLUGIN_DATA}` or `${user_config.…}` needs its plugin and is refused. Two different definitions of one agent are ambiguous and refused until `agnthub.yaml` chooses; identical copies (one agent shipped in several plugins) are one agent. See [ADR 0011](adr/0011-claude-code-plugin-layouts.md) and [Claude Code plugins](research/claude-code-plugins.md).

In a terminal, `add` asks which agents and which harnesses to install into: every supported harness (Claude Code, OpenCode, Codex, GitHub Copilot CLI, Cursor, Gemini CLI, omp) is offered and the ones detected locally are preselected. It then previews the plan (a row per agent and harness with its destination, plus dependencies, scope, effective harness settings such as the omp profile, and notes; see Output above) and writes only after you approve. If some items are blocked (a conflict with an unmanaged or locally edited file, an incompatible conversion, or a destination unavailable in the chosen scope), you may explicitly approve applying only the valid items; the refused ones are listed with their reason. `--json` still reports each item's Variant (`native` or `generated`) and absolute path. Set `ACCESSIBLE=1` (or `TERM=dumb`) for numbered line prompts instead of the terminal UI; redirected output is plain text.

Scope is the project in the current directory unless `--global` is given. The one exception is your home directory (or any directory above it), which is no project: harnesses would apply project files there to projects below it or read them as user configuration (`~/.omp/agents` beside omp's user directory `~/.omp/agent/agents`, `~/.opencode/agents`, `~/.github/agents`; `~/.claude/agents` *is* Claude Code's user directory). Run there, `add` installs into user scope as `--global` does and prints a `note:` line on stderr saying so; `--global=false` (or `scope: project` over MCP) is refused instead. `list`, `check`, `update`, `restore` and `remove` run there use user scope too. A project lock agnthub 0.4.7 and earlier left in the home directory (`~/agnthub-lock.json`) is the exception: while it records installations, those commands use it with a `note:`, so `agnthub remove <name>` run in the home directory removes such an install and deletes the lock once it is empty; `update` and `restore` refuse to rewrite it. User-scope installs also refuse a relative `CLAUDE_CONFIG_DIR`, `CODEX_HOME`, `COPILOT_HOME` or `GEMINI_CLI_HOME`, which those harnesses resolve against the directory they run in. Where each harness loads agents from is in [Agent directories and project discovery](research/harness-agent-formats.md#agent-directories-and-project-discovery-runtime-verified-2026-11-04).

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

Agents published only as one harness's native file (for example `.claude/agents/*.md`) install on the other harnesses by conversion: without an authored Variant for the target, agnthub converts the portable core, else another authored Variant (Claude Code first). Name, description and instructions always carry; tool allowlists translate by capability (read, search, edit, shell, web fetch/search, delegation) into each harness's own form (OpenCode `permission`, Copilot tool aliases, Gemini and omp tool names, Cursor `readonly`); model strings are never carried, so the converted agent uses the harness default model; cosmetic fields are omitted with a note. A target that cannot enforce what the agent declares (MCP tools, hooks, permission modes, approval prompts, delegation for Gemini, web search for Copilot CLI, or an exclusion it cannot withhold) is reported incompatible with the reason. Persistent memory (`memory`) and preloaded skills (`skills`) only add to what an agent knows, so they never make a target incompatible. A turn limit (`maxTurns`) maps to OpenCode `steps` and Gemini CLI `max_turns`; on the other harnesses the agent installs with a short instruction to finish within about that many turns, and the plan notes the limit isn't enforced. Claude Code frontmatter keys Claude Code itself ignores are omitted with a note, unless they read as a tool or permission restriction (`allowed-tools`, `readonly`, `permissions`…), which still refuses. A field set to what omitting it does is omitted with a note rather than refused: `permissionMode: default` (or `manual`), and `hooks`, `mcpServers`, `isolation`, `initialPrompt`, `maxTurns` or an ignored restriction-like key left empty (`[]`, `{}`, blank, `false`). An empty allowlist (`tools: []`, `allowed-tools: []`) allows nothing and `hooks: []` keeps Claude Code from loading the agent, so neither counts as empty.

- **Preloaded skills** become Copilot CLI's `skills` or omp's `autoload-skills`. On the other harnesses, a short section appended to the instructions tells the agent which skills to load; OpenCode and Gemini CLI allowlists also gain their skill tool. agnthub never installs those skills.
- **Memory** is emulated the same way, with a section naming the agent's notes folder and its `MEMORY.md` index. Project and local scope use Claude Code's folders `.claude/agent-memory/<name>/` and `.claude/agent-memory-local/<name>/`. User scope uses `~/.claude/agent-memory/<name>/` on OpenCode and omp, and `~/.cursor/agent-memory/<name>/` on Cursor. Elsewhere, and for agents that cannot edit files, the plan notes that the agent starts each session without its notes.

See [ADR 0009](adr/0009-cross-harness-conversion-through-shared-semantics.md).

`remove <agent>...` deletes only the selected agents' files that agnthub can prove it installed (an ownership record or the lock's digest) and that are unchanged since; a modified or replaced file is refused and left in place, unrelated native configuration is never touched, and the lock and ownership records are updated to match what actually happened (a failed deletion keeps its state and fails the run). `--harness` limits the harnesses and `--extra` selects the recorded binding: a file several bindings share (one project `.omp/agents` file installed for omp profiles `hep` and `work`) keeps the file and drops only the selected binding until its last binding goes. Several recorded bindings without `--extra` are asked about in a terminal and refused otherwise. An agent another remaining agent requires is refused. `--yes`/`--json` follow `add`: unattended runs apply only when every selected artifact can be removed.

Removing an agent never removes its companion agents or skills. `remove --clean-dependencies [name...]` is the separate, explicit cleanup: it removes a companion agent or a skill only when agnthub installed it (origin `dependency`, or a skill directory agnthub's skill installer created), it is unchanged, and no remaining agent in the same harness binding requires it. A skill directory also stays while another binding, another project's installation or another visible lock (the global lock, locks of projects this machine installed into) uses it. Reused skills (compatible copies that were already present) are never deleted: once no agent requires them they only leave the lock. Locks written before requirements were recorded cannot prove a dependency unused, so cleanup preserves it. Preserved items are reported with their reason. The skill installer's own files (for example skills.sh's `skills-lock.json`) are left alone.

`check [agent...]` resolves each locked agent's recorded ref (or default branch) to its current commit and compares, per Harness, what agnthub would install now with the lock: each agent is up to date, has an update available (content, Variant or converter change), is a new or no-longer-required dependency, or is unavailable (unreachable source or ref, or an agent the source no longer provides). Local drift (modified or missing files) and the recorded Harness bindings (such as the omp profile) are shown; `--json` prints the same report. Nothing is written.

`update [agent...]` (default: every direct agent) resolves the same newer content with its current dependency closure, previews content, Variant, dependency and skill changes plus the plan, and writes only after approval (`--yes` noninteractively). Locally modified or unmanaged files are never overwritten: the plan is refused (or, in a terminal, you may explicitly apply only the valid items). Dependencies the new revision no longer requires stay installed. Every artifact keeps the binding it was recorded with: an omp agent installed while profile `hep` was active is updated in `hep` even when another profile is active now, `--extra` naming a different binding is refused instead of retargeting, and a shared file with several recorded bindings needs `--extra` or a terminal choice.

`restore` recreates the installation the lock records, e.g. in a fresh clone: each agent is fetched at its locked commit, its source bytes must match the locked `sourceDigest`, it is regenerated with the recorded converter (an unsupported converter id/version is refused) and every artifact must equal its locked digest before anything is written; skills are installed through the skill installer pinned to their locked commit and verified against the locked digest. An unavailable commit, changed bytes or a mismatch is an error, never a reason to substitute newer content (`update` is the explicit way forward). Recorded bindings are reused; on a machine without them, Harnesses with settings need an explicit binding (`--extra omp.profile=work`, or a terminal choice). Restore never moves or deletes another profile's installation, never replaces a locally modified file and never replaces a shared file on behalf of a different binding. The lock never contains user-local state: absolute project paths, Harness settings and installation times stay in the ownership records.

## MCP server

`agnthub mcp` runs a [Model Context Protocol](https://modelcontextprotocol.io) server on stdin/stdout so AI agents can find, inspect and install agents. MCP clients start it themselves; it is the same binary as the CLI (`npx -y agnthub-cli mcp`, or the installed `agnthub mcp`).

Read-only tools query the public registry API (`https://api.agnthub.sh`; override with `--api-url`, `AGNTHUB_API_URL` or `apiURL:` in `~/.config/agnthub/config.yaml`) and see exactly what the site shows: only currently listed repositories, instructions only under the licensed policy. Outputs are bounded (at most 20 search results per page, 50 agents per repository, instructions only on request and capped at 32 KiB each).

| Tool | What it does |
| --- | --- |
| `search_agents` | Full-text search with harness filters and `relevance`, `recent`, `installs`, `installs_30d`, `installs_7d`, `harness_installs` or `stars` (GitHub + agnthub.sh) order |
| `get_agent` | One agent: description, installable harnesses or why not, dependencies, reported installs, stars (GitHub + agnthub.sh), license; instructions with `include_instructions` |
| `get_repository` | A listed repository with its agents, license, GitHub stats and scan problems |
| `install_command` | The `npx agnthub-cli add …` command for agents, harnesses, scope and omp profile, built and quoted exactly as the site's install builder does (every supported harness selected becomes `--harness all`), after checking the agents are listed; warns about harnesses an agent cannot be installed into |
| `install_agents` | Installs on this machine through the same code as `agnthub add --json`: harness selection, scope, `--extra` settings such as `omp.profile`, dependencies, required skills, the lock and ownership records. Without `confirm` it only returns the plan; with `confirm: true` it applies the plan only when every item is valid, so files agnthub does not own or that were changed locally are never overwritten. Project installs go to `project_dir` (default: the directory the client started the server in). Install reports honor `DO_NOT_TRACK` / `DISABLE_TELEMETRY` |

Client configuration (use `agnthub mcp` instead of `npx -y agnthub-cli mcp` with an installed binary):

- Claude Code: `claude mcp add agnthub -- npx -y agnthub-cli mcp`
- Codex: `codex mcp add agnthub -- npx -y agnthub-cli mcp`, or in `~/.codex/config.toml`:

  ```toml
  [mcp_servers.agnthub]
  command = "npx"
  args = ["-y", "agnthub-cli", "mcp"]
  ```

- OpenCode (`opencode.json`):

  ```json
  { "mcp": { "agnthub": { "type": "local", "command": ["npx", "-y", "agnthub-cli", "mcp"] } } }
  ```

- Cursor (`.cursor/mcp.json` or `~/.cursor/mcp.json`), Gemini CLI (`.gemini/settings.json` or `~/.gemini/settings.json`) and omp (`.omp/mcp.json` or `~/.omp/agent/mcp.json`):

  ```json
  { "mcpServers": { "agnthub": { "command": "npx", "args": ["-y", "agnthub-cli", "mcp"] } } }
  ```

- GitHub Copilot CLI (`~/.copilot/mcp-config.json`):

  ```json
  { "mcpServers": { "agnthub": { "type": "local", "command": "npx", "args": ["-y", "agnthub-cli", "mcp"], "tools": ["*"] } } }
  ```

Development: see [CONTRIBUTING](../CONTRIBUTING.md).
