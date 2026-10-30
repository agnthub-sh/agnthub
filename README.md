# agnthub

agnthub is a registry and installer for coding agents. Find agents at
[app.agnthub.sh](https://app.agnthub.sh), then install them from their source repository into
the coding harnesses you use: Claude Code, Codex, OpenCode, GitHub Copilot CLI, Cursor,
Gemini CLI and omp.

```sh
agnthub add owner/repo --list                 # what the repository offers, without writing anything
agnthub add owner/repo                        # choose agents and harnesses, preview, then install
agnthub add owner/repo --agent reviewer --harness claude --harness codex
agnthub list                                  # what is installed here
agnthub check                                 # newer source content and local drift, no writes
agnthub update                                # preview and apply newer content
agnthub restore                               # recreate exactly what agnthub-lock.json records
agnthub remove reviewer
agnthub mcp                                   # MCP server (stdio) for AI agents
```

Every install is previewed before anything is written, the project lock
(`agnthub-lock.json`) pins each agent to a commit, and agnthub never overwrites files it did
not install or that you changed. The full documentation is at
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

Linux and macOS:

```sh
VERSION=0.3.0
OS=linux       # or darwin
ARCH=amd64     # or arm64
BASE=https://github.com/agnthub-sh/agnthub/releases/download/v$VERSION
curl -fsSLO "$BASE/agnthub_${VERSION}_${OS}_${ARCH}.tar.gz"
curl -fsSLO "$BASE/SHA256SUMS"
sha256sum --ignore-missing -c SHA256SUMS     # macOS: shasum -a 256 --ignore-missing -c SHA256SUMS
tar -xzf "agnthub_${VERSION}_${OS}_${ARCH}.tar.gz" agnthub
sudo install agnthub /usr/local/bin/agnthub  # or any directory on your PATH
agnthub --version
```

Windows (PowerShell):

```powershell
$Version = "0.3.0"
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

The `agnthub` npm package is a small launcher with no dependencies and no install scripts. On
first run it downloads the archive for your platform from this repository's release of the
same version, checks it against SHA-256 checksums that ship inside the npm package, refuses
to run anything that does not match, and caches the verified binary (`~/.cache/agnthub`,
`~/Library/Caches/agnthub` or `%LOCALAPPDATA%\agnthub\Cache`).

```sh
npx agnthub@0.3.0 add owner/repo --list
npm install -g agnthub@0.3.0
```

The npm package has not been published yet; until it is, use the direct download.

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
[privacy and install reports](https://app.agnthub.sh/docs/privacy).

In this repository:

- [Using the CLI](docs/cli.md): sources, selection, scopes, Harness settings, dependencies,
  skills, check/update/restore/remove and the [MCP server](docs/cli.md#mcp-server)
- [CLI contract](docs/cli-contract.md), [glossary](GLOSSARY.md) and
  [architecture decisions](docs/adr/)
- [Native Harness agent formats and conversion limits](docs/research/harness-agent-formats.md)

## Usage reports

After installing agents from a public GitHub repository, the CLI tells the registry
(`https://api.agnthub.sh`) which repository, commit and agents were installed and into which
harnesses, so installs of listed repositories are counted. A report never adds a repository to
the registry. No paths, profiles, harness settings, credentials, or private and local sources
are sent. Set `DO_NOT_TRACK` or `DISABLE_TELEMETRY` to any non-empty value to send nothing.

## Contributing

See [CONTRIBUTING](CONTRIBUTING.md). Report security problems privately as described in
[SECURITY](SECURITY.md).

## License

[MIT](LICENSE)
