# agnthub

Launcher for the [agnthub](https://app.agnthub.sh) CLI, which installs coding agents from
source repositories into Claude Code, Codex, OpenCode, GitHub Copilot CLI, Cursor, Gemini CLI
and omp.

```sh
npx agnthub add owner/repo --list
```

This package has no dependencies and no install scripts. On first run it downloads the native
binary for your platform (Linux, macOS or Windows on x64 or arm64) from the
[heynemann/agnthub](https://github.com/heynemann/agnthub/releases) GitHub release of the same
version, verifies it against the SHA-256 checksums shipped inside this package, and caches it
(`$XDG_CACHE_HOME/agnthub` or `~/.cache/agnthub`, `~/Library/Caches/agnthub`,
`%LOCALAPPDATA%\agnthub\Cache`; override with `AGNTHUB_CACHE_DIR`). A download that does not
match is rejected and never run. Arguments, standard streams and the exit status pass straight
through to the binary.

`AGNTHUB_BINARY=/path/to/agnthub` runs that binary instead (for local development).

The native binary does not need Node.js; see the
[README](https://github.com/heynemann/agnthub#install) for direct downloads.
