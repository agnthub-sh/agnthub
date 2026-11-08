# agnthub

agnthub is a registry and installer for coding agents. Find agents at
[app.agnthub.sh](https://app.agnthub.sh), then install them from their source repository into
the coding harnesses you use: Claude Code, Codex, OpenCode, GitHub Copilot CLI, Cursor,
Gemini CLI and omp.

```sh
agnthub add owner/repo --list                 # what the repository offers, without writing anything
agnthub add owner/repo                        # choose agents and harnesses, preview, then install
agnthub add owner/repo --agent reviewer --harness claude --harness codex
agnthub add owner/repo --agent reviewer --harness claude --diff   # the exact files as a git diff; writes nothing
agnthub list                                  # what is installed here
agnthub check                                 # newer source content and local drift, no writes
agnthub update                                # preview and apply newer content
agnthub restore                               # recreate exactly what agnthub-lock.json records
agnthub remove reviewer
agnthub auth login                            # optional: an agnthub.sh API key for higher rate limits
agnthub mcp install                           # add the hosted MCP server to your harnesses
```

Every install is previewed before anything is written, the project lock
(`agnthub-lock.json`) pins each agent to a commit, and agnthub never overwrites files it did
not install or that you changed. Repositories need no particular setup: agnthub reads portable
`AGENT.md`/`agents/` definitions, each harness's native agent directory, the Claude Code plugins
and marketplaces a repository packages (`.claude-plugin/`), and an optional `agnthub.yaml`.

A repository owner who wants agnthub to leave the repository, or some paths in it, alone lists
them in `.agnthub-ignore` at the root (one literal path per line; a file without paths ignores
the whole repository) or under `ignore:` in `agnthub.yaml` (`ignore: all` or a list of paths).
The CLI and the agnthub.sh indexer then never read those paths; `agnthub add --include-ignored`
installs from them anyway, and agents already installed keep updating. See
[ignoring a repository](docs/cli.md#behavior).

`-d/--diff` on `add`, `update`, `restore` and `remove` prints every file the command would
create, update or delete as a git-style unified diff (exactly the bytes it would write, generated
by the same code as the write) and writes nothing; `git apply --check` accepts it, and `-d -y`
shows it and then applies it. See [the `--diff` details](docs/cli.md#behavior).

A preview is a table of what each harness gets, sorted by agent and harness. Only rows that
change something are listed; notes, refusals and the agents already in place are summed up
under it. Once you approve, agnthub prints only the outcome:

```text
$ agnthub add addyosmani/agent-skills --agent '*' --harness all --global
📋 Plan · addyosmani/agent-skills@301b02b12bff · global scope
   harnesses: claude, opencode, codex, copilot, cursor, gemini, omp profile=default (native default)
   omp: default profile

   AGENT                    HARNESS   ACTION    PATH
   code-reviewer            copilot   + create  ~/.copilot/agents/code-reviewer.agent.md
   code-reviewer            omp       + create  ~/.omp/agent/agents/code-reviewer.md
   code-reviewer            opencode  + create  ~/.config/opencode/agents/code-reviewer.md
   security-auditor         copilot   + create  ~/.copilot/agents/security-auditor.agent.md
   …
   web-performance-auditor  opencode  + create  ~/.config/opencode/agents/web-performance-auditor.md

   · converted from the claude variant
   16 agents were already correct in their harnesses, so no action was needed.

   12 to create · 0 to update · 0 to delete

   ┃ Apply this plan?
   ┃
   ┃   Apply     Cancel

✅ Installed · addyosmani/agent-skills@301b02b12bff · global scope
   12 created · 0 updated · 0 deleted
```

When everything is already in place there is nothing to approve:

```text
$ agnthub add addyosmani/agent-skills --agent '*' --harness all --global
✅ Up to date · addyosmani/agent-skills@301b02b12bff · global scope
   harnesses: claude, opencode, codex, copilot, cursor, gemini, omp profile=default (native default)

   28 agents were already correct in their harnesses, so no action was needed.
```

Agents install into the project in the current directory, or into your user configuration with
`--global`. Your home directory is no project: run there, `add` installs into user scope (as
`--global` does) and says so, and `--global=false` refuses. Installs that agnthub 0.4.7 and
earlier made with the home directory as the project (for example `~/.omp/agents/<name>.md`) are
removed with `agnthub remove <name>` run in the home directory.

Files agnthub converts for a harness (rather than installing an authored file as is) start with
comment lines naming the original file and repository, saying agnthub modified it, and repeating
the source's copyright notice and license; harnesses ignore them. See
[docs/cli.md](docs/cli.md) for the format.

The full documentation is at
[app.agnthub.sh/docs](https://app.agnthub.sh/docs).

This repository holds the CLI source and its releases. The hosted registry service is developed
separately and builds on the packages here.

## Install

agnthub is a single native binary for Linux, macOS and Windows on amd64 (x64) and arm64. It
does not need Node.js.

### Direct download

Pick the archive for your platform from the
[latest release](https://github.com/agnthub-sh/agnthub/releases/latest) and verify it against
the release's `SHA256SUMS` before running it.

Linux and macOS: download, verify and unpack.

```sh
VERSION=0.4.1
OS=linux       # or darwin
ARCH=amd64     # or arm64
BASE=https://github.com/agnthub-sh/agnthub/releases/download/v$VERSION

# Download the archive and the release checksums
curl -fsSLO "$BASE/agnthub_${VERSION}_${OS}_${ARCH}.tar.gz"
curl -fsSLO "$BASE/SHA256SUMS"

# Verify the archive; it must print OK (macOS: shasum -a 256 --ignore-missing -c SHA256SUMS)
sha256sum --ignore-missing -c SHA256SUMS

# Unpack the binary into a directory on your PATH
tar -xzf "agnthub_${VERSION}_${OS}_${ARCH}.tar.gz" agnthub
mkdir -p ~/.local/bin
mv agnthub ~/.local/bin/
agnthub --version
```

If `agnthub` isn't found afterwards, add `export PATH="$HOME/.local/bin:$PATH"` to your shell
profile, or put the binary in a system directory instead with `sudo mv agnthub /usr/local/bin/`.

Windows (PowerShell):

```powershell
$Version = "0.4.1"
$Arch = "amd64"   # or arm64
$Base = "https://github.com/agnthub-sh/agnthub/releases/download/v$Version"
$Zip = "agnthub_${Version}_windows_$Arch.zip"
Invoke-WebRequest "$Base/$Zip" -OutFile $Zip
Invoke-WebRequest "$Base/SHA256SUMS" -OutFile SHA256SUMS
$Expected = (Select-String -Path SHA256SUMS -Pattern " $Zip$").Line.Split(" ")[0]
if ((Get-FileHash $Zip -Algorithm SHA256).Hash -ne $Expected) { throw "checksum mismatch: do not run this file" }
Expand-Archive $Zip -DestinationPath agnthub
.\agnthub\agnthub.exe --version
```

The binaries are not code-signed. On macOS, a file downloaded with a browser (rather than
`curl`) is quarantined; after verifying its checksum, clear it with
`xattr -d com.apple.quarantine agnthub`.

### npm launcher

The `agnthub-cli` npm package is a small launcher with no dependencies and no install scripts. On
first run it downloads the archive for your platform from this repository's release of the
same version, checks it against SHA-256 checksums that ship inside the npm package, refuses
to run anything that does not match, and caches the verified binary (`~/.cache/agnthub`,
`~/Library/Caches/agnthub` or `%LOCALAPPDATA%\agnthub\Cache`). The installed command is `agnthub`.

```sh
npx agnthub-cli@0.4.3 add owner/repo --list
npm install -g agnthub-cli@0.4.3
```

### From source

With Go (the version in [go.mod](go.mod)) installed:

```sh
go install github.com/agnthub-sh/agnthub/cmd/agnthub@latest
```

This builds the CLI from source without the release checksums; `agnthub --version` reports the
module version.

## Documentation

The user documentation lives on the site, at [app.agnthub.sh/docs](https://app.agnthub.sh/docs):
[getting started](https://app.agnthub.sh/docs), [installing the CLI](https://app.agnthub.sh/docs/install),
[commands and flags](https://app.agnthub.sh/docs/commands), [harnesses](https://app.agnthub.sh/docs/harnesses),
[cross-harness conversion](https://app.agnthub.sh/docs/conversion),
[dependencies](https://app.agnthub.sh/docs/dependencies), [the lockfile](https://app.agnthub.sh/docs/lockfile),
[authoring agents](https://app.agnthub.sh/docs/authoring) and
[privacy and install reports](https://app.agnthub.sh/privacy).

In this repository:

- [Using the CLI](docs/cli.md): sources, selection, scopes, Harness settings, dependencies,
  skills, check/update/restore/remove, [AI agents (MCP)](docs/cli.md#ai-agents-mcp) and the
  [API key and rate limits](docs/cli.md#api-key-and-rate-limits)
- [CLI contract](docs/cli-contract.md), [glossary](GLOSSARY.md) and
  [architecture decisions](docs/adr/)
- [Native Harness agent formats and conversion limits](docs/research/harness-agent-formats.md)
- [Claude Code plugins and marketplaces as agent sources](docs/research/claude-code-plugins.md)

## AI agents (MCP)

AI agents use agnthub through its hosted MCP server, `https://mcp.agnthub.sh`: they search the
registry, show you the exact files an install writes with
`npx -y agnthub-cli@latest add … --diff`, check them for security issues and, after you confirm,
install with the same command and `-y`. `agnthub mcp install` sets the server up in every harness
on this machine that does not have it yet:

```sh
npx agnthub-cli mcp install                              # confirm, or pick from a list of the missing ones
npx agnthub-cli mcp install --all --yes                  # every detected harness that lacks it, no questions
npx agnthub-cli mcp install --harness claude,codex --yes
npx agnthub-cli mcp install --harness omp --profile work --yes
npx agnthub-cli mcp uninstall                            # remove it again
```

Harnesses (and omp profiles) that already have the server are never offered, only named on
one line; with none missing it says so and exits 0. One missing harness is a yes/no question,
several a toggle list (all preselected) and then a confirmation; `--all` skips the list and
`--yes` the confirmation. Without a terminal, pass `--all` or `--harness`; `--json` without
`--harness` behaves like `--all --yes`. `mcp uninstall` works the same way on the installs it
finds. It adds an `agnthub` server (Streamable HTTP) to each harness's user configuration (or
the project's, with `--scope project`), with Claude Code's, Copilot CLI's and Gemini CLI's own
`mcp add` when they are on `PATH` and otherwise by editing the file: only the `agnthub` entry
changes, the previous file is backed up under `~/.local/state/agnthub/mcp-backups` and the new
one is written atomically with the old mode. Older agnthub entries (the `/mcp` URLs and the local
server removed in v0.5.0) count as missing and are updated; an `agnthub` entry agnthub did not
write is kept unless you confirm or pass `--force`. You then sign in with GitHub in each harness
(it prints how), or pass `--api-key` to store your API key as a header instead: it is then kept
in plain text in each harness's MCP configuration file, readable by those tools and anyone who
can read the file (the CLI warns). See [AI agents (MCP)](docs/cli.md#ai-agents-mcp) for every
flag, the per-harness details and the exit statuses.

## API key and rate limits

Everything works without an account; the registry API limits anonymous callers per network
address (search: 60/min and 2,000/hour). With an agnthub.sh API key the limits are ten times
higher and charged to your account:

```sh
agnthub auth login     # opens https://app.agnthub.sh/api-key; paste the key at the hidden prompt
agnthub auth status    # key source, agh_…last4, GitHub account, current limit (--json too)
agnthub auth logout
```

`AGNTHUB_API_KEY` wins over the saved key (`apiKey:` in `~/.config/agnthub/config.yaml`,
mode 0600). The CLI and install reports send it as `Authorization: Bearer` to
the registry API only, never to GitHub or any other host. A short `429` (`Retry-After` of 10 s
or less) is waited out once; a longer one fails with the time to wait. See
[API key and rate limits](docs/cli.md#api-key-and-rate-limits) for the limits, headers and
errors.

## Usage reports

After installing agents from a public GitHub repository, the CLI tells the registry
(`https://api.agnthub.sh`) which repository, commit and agents were installed and into which
harnesses, so installs of listed repositories are counted. A report never adds a repository to
the registry. No paths, profiles, harness settings, or private and local sources are sent; the
report is pseudonymous (a random id per agent, linked to no account) unless you configured an
API key, which it then carries. Before the first report, the CLI says so once on stderr. Set
`DO_NOT_TRACK` or `DISABLE_TELEMETRY` to any non-empty value to send nothing. Details:
[install reports](https://app.agnthub.sh/privacy#install-reports).

## Contributing

See [CONTRIBUTING](CONTRIBUTING.md). Report security problems privately as described in
[SECURITY](SECURITY.md).

## License

[PolyForm Shield 1.0.0](LICENSE): you may use, change and redistribute the CLI for any purpose
except providing a product that competes with it or with a product agnthub.sh provides with it,
such as another agent registry. Releases up to v0.10.2 were published under the MIT License and
stay available under it.
