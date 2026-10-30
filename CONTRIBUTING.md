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

```sh
make build        # bin/agnthub
make fmt          # gofumpt, goimports, golines
make lint         # must report 0 issues
make test         # go test -race ./...
make npm-test     # npm launcher tests
make cross        # compile every release platform
```

CI runs the same checks on every push and pull request.

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
