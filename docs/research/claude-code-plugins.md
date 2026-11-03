# Claude Code plugins and marketplaces as agent sources

Checked 2026-11-03 against Claude Code's own documentation. Many agent collections ship as a Claude Code plugin marketplace rather than a `.claude/agents/` directory (for example [VoltAgent/awesome-claude-code-subagents](https://github.com/VoltAgent/awesome-claude-code-subagents), commit `ed74d115`: ten plugins under `categories/`, 164 agents listed in `plugin.json` `agents` arrays). This note records what the discovery of those layouts relies on; the decisions are in [ADR 0011](../adr/0011-claude-code-plugin-layouts.md).

## Marketplace file

Source: [Marketplace reference](https://code.claude.com/docs/en/plugins/marketplace-reference).

- The file is `.claude-plugin/marketplace.json` in the marketplace's directory. The directory that contains `.claude-plugin/` is the marketplace root, and every relative plugin source resolves from it, not from `.claude-plugin/`.
- Required top-level fields: `name`, `owner`, `plugins`. Each entry of `plugins` is validated on its own: one invalid entry does not fail the marketplace.
- Each entry requires `name` and `source`. `source` is either a relative path string or an object whose `source` key names the type.
  - **Relative path:** a directory inside the marketplace, resolved from the marketplace root; it must start with `./`, and `"."` alone means the root itself. With `metadata.pluginRoot` set (Claude Code 2.1.239+), a *bare name* (one directory name without `/`) resolves under that directory; a source containing `/` still needs `./`.
  - **Remote:** `github` (`repo`, `ref`, `sha`), `url` (any Git URL), `git-subdir` (one directory of another repository), `npm`, `archive` (HTTPS zip) and `command` (a directory printed by a command run on the user's machine).
- An entry accepts every `plugin.json` field. When the plugin has its own `plugin.json`, `strict` (default `true`) decides what happens to the entry's component fields (`commands`, `agents`, `skills`, `hooks`, `outputStyles`, `themes`): with `true` they are appended to the manifest's; with `false` declaring any is a conflict and the plugin fails to load. Without a `plugin.json`, the entry is the manifest.

## Plugin manifest and agents

Sources: [Plugin manifest reference](https://code.claude.com/docs/en/plugins-reference), [Plugin components: agents](https://code.claude.com/docs/en/plugins/components#agents).

- The manifest is optional, at `.claude-plugin/plugin.json` under the plugin root; without it Claude Code loads the standard layout and takes the name from the marketplace entry (or the directory with `--plugin-dir`). `name` is the only required manifest field; components are namespaced under the manifest's `name`, also when the marketplace entry lists the plugin under another name.
- `agents` is a path or an array of paths to agent `.md` files. **Directories aren't accepted.** Setting it **replaces** the default `agents/` scan.
- Path rules: every component path is relative to the plugin root and must start with `./` (`agents/x.md` fails validation). Every path must resolve inside the plugin root (a `..` path is reported as a traversal attempt and does not load) and must exist.
- Default location: `agents/` under the plugin root, scanned recursively; subfolders become part of the scoped name (`agents/review/security.md` in `my-plugin` is `my-plugin:review:security`). A file listed in `agents` loads without subfolder names.
- An agent is named `<plugin>:<name>`, where `<name>` is the frontmatter `name`, or the file name when `name` is missing or longer than 256 characters. Frontmatter that doesn't parse still loads, under the file name, with description `Agent from <plugin> plugin`.
- A repository root can itself be a plugin: a root `.claude-plugin/plugin.json` with components at the root (`--plugin-dir .`, or a marketplace entry whose source is `"."`/`"./"`).

## What a plugin agent may declare

Sources: [Plugin components: frontmatter fields in plugin agents](https://code.claude.com/docs/en/plugins/components#frontmatter-fields-in-plugin-agents), [Subagents: choose the subagent scope](https://code.claude.com/docs/en/sub-agents#choose-the-subagent-scope).

- Supported: `name`, `description`, `model`, `effort`, `maxTurns`, `autoCompactWindow`, `tools`, `disallowedTools`, `skills`, `memory`, `background`, `omitClaudeMd`, `isolation` (only `worktree`), `color`, `experimental.cacheTtl`.
- **Ignored:** `permissionMode`, `hooks`, `mcpServers` and `initialPrompt`. The subagents page: "For security reasons, plugin subagents don't support the `hooks`, `mcpServers`, or `permissionMode` frontmatter fields. These fields are ignored when loading agents from a plugin. If you need them, copy the agent file into `.claude/agents/` or `~/.claude/agents/`." Copying a plugin agent into an agents directory therefore activates fields the plugin form never applied.

## Variables that only exist inside a plugin

Source: [Plugin manifest reference: environment variables](https://code.claude.com/docs/en/plugins-reference#environment-variables), [user configuration](https://code.claude.com/docs/en/plugins-reference#reference-a-saved-value).

- `${CLAUDE_PLUGIN_ROOT}` (the installed plugin's directory) and `${CLAUDE_PLUGIN_DATA}` (`~/.claude/plugins/data/<id>/`) are substituted "anywhere in the Markdown body" of skill, command and agent content; `${user_config.KEY}` substitutes a non-sensitive option the user set for the plugin. An agent body using them points at files or settings that exist only where the plugin is installed.

## Consequences for agnthub

- Relative plugin sources are part of the snapshot and can be read inertly. Remote plugin sources are other repositories (or commands); agnthub does not fetch them during discovery.
- Plugin agents are Claude Code agent files, so they are Claude Code Variants. Installed standalone they would gain `permissionMode`, `hooks`, `mcpServers` and `initialPrompt`, and lose the `${CLAUDE_PLUGIN_ROOT}`-style substitutions.
- Agent identity stays the frontmatter `name` Claude Code would show after the plugin prefix; the `<plugin>:` scope has no equivalent outside a plugin.
