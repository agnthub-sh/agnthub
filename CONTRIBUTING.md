# Contributing to agnthub

Thanks for helping. This repository is the agnthub CLI, the Go packages it is built from, and
its npm launcher. The hosted registry at [app.agnthub.sh](https://app.agnthub.sh) is a separate
service that imports these packages; it is not developed here.

Before changing behavior, read the [CLI contract](docs/cli-contract.md), the
[glossary](GLOSSARY.md) and the [architecture decisions](docs/adr/). For anything larger than a
fix, open an issue first so the approach can be agreed before you write it.

## Setup

- Go, at the version in [go.mod](go.mod)
- [golangci-lint v2](https://golangci-lint.run/welcome/install/)
- `git` (sources are fetched with the system `git`)
- Node.js 18 or newer, only for the npm launcher in `npm/agnthub`
- Docker, only for the end-to-end suite

```sh
make build        # bin/agnthub
make fmt          # gofumpt, goimports, golines
make lint         # must report 0 issues
make test         # go test -race ./...
make npm-test     # npm launcher tests
make cross        # compile every release platform
make e2e          # end-to-end suite against the real Harness CLIs (Docker)
```

CI runs the same checks on every push and pull request.

## End-to-end suite

`e2e/` is the regression suite that proves every supported layout installs exactly as
expected into each real Harness, and keeps proving it as Harnesses release new versions.
For each of the seven Harnesses it builds a container (`e2e/Dockerfile`, target
`e2e-<harness>`) with that Harness's CLI at a pinned version, skills.sh, and the `agnthub`
and e2e test binaries built from your checkout, then runs the suite offline
(`--network none`):

1. The fixture repositories in `e2e/testdata/fixtures/` are published as Git repositories
   served over HTTPS on the container's loopback (`https://localhost:8443/<name>.git`), and
   also used as local directories, so both source paths are exercised. `layouts` covers the
   portable layouts (root `AGENT.md`, `agents/<id>.md`, `agents/<id>/AGENT.md`), every
   Harness's native directory and suffix, native-over-portable Variant precedence, an
   invalid native Variant, unusual frontmatter (unquoted colons, folded descriptions,
   Unicode, CRLF), harmless metadata, tool lists that convert or refuse and MCP tool
   references. `ambiguous` has duplicate identities; `manifest` is an `agnthub.yaml` with
   Variants, same-repository, external (`companion`) and Variant-only Agent Dependencies
   and a Skill Dependency on `skills`.
2. Each step runs `agnthub add … --json` in a fresh project and home and compares the exit
   status, stderr, stdout and every file written to the project and home (bytes and modes)
   with `e2e/testdata/golden/<harness>/<step>/`. Refusals and plans must write nothing.
3. After each install the real Harness is asked, without a model call or a login, which
   agents it loads: `claude -p --agent <unknown>` (lists agents), `codex debug
   prompt-input` (custom agents appear as roles; the project is trusted in a temporary
   `CODEX_HOME`), `opencode debug agents`, `copilot -p x --agent <unknown>` (with a dummy
   BYOK provider that is never contacted), and, for the Harnesses without such a command,
   their own shipped loader code: Gemini CLI's `loadAgentsFromDirectory` with its storage
   paths, omp's `discoverAgents`, and cursor-agent's workspace subagent loader (see
   `e2e/testdata/checks/`). cursor-agent does not load user-scope agents, so that check is
   skipped for global installs.

```sh
make e2e                              # every Harness
make e2e HARNESS=claude               # one Harness (several: HARNESS="claude codex")
make e2e-update HARNESS=claude        # rewrite its goldens from this run
E2E_BUILD_ARGS="--pull --no-cache --build-arg CLAUDE_VERSION=latest" make e2e HARNESS=claude
```

`go test ./...` does not run the suite: its tests carry the `e2e` build tag and skip
outside their container. When a change alters what agnthub installs, run `make
e2e-update` and review the golden diff like code: it is the exact change users get. Add a
fixture or step for every new layout, field or Harness behavior.

The [E2E workflow](.github/workflows/e2e.yml) runs the suite per Harness on every push and
pull request at the pinned versions, and nightly with each Harness's latest release. A
nightly failure opens (or comments on) an issue titled "Nightly e2e: <harness> fails with
its latest release".

To bump a pinned Harness version:

1. Change its `*_VERSION` build argument at the top of `e2e/Dockerfile` (npm versions for
   Claude Code, Codex, Copilot CLI, Gemini CLI and omp, an OpenCode 2 release, a Cursor
   `cursor-agent --version` build such as `2026.10.22-27dcf5f`).
2. `make e2e HARNESS=<harness>`. If it fails because the Harness changed, fix agnthub
   (with a unit test) or, when the new output is intended, `make e2e-update
   HARNESS=<harness>` and review the diff.
3. Update the versions named in
   [docs/research/harness-agent-formats.md](docs/research/harness-agent-formats.md) if the
   behavior recorded there changed, and commit everything together.

## Layout

| Package | Purpose |
| --- | --- |
| `cmd/agnthub` | the CLI entry point |
| `agent` | Agent definitions, Variants, Dependencies, frontmatter parsing and rendering |
| `source` | source locators (local, GitHub, Git), inert trees and immutable snapshots |
| `discovery` | bounded, inert discovery of portable, native and `agnthub.yaml` agents |
| `harness` | Harness adapters, conversion through the shared semantics model, Harness Settings |
| `resolve` | the Agent and Skill Dependency closure |
| `skills` | skill installer configuration, Skill Dependency install and verification |
| `install` | planning, assessment and atomic application of installs |
| `state` | the project lock, ownership records and installation identities |
| `adoption` | anonymous install reports to the registry |
| `platform/env` | the process environment, behind an interface tests can replace |
| `internal/cli` | the cobra commands and terminal UI |
| `internal/uninstall` | removal and dependency cleanup |
| `e2e` | the containerized end-to-end suite against the real Harness CLIs |

The top-level packages are importable by other modules (the registry service uses them to
assess repositories exactly as the CLI would install them). Keep their exported API stable;
prefer additive changes and call out any breaking one in the pull request.

## Conventions

- Lint findings are fixed, not silenced. A `//nolint` needs the linter name and a reason.
- `.golangci.yml` forbids mutexes (use `github.com/puzpuzpuz/xsync/v4` or `sync/atomic`),
  `fmt.Print*` (write to an injected `io.Writer`), `encoding/json` (use
  `github.com/bytedance/sonic`), `os.Getenv` and friends (use `platform/env`) and `time.Sleep`.
- Tests assert behavior at boundaries (refusals, precedence, what is written), not wording or
  wiring. Tests never touch a real Harness configuration: use a temporary `HOME` and the
  Harness's config-directory variables.
- Harness format changes cite the Harness's own documentation or source in
  [docs/research/harness-agent-formats.md](docs/research/harness-agent-formats.md), and should be
  checked against the real Harness where it can load agents without signing in.
- Commit messages follow [Conventional Commits](https://www.conventionalcommits.org/)
  (`feat(harness): …`, `fix(source): …`) and explain why.

## Releasing

Maintainers release from a clean, pushed `main`:

```sh
git tag vX.Y.Z && git push origin vX.Y.Z
```

The [release workflow](.github/workflows/release.yml) tests the tag, builds reproducible
archives with GoReleaser (static binaries, commit-timestamped, root-owned entries) and publishes
the GitHub Release with `SHA256SUMS`. Then embed the checksums in the npm launcher:

```sh
git switch --detach vX.Y.Z
make release           # rebuilds the same archives locally; writes checksums and version into npm/agnthub
make release-verify    # dist/SHA256SUMS must equal the published release's
make npm-pack          # dist/agnthub-X.Y.Z.tgz, the exact package to publish
git switch main       # carries the npm/agnthub changes over
git commit -m "release: embed vX.Y.Z checksums in the npm launcher" npm/agnthub && git push
npm publish dist/agnthub-X.Y.Z.tgz --access public   # needs `npm login` as the package owner
```

Nothing in CI publishes to npm. `make release-publish` uploads `dist/` by hand if the workflow
cannot.

## License

By contributing you agree that your contributions are licensed under the [MIT License](LICENSE).
