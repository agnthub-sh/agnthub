# Changelog

## v0.10.0

### Added

- **Catalog layouts** (ADR 0017). A source without `agnthub.yaml` and without agents in any
  other layout may keep its agents in top-level division folders, as msitarzewski/agency-agents
  does: `agnthub add msitarzewski/agency-agents --list` now lists its 283 agents across 18
  divisions. A Markdown file directly in a folder, or one folder down, is an agent when its
  frontmatter has a `name`, a `description`, mostly agent fields and no `id` or `slug`, and
  instructions follow; a folder is a division when two thirds of its Markdown files are agents,
  and the source is a catalog when divisions with three or more agents hold five together.
  Documentation, code, tooling, example, template, test and integration folders never count.
  `--list` shows each agent's `division` (also in `--list --json`) and the picker shows the
  folder.
- **Same-name conflicts.** Two agents that one harness would load under the same name in one
  directory are refused as a conflict naming the other file or planned agent.

### Changed

- **Display names are accepted in every layout.** A `name` that is not an identifier
  (`name: Search Relevance Engineer`, capitals, spaces) no longer refuses an agent or decides
  its identity: the identifier is the location, normalized (ASCII-folded, lowercased,
  hyphenated: `Café Copywriter (v2).md` → `cafe-copywriter-v2`). Two files that normalize to one
  identifier are ambiguous. Authored files install byte for byte into their own harness unless it
  rejects the name (Gemini CLI needs a slug; Claude Code skips a leading `-`, a `:` or more than
  256 characters). Converted files use the identifier as `name` in Claude Code, Codex, Cursor,
  Gemini CLI and omp, keep the display name in Copilot CLI's `name` and Gemini CLI's
  `display_name`, and elsewhere start the description with it. Cosmetic keys (`color`, `emoji`,
  `vibe`) are dropped with a note instead of refusing the agent.
- `--list` and the picker no longer cut a description inside a multi-byte character.

### Library

- `agent.Definition.Division`, `agent.NormalizeID`, `agent.IDFromPath`, `agent.DisplayName`;
  `harness.NativeAgent.DisplayName`, `harness.Semantics.DisplayName`, `harness.InvocationName`,
  `harness.KnownAgentKey`; `category.FromDivision` maps obvious division folder names to
  taxonomy categories (a hint, never a publisher category).

## v0.9.2

### Changed

- **The install-report notice says the reports are pseudonymous.** It now reads:
  `agnthub sends install counts tagged with a random installation id (pseudonymous; no account
  or IP is stored); set DO_NOT_TRACK=1 to opt out. See
  https://app.agnthub.sh/privacy#install-reports`. v0.9.1's wording ("anonymous-by-design")
  contradicted the privacy policy. A machine that already showed the v0.9.1 notice does not
  show it again.

## v0.9.1

### Changed

- **Converted agent files carry their source's attribution and are marked as modified.**
  Every generated (converted) file starts with comment lines that every harness ignores and
  that stay outside the agent's instructions: YAML comments right after the frontmatter's
  opening `---` (Claude Code, OpenCode, Copilot CLI, Cursor, Gemini CLI, omp) or `#` comments
  at the top of a Codex `.toml` file. They name the original file and repository
  (`# Adapted from agents/reviewer.md in github.com/acme/agents`), say agnthub converted it
  (`# Modified by agnthub: converted to the Codex agent format`), repeat the copyright lines of
  the repository's root `LICENSE`/`LICENCE`/`COPYING` file and point to the license (or say
  `# No license file found`). Authored Variants still install byte for byte. This is converter
  `v6`: `check` shows converted files from earlier versions as a converter change and `update`
  rewrites them; `restore` of a lock written by `v5` or earlier still reproduces it byte for
  byte, without the header.
- **One-time install-report notice.** Right before the first install report a machine sends,
  the CLI prints on stderr (also with `--json`): `agnthub sends anonymous-by-design install
  counts with a random installation id; set DO_NOT_TRACK=1 to opt out. See
  https://app.agnthub.sh/privacy#install-reports`. It is recorded in
  `$XDG_STATE_HOME/agnthub/install-report-notice` and never shown again; opted-out runs and
  runs that send nothing show nothing. The docs now call the reports pseudonymous.

### Library

- `discovery.Inventory.License` and `agent.Definition.License` (`*agent.License{Path,
  Copyright, More}`) expose the source's root license file and its copyright lines;
  discovery reads it without ever failing a scan. `install.Target.Converter` lets a restore
  reproduce a lock's converter, and `harness.Attribute` writes the attribution header.

## v0.9.0

### Changed

- **`mcp install` offers only the harnesses that are missing agnthub's MCP server.** It
  detects every supported harness (and every omp profile) and keeps the ones without the
  hosted server at `https://mcp.agnthub.sh`; an older agnthub entry (the former `/mcp` URLs,
  the local server `npx agnthub-cli mcp`) counts as missing and is updated. The ones already
  set up are never offered, only named on one dimmed line (`Already set up: claude,
  omp · default`). With none missing it prints `agnthub's MCP server is already set up in
  every detected harness.` with the detected ones and exits 0. In a terminal, one missing
  harness is a yes/no question (`Add agnthub's MCP server to cursor?`); several are a toggle
  list of only those, all preselected, and then a confirmation of the chosen set. Declining
  exits 1 and writes nothing. The human table lists only what changed.
- **`mcp uninstall` follows the same pattern**: none found prints
  `No agnthub MCP installs detected.` and exits 0; one is a yes/no question; several are a
  toggle list (all preselected) and then a confirmation.
- **New `--all`** on both commands takes every candidate without the list; `--yes` now only
  skips the confirmation, so `--all --yes` is fully non-interactive. Explicit `--harness` /
  `--profile` picks show no list and, in a terminal, are confirmed unless `--yes`.

### Breaking changes

- `--harness all` is removed from `mcp install` and `mcp uninstall`; use `--all` (it now
  fails with that hint).
- Without a terminal, both commands need `--all` or `--harness`: `mcp uninstall --yes` alone
  no longer removes every install found (use `mcp uninstall --all --yes`), and `--all` runs
  without a confirmation there.
- `--json` without `--harness` now behaves like `--all --yes` (it used to fail without
  `--harness`); it still never prompts.

## v0.8.0

### New

- **`--diff` (`-d`)** on `add`, `update`, `restore` and `remove` (including
  `--clean-dependencies`) prints every file the command would create, update or delete as a
  unified diff in git's format, with the exact bytes it would write, and writes nothing. The
  diff runs the same apply code as the real write against an in-memory recorder, so it cannot
  drift from what `--yes` writes. Created files are `new file mode` from `/dev/null`, updated
  files a real diff against the file on disk, deleted files `deleted file mode` (a removed skill
  directory lists each file, a removed link is a link deletion), binary content a
  `GIT binary patch`. Paths are relative to the project root (`git apply --check` accepts the
  diff; a `# Check:` comment gives the exact command, with `--directory` inside a larger
  repository), or to your home directory in user scope. It covers agent files in every harness,
  dependency agents, the files of skills the plan installs and agnthub's lock; agnthub's private
  state directory (ownership records) is left out, and `#` comments at the top say so. The diff
  goes to stdout and the plan to stderr; a color terminal gets git's diff colors, redirected
  output, `NO_COLOR` and `TERM=dumb` plain text. `--diff` never prompts and exits 0 (1 when the
  plan has blocked items, whose changes it leaves out); `--diff --yes` shows the diff and then
  applies it. `--json --diff` adds `diff` and `files` (`[{path, action, sha256}]`) to the plan.
- Every command that plans and applies (`add`, `update`, `restore`, `remove`, `mcp install`,
  `mcp uninstall`) accepts `-y` for `--yes`, as before; a test now keeps it that way.
- Packages for the hosted registry: `gitdiff` renders recorded changes as a git diff,
  `install.Planner.Preview` returns the exact changes `Apply` would make, `state.Files` /
  `state.Recorder` let every write go to disk or to memory, `resolve.Closure.Targets` turns a
  dependency closure into plan targets, and `skills.Item.Files` carries a skill install's
  validated source files.

### Changed

- `state.Lock.Save` and `state.Ownership.Save` take the `state.Files` they write through
  (`state.Disk` for the filesystem), and `skills.RemoveInstalled` takes it first.

## v0.7.0

### New

- **Worktree isolation installs everywhere it can be emulated.** A Claude Code agent with
  `isolation: worktree` keeps it on Claude Code. On the other harnesses, which cannot isolate
  one agent in its own worktree, an agent that can run any shell command now installs with
  an instruction at the top of its instructions to create a git worktree, make every edit,
  command and commit there, and report the worktree path and branch. The plan shows
  `! reduced: <agent> on <harness> goes without isolation` with the note "<harness> can't
  isolate it; the agent is instructed to work only in a git worktree", and `--json` lists
  `isolation` under `reduced`. An agent that edits without such a shell stays refused there
  (it cannot create a worktree); one that neither edits nor runs commands installs unchanged.
- **Manual-only agents convert.** A Copilot CLI agent with `disable-model-invocation: true`
  (or the retired `infer: false`) is written as a primary agent (`mode: primary`) on
  OpenCode, which never delegates to it. On Claude Code, Codex, Cursor, Gemini CLI and omp,
  which have no such field, it installs with its tools unchanged and a description starting
  "Use only when the user explicitly asks for this agent.", shown as `! reduced: … goes
  without manual-only invocation`.
- **`Tool(*)` is the tool.** A Claude Code rule whose specifier is exactly `*`
  (`WebFetch(*)`, `Bash(*)`, `Read(*)`) converts as the plain tool, as Claude Code reads it;
  other specifiers (`Bash(git *)`, `WebFetch(domain:…)`) keep their handling.

See [ADR 0014](docs/adr/0014-emulate-worktree-isolation-and-manual-only-invocation.md) and
[harness-agent-formats.md](docs/research/harness-agent-formats.md#manual-only-invocation-and-worktree-isolation-verified-2026-11-06).

## v0.6.0

### New

- **`agnthub mcp install`** sets up agnthub's hosted MCP server (`https://mcp.agnthub.sh`) in
  every harness on this machine: Claude Code, OpenCode, Codex, GitHub Copilot CLI, Cursor,
  Gemini CLI and omp, in every omp profile. In a terminal it lists the detected harnesses and
  profiles (the ones without agnthub preselected, installed ones marked, older agnthub setups
  offered as updates); otherwise choose with `--harness claude,codex` / `--harness all` and
  `--profile <omp profile>`, with `--yes` and `--json`. It writes an `agnthub` Streamable HTTP
  entry at user scope (`--scope project` for the project's file), with Claude Code's, Copilot
  CLI's and Gemini CLI's own `mcp add` when they are on `PATH` and otherwise by editing the
  file: comments and every other entry are kept, the previous file is backed up under
  `~/.local/state/agnthub/mcp-backups`, the new one is written atomically with the old mode,
  and a file that does not parse is never changed. Older agnthub entries (the
  `https://api.agnthub.sh/mcp` and `https://mcp.agnthub.sh/mcp` URLs, and the local server
  `npx agnthub-cli mcp` removed in v0.5.0) are replaced; an `agnthub` entry agnthub did not
  write is kept unless confirmed or `--force`. It then prints how to sign in with GitHub in
  each harness; `--api-key` writes your agnthub API key as an `Authorization: Bearer` header
  instead (with a warning that it is stored in plain text). Exit status 0, 1 (error) or 2 (a
  foreign entry was skipped).
- **`agnthub mcp uninstall`** finds agnthub's entries, in any of those forms, in every
  harness and omp profile (and the project with `--scope project`), lists them all selected
  (`--yes` removes every one) and removes only those; other servers are never touched. With
  none it prints `No agnthub MCP installs detected.`
- Bare `agnthub mcp`, which configurations from before v0.5.0 still start as a stdio server,
  keeps printing the removal notice and exiting 1 without a terminal, now followed by
  ``Run `npx agnthub-cli mcp install` to configure the hosted server.``; in a terminal it shows
  the help of the `mcp` commands, which `agnthub --help` now lists.
- The end-to-end suite proves both commands in each harness image with the harness's own
  listing (`claude mcp list`, `codex mcp list --json`, `gemini mcp list`, `opencode mcp
  list`, `copilot mcp list`, `cursor-agent mcp list`, omp's MCP loader), an omp named profile
  included (see `e2e/COVERAGE.md`).

## v0.5.0

### Breaking changes

- **The local MCP server is removed.** `agnthub mcp` (`npx -y agnthub-cli mcp`), its tools
  (`search_agents`, `get_agent`, `get_repository`, `install_command`, `install_agents`,
  `auth_status`) and the public `mcpserver` Go package are gone. AI agents use the hosted MCP
  server instead: `https://mcp.agnthub.sh` (setup for every harness:
  https://app.agnthub.sh/mcp). Its `install_agents` tool returns the exact
  `npx -y agnthub-cli@latest add …` command, which the agent runs with its shell tool after the
  user confirms. A configuration that still starts `agnthub mcp` fails with:
  `The local MCP server was removed in v0.5.0. Use the hosted server instead:
  https://mcp.agnthub.sh (setup: https://app.agnthub.sh/mcp).` Replace that entry with the
  hosted server's URL.
- `agnthub auth` stays: the API key raises the CLI's own registry API limits.
