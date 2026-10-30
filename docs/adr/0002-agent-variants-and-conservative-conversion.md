# Keep one agent identity across harness variants and require conservative conversion

A published agent has one identity with potentially different harness-specific variants; variants are not separate agents or revisions over time. An authored variant may supply a different prompt or native configuration, rather than requiring every harness to use a conversion of one portable definition. Explicit Git refs select source revisions, resolved content is locked, and the current [registry contract](../registry-design.md) defines selection and update behavior.

Automatic conversion may translate supported semantics and report harmless metadata omissions, but must refuse a target if it cannot preserve a declared behavioral or safety constraint. A suitable authored variant or corrected definition is then required. Broad harness coverage is a product goal, not a promise that every agent can install on every harness; best-effort lossy conversion and blanket permission to drop unknown fields were rejected.

An explicitly mapped harness-specific variant takes precedence over conversion of the portable definition. An invalid explicit variant produces an error rather than silently falling back to converted content.

Conversion sources and the shared semantics model are decided in [ADR 0009](0009-cross-harness-conversion-through-shared-semantics.md): without a Variant for the target, the portable core is converted first, then any other authored Variant.
