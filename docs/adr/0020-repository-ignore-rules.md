# Let repository owners ask agnthub to ignore their repository or some paths

Status: accepted (2026-11-08).

agnthub reads agents from any public repository: the registry indexes the repositories users submit, and anyone can `agnthub add` any source. Some owners do not want that for all of their content: a repository of examples, test fixtures that look like agents, internal prompts, or a whole repository whose agents are not meant for reuse. They had no way to say so; agnthub.sh issue #46 asks for one.

**A source declares what agnthub ignores, and the shared discovery package applies it.** The rules come from two places, unioned:

- `.agnthub-ignore` at the source root: one path per line; blank lines and lines starting with `#` are skipped.
- The `ignore` key of `agnthub.yaml`: `ignore: all`, or a list of paths. A manifest that only sets `ignore` (and optionally `categories`) needs no `schemaVersion` and keeps normal layout discovery; a manifest that declares `agents` still needs `schemaVersion: 1`.

Paths are literal and root-relative: a leading `/` or `./` and a trailing `/` are normalized, and a directory covers everything under it. Glob characters (`*`, `?`, `[`) and paths leaving the root are rejected, as is `ignore: []`; an invalid `.agnthub-ignore` is an `IgnoreError` that rejects the whole source, as an invalid manifest's `ManifestError` does. Literal paths keep the rules unambiguous to the CLI, the indexer and the owner, and cover the cases owners describe (a folder, a file, everything); globs can come later without changing what existing files mean. A `.agnthub-ignore` that lists no paths (empty, comments only, or `/`) ignores the whole source: creating the empty file is the obvious way to opt out, so it must not silently mean "ignore nothing". An agent `agnthub.yaml` declares under an ignored path rejects the manifest: the author contradicts themselves, and guessing which half they meant would either install what they asked to ignore or drop what they declared.

Discovery never reads or fetches ignored paths, so they cost no budget and their content cannot reach a plan; a whole-source ignore leaves an inventory with no agents, problems or license. `Inventory.Ignore` always reports the declared rules, applied or not. The rules live in the discovery package the CLI and the agnthub.sh indexer share, so both read a source the same way: the registry does not list what `agnthub add` would not offer, and the reverse.

**The CLI can override the rules.** The owner's wish governs what agnthub offers, not what a user may do with a repository they can already read: `agnthub add --include-ignored` reads ignored paths as if no rules were set, in the root source and in every dependency source it resolves. Without it, `add` of an ignored source fails naming the source and the flag, an agent `add` cannot find while paths are ignored gets a hint naming them, and `add --list` (and `--list --json`'s `ignore` object: `all`, `paths`, `applied`) shows the rules. The hosted preview mirrors `agnthub add` without the flag.

**Lifecycle commands always include ignored paths.** Ignore rules decide what can be newly added; they never break what is already installed. `check`, `update` and `restore` discover the sources of locked agents, and the dependencies they resolve, with ignored paths included. An owner who opts out later stops new installs and registry listing, while every existing install keeps checking, updating and restoring exactly as before instead of reporting its agent as gone.

Consequences: owners have one file to opt out with and one manifest key for authors who already keep an `agnthub.yaml`. CLIs before 0.11.0 do not know the rules: they ignore `.agnthub-ignore` and reject a manifest with the `ignore` key as an unknown key. Rules are per source revision, so an owner's change applies to the commits that carry it; the registry stops listing the agents when it next indexes the repository.
