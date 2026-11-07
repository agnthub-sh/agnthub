# Attribute converted agents to their source and mark them as modified

Status: accepted (decided by the operator on 2026-11-07, after a legal review of the registry's terms).

A converted agent file is an adaptation of someone else's work. Permissive licenses ask for the copyright notice with every copy (MIT, BSD, ISC), Apache-2.0 for a pointer to the license, and CC-BY-4.0 for an indication that the work was modified. agnthub's conversions are modifications, and the registry's `install_diff` hands the same bytes out, so each generated file now says where it came from.

**The header is comments, at the top of the file's metadata.** Markdown agents get YAML comment lines right after the frontmatter's opening `---`; Codex agents get `#` comment lines at the top of the `.toml` file. YAML and TOML parsers drop comments and Cursor's line reader skips lines starting with `#`, so every harness reads the agent exactly as before, and the comments stay out of the instructions sent to the model (an HTML comment in the body would not). Each generated file is still validated as that harness's native agent after the header is added. The lines:

```
# Adapted from <path> in <repository>
# Modified by agnthub: converted to the <harness> agent format
# <copyright lines of the repository's root license file, at most five>
# License: <the agent's declared license; >see <license file> in <repository>
```

A local directory is not named (its path is the user's own); a source without a license file says `# No license file found`. Text from the repository is kept to one printable line each, so it cannot end the comment. The revision and ref are left out so a new commit that does not touch the agent does not change the file.

**Discovery reads the license file.** The first of `LICENSE`, `LICENCE`, `COPYING` (bare, `.md`, `.txt`, any case) at the root, up to 64 KiB, gives `discovery.Inventory.License` and every `agent.Definition.License`: its path and its copyright lines (lines starting `Copyright`, `©` or `(c)`, without license templates such as `Copyright [yyyy]`). Reading it never fails a scan. There is no license detection here; the registry keeps using GitHub's.

**Authored Variants are untouched.** They install byte for byte, as before; the header is only for files agnthub wrote.

Consequences: converter `v6`. `check` reports earlier generated files as a converter change and `update` rewrites them. A lock written by `v5` or earlier still restores byte for byte: the restore plans that artifact without the header and records it as `v5`, so `restore` stays the reproducible, no-surprise command. The registry's assessments are unchanged (statuses and notes do not depend on the bytes); its `install_diff` shows the header once it requires this release.
