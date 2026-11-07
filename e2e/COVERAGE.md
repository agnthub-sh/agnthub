# End-to-end coverage

What the end-to-end suite proves for every agent type agnthub supports, in every Harness,
in each scope (project, global) and from each source kind (local directory, Git over
HTTPS), and what it does not prove, with the reason. The matrix below is generated from
the goldens by `go test ./e2e -run TestCoverage -update-coverage`; `TestCoverage` (part of
`go test ./...`) fails when a cell loses its evidence, when a step or Harness has no
golden, or when this file is stale. The scenarios and steps are declared in
[coverage.go](coverage.go).

## Agent-type taxonomy

The CLI's own model (packages `discovery`, `harness`, `install`, `resolve`, `skills`;
[CLI contract](../docs/cli-contract.md)), which the scenarios cover:

| Dimension | What exists |
|---|---|
| Portable layouts | root `AGENT.md` (needs `name`), `agents/<id>.md`, `agents/<id>/AGENT.md`; `AGENTS.md`/`README.md` are not agents. Portable core = `name`, `description`, body; descriptive metadata (`version`, `tags`, `author`, `license`, `homepage`, `metadata`) is noted and omitted; other keys are read as Claude Code reads them (`model` kept on Claude Code and noted elsewhere, `tools` the tool policy, `color` omitted), and keys that read as restrictions Claude Code ignores refuse. |
| Native layouts | `.claude/agents/**/*.md` (recursive, lenient YAML), `.opencode/agents/*.md` (id = file name), `.codex/agents/*.toml`, `.github/agents/*.agent.md` and `*.md` (id = file name), `.cursor/agents/*.md` (one-line keys), `.gemini/agents/*.md`, `.omp/agents/*.md`. |
| Manifest | `agnthub.yaml` is the whole inventory: declared ids, `categories`, `portable`, per-Harness `variants` (any path), common and Variant-only `dependencies.agents` (same repository or external Git source with ref) and `dependencies.skills` (source, skill, version bounds). |
| Variant selection | An authored Variant for the target installs byte for byte after native validation; otherwise the portable core is converted, else another authored Variant (claude, opencode, codex, copilot, cursor, gemini, omp order). An invalid Variant for the target is refused, never replaced by a conversion. |
| Conversion | Name, description and instructions carry; tool allowlists, denylists, read-only, MCP tool references and `maxTurns` map by capability into each Harness's form or are refused when the target cannot enforce them (a whole-tool rule such as `WebFetch(*)` is the plain tool); a capability the target cannot grant (Copilot CLI web search, Gemini CLI delegation) is withheld and shown as `reduced`; an agent that would lose the shell it uses (Cursor `readonly`, scoped `Bash(...)` rules outside OpenCode, `permissionMode: plan`) installs only with `--allow-without-shell`; `isolation: worktree` is native on Claude Code and elsewhere an instruction to work in a git worktree (`reduced`), refused for an agent that edits without a shell; Copilot CLI `disable-model-invocation` is native on Copilot CLI, `mode: primary` on OpenCode and a description request elsewhere (`reduced`); Codex `sandbox_mode = "read-only"` withholds edits elsewhere and is refused on Codex; background agents run in the foreground with a note; model strings are dropped with a note; cosmetic fields are noted; agent-scoped MCP servers carry only to Gemini CLI's `mcp_servers`; `permissionMode: dontAsk` and hooks with real events are refused; keys Claude Code ignores are noted, unless they read as restrictions (refused; `allowed-tools` is the allowlist elsewhere); persistent memory and preloaded skills never refuse: native field, delimited instructions, or a note. |
| Identity | Ambiguous identities (two portable files, two native files of one Harness) refuse that agent only. |
| Selection | `--agent`/`'*'`, `--harness`/`all`, `--all`, `--global`, `--yes` (never reduces a plan), `--allow-without-shell` (installs agents that lose the shell they use), `--extra omp.profile=<name>` (omp only; refused for unselected Harnesses; profiles must exist). |
| Lifecycle | `list`, `check` (updates, drift), `update` (locked ref, refuses local edits), `restore` (lock only, byte-identical, refuses mismatches), `remove` (owned and unchanged files only), `remove --clean-dependencies`. |
| Dependencies | Agent Dependencies install with the selection (origin `dependency`); Skill Dependencies install through skills.sh into the Harness's skill directory, refused where the Harness would not load them (Gemini CLI untrusted project). |

## How each Harness is asked whether it loaded the agents (`N`)

After every install step the real Harness CLI of the image, offline and without a login,
reports which agents it loaded; an agent counts only when it is the file agnthub wrote
(Gemini CLI, omp and Cursor: the listed description must be the installed one, so a
built-in or another file of the same name does not count).

| Harness | Real CLI command |
|---|---|
| claude | `claude -p --agent <unknown>` lists every discovered agent before any model call |
| opencode | `opencode debug agents` (resolved agents of its service) |
| codex | `codex debug prompt-input` renders the model input; custom agents are spawn roles (project trusted) |
| copilot | `copilot -p --agent <unknown>` names the available agents (BYOK provider never contacted) |
| gemini | `gemini -p` with an API key against a local model stub: the first request's system instruction lists the registered subagents. Project agents are acknowledged first, as Gemini CLI's own prompt would (it registers a project agent only after the user acknowledged that file's SHA-256). Any `Agent loading error` fails the check. |
| omp | `omp -p --model stub/stub-model` (a `models.yml` provider pointing at the stub, in the step's omp profile): its `task` tool lists every discovered agent |
| cursor | `cursor-agent-local --authless -p` (the same release's agent-cli-local build, installed beside `cursor-agent`; every `cursor-agent` command needs a signed-in account) against the stub: its `Task` tool lists the workspace subagents. User scope: the check asserts Cursor's CLI still does **not** load `~/.cursor/agents` (`N0`). |

The model stub (`modelstub_test.go`) answers on the container's loopback; the suite runs
with `--network none`, so no check reaches a real model or account.

## Proof level per Harness

| Harness | Pinned version | Load proof (every install step) | Live run (`make e2e-live`, 2026-11-02) |
|---|---|---|---|
| claude | Claude Code 2.1.309 | real CLI, project and user scope | ran `live-probe` through OpenRouter (`anthropic/claude-haiku-4.5`) |
| opencode | OpenCode 2.1.3 | real CLI, project and user scope | ran `live-probe` through OpenRouter (`anthropic/claude-haiku-4.5`) |
| codex | Codex 0.164.0 | real CLI, project and user scope | main agent spawned the `live-probe` role through OpenRouter (`openai/gpt-5-mini`) |
| copilot | Copilot CLI 1.0.105 | real CLI, project and user scope | ran `live-probe` with OpenRouter as BYOK provider (`anthropic/claude-haiku-4.5`) |
| cursor | cursor-agent 2026.10.22-27dcf5f | real Cursor CLI build (agent-cli-local of the same release), project scope; user scope proven not loaded (`N0`) | not run: needs `CURSOR_API_KEY` (cursor-agent has no keyless headless mode; agent-cli-local ignores subagent instructions) |
| gemini | Gemini CLI 0.66.0 | real CLI headless against the stub, project (acknowledged) and user scope | not run: needs `GEMINI_API_KEY` (Gemini CLI speaks only the Gemini API) |
| omp | omp 18.13.3 | real CLI headless against the stub, project, user and named-profile scope | main agent ran `live-probe` via `task` through OpenRouter (`anthropic/claude-haiku-4.5`) |

## Isolation

- Each image holds one Harness: `TestHarness/image-isolation` fails if another Harness's
  CLI is on `PATH`, another Harness's installation exists, or any Harness configuration
  directory exists in the image's home.
- Every step runs in an empty project and home. The golden of each step lists every file
  written anywhere in the project and home (`files.txt`, with modes and bytes), and
  `requireOwnDirectories` fails any step selecting one Harness that writes outside that
  Harness's agent/skill directories and agnthub's own lock, configuration and state.
- Refusals, plans, inventories, `list` and `check` must leave the project and home
  byte-identical (`requireUnchanged`).

## Matrix

Cell codes: `W` the golden shows the agent written (files, lock and ownership byte for byte)
in every scope and source of the row; `R` the golden shows it refused and nothing written;
`O` the plan refuses it only because it would lose the shell access it uses, the refusal
golden shows it refused without the opt-in, and `install-layouts-without-shell` writes it
with `--allow-without-shell` (project · local);
`A` the goldens show the agent is not installed for that Harness (not part of its
selection); `✓` the step's golden output (inventory, selection, lifecycle) with exit status
0; `N` the real Harness CLI loaded what the step installed; `N0` the real CLI provably does
not load that scope (see Exclusions). Scope · source lists where the row's steps run.

<!-- coverage:begin (generated by go test ./e2e -run TestCoverage -update-coverage) -->

### Discovery layouts

| Scenario | Fixture agent | Scope · source | claude | opencode | codex | copilot | cursor | gemini | omp |
|---|---|---|---|---|---|---|---|---|---|
| root `AGENT.md` (portable) | `root-helper` | global · git<br>global · local<br>project · git<br>project · local | W N | W N | W N | W N | W N N0 | W N | W N |
| `agents/<id>.md` (portable) | `planner` | global · git<br>global · local<br>project · git<br>project · local | W N | W N | W N | W N | W N N0 | W N | W N |
| `agents/<id>/AGENT.md` (portable) | `researcher` | global · git<br>global · local<br>project · git<br>project · local | W N | W N | W N | W N | W N N0 | W N | W N |
| `.claude/agents/*.md` (Claude Code native) | `reviewer` | global · git<br>global · local<br>project · git<br>project · local | W N | W N | R | W N | O N | W N | W N |
| `.claude/agents/<dir>/*.md` (recursive) | `fixer` | global · git<br>global · local<br>project · git<br>project · local | W N | W N | W N | W N | W N N0 | W N | W N |
| `.opencode/agents/*.md` (OpenCode native, CRLF) | `oc-native` | global · git<br>global · local<br>project · git<br>project · local | W N | W N | R | W N | W N N0 | W N | W N |
| `.codex/agents/*.toml` (Codex TOML) | `cx-native` | global · git<br>global · local<br>project · git<br>project · local | W N | W N | W N | W N | R | W N | W N |
| `.github/agents/*.agent.md` (Copilot CLI) | `gh-native` | global · git<br>global · local<br>project · git<br>project · local | W N | W N | R | W N | W N N0 | W N | W N |
| `.github/agents/*.md` (Copilot CLI, plain suffix) | `gh-plain` | global · git<br>global · local<br>project · git<br>project · local | W N | W N | R | W N | W N N0 | W N | W N |
| `.cursor/agents/*.md` (Cursor, `readonly`) | `cu-native` | global · git<br>global · local<br>project · git<br>project · local | W N | W N | R | W N | W N N0 | W N | W N |
| `.gemini/agents/*.md` (Gemini CLI) | `gm-native` | global · git<br>global · local<br>project · git<br>project · local | W N | W N | R | W N | W N N0 | W N | W N |
| `.omp/agents/*.md` (omp) | `omp-native` | global · git<br>global · local<br>project · git<br>project · local | W N | W N | R | W N | W N N0 | W N | W N |
| `AGENTS.md` and `README.md` are not agents (inventory) | — | inventory · local | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |

### agnthub.yaml manifest

| Scenario | Fixture agent | Scope · source | claude | opencode | codex | copilot | cursor | gemini | omp |
|---|---|---|---|---|---|---|---|---|---|
| declared ids are the inventory (undeclared agent left out), categories | — | inventory · git | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| declared Variants (OpenCode, Codex) with a portable core | `lead` | global · git<br>project · git<br>project · local | W N | W N | W N | W N | W N N0 | W N | W N |
| agent declared only as a Claude Code Variant | `native-only` | global · git<br>project · git<br>project · local | W N | W N | R | W N | W N N0 | W N | W N |
| same-repository Agent Dependency | `helper` | global · git<br>project · git<br>project · local | W N | W N | W N | W N | W N N0 | W N | W N |
| Agent Dependency from another Git repository | `companion` | global · git<br>project · git<br>project · local | W N | W N | W N | W N | W N N0 | W N | W N |
| dependency only the Codex Variant adds (elsewhere provably not installed) | `codex-helper` | global · git<br>project · git<br>project · local | A | A | W N | A | A | A | A |
| Skill Dependency installed by skills.sh into the Harness's skill directory | — | global · git<br>project · git<br>project · local | ✓ N | ✓ N | ✓ N | ✓ N | ✓ N N0 | ✓ R N | ✓ N |
| every declared Variant invalid: refused, never falls back to the portable core | `strict` | global · git<br>project · git<br>project · local | R | R | R | R | R | R | R |

### Claude Code plugins

| Scenario | Fixture agent | Scope · source | claude | opencode | codex | copilot | cursor | gemini | omp |
|---|---|---|---|---|---|---|---|---|---|
| marketplace inventory names each agent's plugin; a remote source and one escaping the repository are reported, never read | — | inventory · git | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| agent listed in a plugin's `plugin.json` `agents` array | `mk-lister` | global · git<br>project · local | W N | W N | W N | W N | W N N0 | W N | W N |
| agent in a plugin's default `agents/` directory | `mk-scanner` | global · git<br>project · local | W N | W N | W N | W N | W N N0 | W N | W N |
| agent in a subfolder of a plugin's `agents/` directory | `mk-nested` | global · git<br>project · local | W N | W N | W N | W N | W N N0 | W N | W N |
| `permissionMode` and `hooks`, which Claude Code ignores in plugins: omitted with a note | `mk-guarded` | global · git<br>project · local | W N | W N | W N | W N | W N N0 | W N | W N |
| instructions that use `${CLAUDE_PLUGIN_ROOT}` need their plugin: refused | `mk-rooted` | global · git<br>project · local | R | R | R | R | R | R | R |

### Sources

| Scenario | Fixture agent | Scope · source | claude | opencode | codex | copilot | cursor | gemini | omp |
|---|---|---|---|---|---|---|---|---|---|
| repository too large to fetch whole (20 MiB of other files, a 4 MiB binary): listed and installed from its trees and candidate files, under 1 MiB fetched | `large-probe` | inventory · git<br>project · git | W ✓ N | W ✓ N | W ✓ N | W ✓ N | W ✓ N | W ✓ N | W ✓ N |

### Variants and conversion

| Scenario | Fixture agent | Scope · source | claude | opencode | codex | copilot | cursor | gemini | omp |
|---|---|---|---|---|---|---|---|---|---|
| authored native Variant wins over conversion (portable + Claude + OpenCode) | `dual` | global · git<br>global · local<br>project · git<br>project · local | W N | W N | W N | W N | W N N0 | W N | W N |
| invalid native Variant for the target: refused, never converted instead | `broken` | global · git<br>global · local<br>project · git<br>project · local | R | R | R | R | R | R | R |
| portable core with descriptive metadata (noted, omitted) | `tagged` | global · git<br>global · local<br>project · git<br>project · local | W N | W N | W N | W N | W N N0 | W N | W N |
| portable core written for Claude Code (`model`, `tools`, `color`): read as a Claude Code agent | `claude-shaped` | global · git<br>global · local<br>project · git<br>project · local | W N | W N | R | W N | W N N0 | W N | W N |
| Claude-only `permissionMode: plan`: native on Claude Code, read-only elsewhere (no edits or shell) with `--allow-without-shell`, refused on Codex | `guarded` | global · git<br>global · local<br>project · git<br>project · local | W N | O N | R | O N | O N | O N | O N |
| `permissionMode: acceptEdits`, which only approves more: native on Claude Code, dropped with a note elsewhere (the harness asks for approval as usual) | `relaxed` | global · git<br>global · local<br>project · git<br>project · local | W N | W N | W N | W N | W N N0 | W N | W N |
| `hooks` whose events Claude Code ignores (`pre`, `post`): native on Claude Code, omitted with a note elsewhere | `scripted` | global · git<br>global · local<br>project · git<br>project · local | W N | W N | W N | W N | W N N0 | W N | W N |
| Copilot CLI Variant with keys Copilot CLI ignores (`color`, `title`): installed as written with a note | `gh-colored` | global · git<br>global · local<br>project · git<br>project · local | W N | W N | R | W N | W N N0 | W N | W N |
| Cursor Variant with keys Cursor ignores (`category`, `color`): installed as written with a note | `cu-tagged` | global · git<br>global · local<br>project · git<br>project · local | W N | W N | R | W N | W N N0 | W N | W N |
| Codex `nickname_candidates`: native on Codex, omitted with a note elsewhere | `cx-nicknamed` | global · git<br>global · local<br>project · git<br>project · local | W N | W N | W N | W N | R | W N | W N |
| Codex `sandbox_mode = "read-only"`: refused on Codex (it runs agents in the parent session's sandbox), edits withheld elsewhere | `cx-sandboxed` | global · git<br>global · local<br>project · git<br>project · local | W N | W N | R | W N | O N | W N | W N |
| Codex `sandbox_mode = "workspace-write"`: installed on Codex with a note (the parent session's sandbox applies), omitted with a note elsewhere | `cx-workspace` | global · git<br>global · local<br>project · git<br>project · local | W N | W N | W N | W N | W N N0 | W N | W N |
| Codex `features.multi_agent = false`: installed on Codex with a warning (not enforced there), delegation withheld elsewhere | `cx-solo` | global · git<br>global · local<br>project · git<br>project · local | W N | W N | W N | W N | W N N0 | W N | W N |
| Claude Code fields at their default or empty (`permissionMode: default`, `disallowedTools: []`, `hooks: {}`, `mcpServers: []`): native on Claude Code, omitted with a note elsewhere | `defaulted` | global · git<br>global · local<br>project · git<br>project · local | W N | W N | W N | W N | W N N0 | W N | W N |
| `isolation: worktree` with a shell: native on Claude Code, elsewhere told to work only in a git worktree it creates (`reduced`: isolation) | `isolated` | global · git<br>global · local<br>project · git<br>project · local | W N | W N | W N | W N | W N N0 | W N | W N |
| `isolation: worktree` with edits but no shell: native on Claude Code, refused elsewhere (it cannot create a worktree) | `isolated-editor` | global · git<br>global · local<br>project · git<br>project · local | W N | R | R | R | R | R | R |
| Copilot CLI `disable-model-invocation: true`: native on Copilot CLI, OpenCode `mode: primary`, elsewhere a description asking delegating models not to pick it (`reduced`: manual-only invocation) | `gh-manual` | global · git<br>global · local<br>project · git<br>project · local | W N | W N | W N | W N | W N N0 | W N | W N |

### Tool restrictions

| Scenario | Fixture agent | Scope · source | claude | opencode | codex | copilot | cursor | gemini | omp |
|---|---|---|---|---|---|---|---|---|---|
| allowlist Read/Grep/Glob/Bash (Cursor: `readonly`, without the shell, with `--allow-without-shell`) | `reviewer` | global · git<br>global · local<br>project · git<br>project · local | W N | W N | R | W N | O N | W N | W N |
| whole-tool rules `WebFetch(*)`, `Bash(*)`: read as `WebFetch`, `Bash` | `wildcard` | global · git<br>global · local<br>project · git<br>project · local | W N | W N | W N | W N | W N N0 | W N | W N |
| allowlist with edit and shell | `fixer` | global · git<br>global · local<br>project · git<br>project · local | W N | W N | W N | W N | W N N0 | W N | W N |
| read-only allowlist (Read, Grep) | `colon-native` | global · git<br>global · local<br>project · git<br>project · local | W N | W N | R | W N | W N N0 | W N | W N |
| OpenCode `permission` edit/bash deny | `oc-native` | global · git<br>global · local<br>project · git<br>project · local | W N | W N | R | W N | W N N0 | W N | W N |
| Cursor `readonly: true` | `cu-native` | global · git<br>global · local<br>project · git<br>project · local | W N | W N | R | W N | W N N0 | W N | W N |
| Codex `features.shell_tool = false` | `cx-native` | global · git<br>global · local<br>project · git<br>project · local | W N | W N | W N | W N | R | W N | W N |
| denylist (`disallowedTools: Bash`) | `no-shell` | global · git<br>global · local<br>project · git<br>project · local | W N | W N | W N | W N | R | W N | W N |
| MCP tool reference (`mcp__github__get_issue`) | `issue-reader` | global · git<br>global · local<br>project · git<br>project · local | W N | W N | R | W N | R | W N | W N |
| whole MCP server (`mcp__github__*`) | `mcp-wide` | global · git<br>global · local<br>project · git<br>project · local | W N | W N | R | R | R | W N | W N |
| web fetch and web search (Copilot CLI: installed without web search, `reduced`) | `web-searcher` | global · git<br>global · local<br>project · git<br>project · local | W N | W N | R | W N | R | W N | W N |
| scoped shell rule `Bash(git diff *)` with edits: OpenCode `permission.bash`, shell withheld elsewhere with `--allow-without-shell` (Cursor: refused) | `differ` | global · git<br>global · local<br>project · git<br>project · local | W N | W N | O N | O N | R | O N | O N |
| allowlist names with no equivalent (`read`, `ListMcpResourcesTool`, `playwright`): left out with a note | `misnamed` | global · git<br>global · local<br>project · git<br>project · local | W N | W N | W N | W N | W N N0 | W N | W N |
| delegation (`Agent`; Gemini CLI: installed without it, `reduced`) | `delegator` | global · git<br>global · local<br>project · git<br>project · local | W N | W N | R | W N | W N N0 | W N | W N |
| turn limit (`maxTurns`) | `bounded` | global · git<br>global · local<br>project · git<br>project · local | W N | W N | W N | W N | W N N0 | W N | W N |
| background execution (`background: true`): in the foreground with a note where the Harness has no background agents | `backgrounder` | global · git<br>global · local<br>project · git<br>project · local | W N | W N | W N | W N | W N N0 | W N | W N |
| Gemini `mcp_servers` + `mcp_<server>_<tool>`: native on Gemini CLI | `gm-mcp` | global · git<br>global · local<br>project · git<br>project · local | R | R | R | R | R | W N | R |
| Gemini `mcpServers` (camelCase), which Gemini CLI rejects: refused | `gm-camel` | global · git<br>global · local<br>project · git<br>project · local | R | R | R | R | R | R | R |
| agent-scoped `mcpServers`: native on Claude Code, Gemini CLI `mcp_servers`, refused elsewhere | `mcp-owner` | global · git<br>global · local<br>project · git<br>project · local | W N | R | R | R | R | W N | R |

### Additive capabilities

| Scenario | Fixture agent | Scope · source | claude | opencode | codex | copilot | cursor | gemini | omp |
|---|---|---|---|---|---|---|---|---|---|
| `memory: project` with edits: Claude Code's folder named in the instructions | `rememberer` | global · git<br>global · local<br>project · git<br>project · local | W N | W N | W N | W N | W N N0 | W N | W N |
| `memory: user` with edits: emulated where agents may write under the home directory, noted elsewhere | `journaler` | global · git<br>global · local<br>project · git<br>project · local | W N | W N | W N | W N | W N N0 | W N | W N |
| `memory: local` with inherited tools | `local-notes` | global · git<br>global · local<br>project · git<br>project · local | W N | W N | W N | W N | W N N0 | W N | W N |
| `memory: user` without edits: noted, never emulated or granted | `recaller` | global · git<br>global · local<br>project · git<br>project · local | W N | W N | R | W N | W N N0 | W N | W N |
| `skills` preload: native `skills`/`autoload-skills`, else instructions plus the skill tool | `skilled` | global · git<br>global · local<br>project · git<br>project · local | W N | W N | W N | W N | W N N0 | W N | W N |

### Variants and conversion

| Scenario | Fixture agent | Scope · source | claude | opencode | codex | copilot | cursor | gemini | omp |
|---|---|---|---|---|---|---|---|---|---|
| keys Claude Code ignores (`capabilities`, `category`, `domain`): omitted with a note | `annotated` | global · git<br>global · local<br>project · git<br>project · local | W N | W N | W N | W N | W N N0 | W N | W N |
| `allowed-tools`, which Claude Code ignores in agents: kept with a warning on Claude Code, the tool allowlist elsewhere | `allowlisted` | global · git<br>global · local<br>project · git<br>project · local | W N | W N | R | W N | W N N0 | W N | W N |

### Additive capabilities

| Scenario | Fixture agent | Scope · source | claude | opencode | codex | copilot | cursor | gemini | omp |
|---|---|---|---|---|---|---|---|---|---|
| memory, skills and `maxTurns` together (the qa-lead shape) | `studio-qa` | global · git<br>global · local<br>project · git<br>project · local | W N | W N | W N | W N | W N N0 | W N | W N |

### Model fields

| Scenario | Fixture agent | Scope · source | claude | opencode | codex | copilot | cursor | gemini | omp |
|---|---|---|---|---|---|---|---|---|---|
| native `model: sonnet` kept byte-for-byte, dropped with a note on conversion | `reviewer` | global · git<br>global · local<br>project · git<br>project · local | W N | W N | R | W N | O N | W N | W N |
| portable `model`, read as Claude Code's: kept on Claude Code, dropped with a note elsewhere | `modelled` | global · git<br>global · local<br>project · git<br>project · local | W N | W N | W N | W N | W N N0 | W N | W N |

### Frontmatter edge cases

| Scenario | Fixture agent | Scope · source | claude | opencode | codex | copilot | cursor | gemini | omp |
|---|---|---|---|---|---|---|---|---|---|
| unquoted colon in a portable description | `colon` | global · git<br>global · local<br>project · git<br>project · local | W N | W N | W N | W N | W N N0 | W N | W N |
| unquoted colon in a Claude Code Variant | `colon-native` | global · git<br>global · local<br>project · git<br>project · local | W N | W N | R | W N | W N N0 | W N | W N |
| CRLF portable core | `researcher` | global · git<br>global · local<br>project · git<br>project · local | W N | W N | W N | W N | W N N0 | W N | W N |
| CRLF native Variant (installed with its CRLF) | `oc-native` | global · git<br>global · local<br>project · git<br>project · local | W N | W N | R | W N | W N N0 | W N | W N |
| UTF-8 BOM portable core | `bom` | global · git<br>global · local<br>project · git<br>project · local | W N | W N | W N | W N | W N N0 | W N | W N |
| UTF-8 BOM Claude Code Variant | `bom-native` | global · git<br>global · local<br>project · git<br>project · local | W N | W N | R | W N | R | W N | W N |
| non-ASCII description and body | `planner` | global · git<br>global · local<br>project · git<br>project · local | W N | W N | W N | W N | W N N0 | W N | W N |
| folded description (`>-`) | `researcher` | global · git<br>global · local<br>project · git<br>project · local | W N | W N | W N | W N | W N N0 | W N | W N |
| literal block description (`|`) | `literal` | global · git<br>global · local<br>project · git<br>project · local | W N | W N | W N | W N | W N N0 | W N | W N |
| description with a colon and both quote characters: one quoted line on Cursor | `quoted` | global · git<br>global · local<br>project · git<br>project · local | W N | W N | W N | W N | W N N0 | W N | W N |
| Cursor Variant with a multi-line description: folded onto one line | `cu-folded` | global · git<br>global · local<br>project · git<br>project · local | W N | W N | R | W N | W N N0 | W N | W N |

### Ambiguous identities

| Scenario | Fixture agent | Scope · source | claude | opencode | codex | copilot | cursor | gemini | omp |
|---|---|---|---|---|---|---|---|---|---|
| two portable files, one id: refused | — | inventory · local<br>project · local | ✓ R | ✓ R | ✓ R | ✓ R | ✓ R | ✓ R | ✓ R |
| two Claude Code files (recursive), one id: refused | — | project · local | R | R | R | R | R | R | R |
| an unambiguous agent of the same source installs | `solo` | project · local | W N | W N | W N | W N | W N | W N | W N |

### Selection

| Scenario | Fixture agent | Scope · source | claude | opencode | codex | copilot | cursor | gemini | omp |
|---|---|---|---|---|---|---|---|---|---|
| `--agent '*'` selects every agent of the source | — | project · local | ✓ N | ✓ N | ✓ N | ✓ N | ✓ N | ✓ N | ✓ N |
| `--harness all` installs into every Harness (detection: only this one) | — | project · local | ✓ N | ✓ N | ✓ N | ✓ N | ✓ N | ✓ N | ✓ N |
| `--all` = every agent, every Harness, no prompt | — | project · local | ✓ N | ✓ N | ✓ N | ✓ N | ✓ N | ✓ N | ✓ N |
| `--yes` never reduces a plan: one refused agent fails the run | — | project · local | R | R | R | R | R | R | R |
| `--extra omp.profile`: omp profile destinations; other Harnesses refuse it | — | global · local<br>project · local | R | R | R | R | R | R | ✓ R N |

### Lifecycle

| Scenario | Fixture agent | Scope · source | claude | opencode | codex | copilot | cursor | gemini | omp |
|---|---|---|---|---|---|---|---|---|---|
| `list` shows installed agents and bindings | — | project · git | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| `check`: up to date | — | project · git | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| `check`: local drift (modified file) | — | project · git | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| `update`: newer revision of the locked ref | — | project · git | ✓ N | ✓ N | ✓ N | ✓ N | ✓ N | ✓ N | ✓ N |
| `update` refuses to overwrite a locally modified file | — | project · git | R | R | R | R | R | R | R |
| `restore` recreates the lock's files byte for byte (omp: a machine without recorded bindings must name the profile) | — | project · git | ✓ N | ✓ N | ✓ N | ✓ N | ✓ N | ✓ N | ✓ R N |
| `restore` refuses when a locked file was modified | — | project · git | R | R | R | R | R | R | R |
| `remove` deletes only owned files (unmanaged neighbor kept) | — | project · git | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| `remove` refuses a modified file | — | project · git | R | R | R | R | R | R | R |
| `remove --clean-dependencies` removes unused companions and skills | — | project · git | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| user scope: `add -g`, `list -g`, `check -g`, `remove -g` | — | global · git | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |

### Home directory

| Scenario | Fixture agent | Scope · source | claude | opencode | codex | copilot | cursor | gemini | omp |
|---|---|---|---|---|---|---|---|---|---|
| `add` in the home directory (no project) installs into user scope, which the Harness loads | — | global · local | ✓ N | ✓ N | ✓ N | ✓ N | ✓ N0 | ✓ N | ✓ N |
| `add --global=false` in the home directory is refused; nothing is written | — | project · local | R | R | R | R | R | R | R |
| a project install agnthub 0.4.7 made in the home directory: listed and removed there | — | project · local | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |

### Exclusions

- native check, cursor × global scope (`N0`): cursor-agent loads only workspace agents (.cursor/.claude/.grok agents of the project); agnthub installs user agents where Cursor documents them (~/.cursor/agents, read by the Cursor editor). The native check runs the real CLI and asserts it still does not load them, so a Cursor release that starts loading user agents fails the nightly run and removes this exclusion.

### Live tier

| Harness | Secret (first set wins) | How the installed agent is run |
|---|---|---|
| claude | `OPENROUTER_API_KEY` | `claude -p --agent live-probe` through OpenRouter's Anthropic-compatible API (`ANTHROPIC_BASE_URL=https://openrouter.ai/api`) |
| opencode | `OPENROUTER_API_KEY` | `opencode run --agent live-probe -m openrouter/<model>` |
| codex | `OPENROUTER_API_KEY` | `codex exec --json` with an OpenRouter Responses provider; the main agent spawns the `live-probe` role (Codex has no direct agent flag) |
| copilot | `OPENROUTER_API_KEY` | `copilot -p --agent live-probe` with OpenRouter as BYOK provider (`COPILOT_PROVIDER_BASE_URL`), no GitHub login |
| cursor | `CURSOR_API_KEY` | `cursor-agent -p` signed in with a Cursor API key; the main agent runs the `live-probe` subagent through its Task tool |
| gemini | `GEMINI_API_KEY` | `gemini -p` with a Gemini API key; the main agent invokes the `live-probe` subagent (`invoke_agent`) |
| omp | `OPENROUTER_API_KEY` | `omp -p --model openrouter/<model>`; the main agent runs `live-probe` via `task` |
<!-- coverage:end -->

## Gemini CLI agent-scoped MCP servers

The docs/runtime question (`mcpServers` documented vs `mcp_servers` seen at runtime) is
settled by the suite at the pinned version and nightly at the latest: Gemini CLI's local
agent schema accepts `mcp_servers` and rejects `mcpServers` ("Unrecognized key(s) in
object: 'mcpServers'", observed with 0.66.0, which is also the latest release on
2026-11-02). agnthub agrees: a native Gemini Variant with `mcp_servers` installs and loads
(`gemini-mcp-servers`, `N`), one with `mcpServers` is refused as invalid
(`gemini-mcp-camel`). The converter never writes an agent-scoped MCP server field for
Gemini (agent-scoped servers are refused on every target); it writes MCP tool references
as `mcp_<server>_<tool>` / `mcp_<server>_*` entries of `tools`, which Gemini CLI loads
(`tools-mcp-tool`, `tools-mcp-server`).

## MCP setup (`agnthub mcp install` / `uninstall`)

`TestHarness/mcp` ([mcp_test.go](mcp_test.go)) runs in every Harness image, offline and in an
empty home whose user configuration holds a neighbor server (`other`, an `.invalid` URL)
and agnthub's removed local server (`agnthub`: `npx -y agnthub-cli mcp`); omp also has a
named profile `work` with only the neighbor. Each step asks the real Harness which MCP
servers its configuration defines, without a model or a sign-in:

| Harness | How agnthub changes it | Real CLI listing |
|---|---|---|
| claude | `claude mcp add` / `claude mcp remove` (`mechanism: command`) | `claude mcp list` (name and URL; the health check fails offline, after the listing) |
| opencode | file edit of `~/.config/opencode/opencode.json` (OpenCode 2 `mcp.servers`) | `opencode mcp list` from its service (retried until it loaded, then stopped) and the URL from `opencode debug config` |
| codex | file edit of `~/.codex/config.toml` | `codex mcp list --json` (name and URL) |
| copilot | `copilot mcp add` / `copilot mcp remove` | `copilot mcp list` and `copilot mcp get agnthub` (URL) |
| cursor | file edit of `~/.cursor/mcp.json` | `cursor-agent mcp list` (names) |
| gemini | `gemini mcp add -s user` / `gemini mcp remove -s user` | `gemini mcp list` (name and URL) |
| omp | file edit of each profile's `mcp.json` | omp's own loader, `loadAllMCPConfigs` from the installed package (what omp connects to at startup), with `OMP_PROFILE` unset and `=work` |

Steps: bare `agnthub mcp` without a terminal exits 1 with the removal notice and the
`mcp install` hint, and `mcp install` without `--harness` exits 1; `mcp install --harness <h>
--yes --json` replaces the local server (`update`; omp: `update` in the default profile,
`install` in `work`) with the hosted server, which the Harness then lists at
`https://mcp.agnthub.sh` beside the neighbor; a second install is `unchanged`; `mcp uninstall
--yes --json` removes it from every profile while the Harness still lists the neighbor; a
fresh install is `install`; `--api-key` (with `AGNTHUB_API_KEY`) rewrites it with a bearer
header the Harness still loads and warns that the key is stored in plain text; `mcp
uninstall --harness <h> --yes` removes it again. The exact files each edit produces (every
legacy form, a foreign entry, comments, an unparseable file, the API key header), backups,
file modes, environment overrides, project scope and the prompts are proven by the unit
tests in `internal/mcpsetup` and `internal/cli/mcp_test.go`, not here: the Harness commands
add their own state (for example Claude Code's `~/.claude.json` bookkeeping), so these
steps have no goldens.

Not proven: signing in. The suite runs without a network and an account, so no Harness
completes the OAuth flow against the hosted server.

## Not proven by this suite, and why

- **Cursor user scope loading.** cursor-agent loads only workspace agents; agnthub installs
  user agents in `~/.cursor/agents`, where Cursor documents them for the editor. The native
  check asserts the CLI still ignores them, so the nightly run flags the day it starts
  loading them.
- **Cursor offline run.** agent-cli-local lists custom subagents but does not put their
  instructions into the subagent's prompt (verified with the stub: the subagent request
  carries no part of the agent body), so only the live tier with a Cursor API key can show
  a Cursor subagent running its installed instructions.
- **Live runs without their secret.** The live tier skips a Harness whose secret is not
  set; locally only `OPENROUTER_API_KEY` was available, so Gemini CLI and Cursor live runs
  are implemented but were not exercised (see the Live tier table).
- **Gemini CLI project agents in headless runs.** Gemini CLI uses a project agent only after
  it was acknowledged in an interactive session (a security prompt keyed by the file's
  SHA-256); a fresh `gemini -p` run silently skips unacknowledged project agents. The
  native check acknowledges exactly the installed files; agnthub does not write Gemini's
  acknowledgment store.
- **skills.sh's own lock after dependency cleanup.** `remove --clean-dependencies` deletes
  the skill directory agnthub's skill installer created, but skills.sh's project
  `skills-lock.json` (a file agnthub does not own) keeps the skill's entry, so a later
  `skills experimental_install` would bring it back. The `lifecycle-clean-dependencies`
  goldens record this; agnthub has no skill-installer removal command to delegate to.
- **Behaviors proven by unit tests only** (not by the containerized suite, because they do
  not depend on a Harness CLI): dependency cycles, conflicting dependency revisions and
  missing or ambiguous dependency agents (`resolve/resolve_test.go`), dependency failures
  leaving the project untouched (`internal/cli/add_deps_test.go`); conversion from
  non-portable Variants and the portable core's precedence over them
  (`harness/convert_test.go`); manifest path safety and discovery budgets (`discovery`);
  SSH transports and install reports to the registry (`source`, `internal/cli`); Windows
  and macOS destinations (`harness`, cross builds).
