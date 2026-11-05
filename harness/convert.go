package harness

import (
	"errors"

	"github.com/agnthub-sh/agnthub/agent"
)

// Generate converts def for installer when it has no authored Variant for that
// Harness. Sources are tried in a fixed order: the portable core, then every
// other authored Variant, Claude Code first and the rest in display order. The
// first source that converts best wins: exactly, under the rules before ADR
// 0012; else through a reading ADR 0012 added (Semantics.relaxedFor); else
// with reduced capability; else without the shell access the Agent uses (the
// plan then needs the user's opt-in). So a source that converts as before
// keeps winning, and an install never gains or loses capabilities because
// another source became convertible. Otherwise the first refusal is returned.
// It returns the artifact plus the path and bytes of the source file.
func (r *Registry) Generate(
	installer Installer,
	def *agent.Definition,
) (Artifact, string, []byte, error) {
	target := installer.ID()
	var first error
	var best *generated
	// try keeps the conversion if it ranks better than the best so far and
	// reports whether the search is over: a conversion ranking first.
	try := func(sem *Semantics, artifact Artifact, path string, raw []byte, err error) bool {
		if err != nil {
			if first == nil {
				first = err
			}
			return false
		}
		rank := conversionRank(target, sem, artifact)
		if best == nil || rank < best.rank {
			best = &generated{artifact: artifact, path: path, raw: raw, rank: rank}
		}
		return rank == rankExact
	}
	done := false
	if def.Portable != nil {
		sem, err := PortableSemantics(target, def.ID, def.Portable)
		var artifact Artifact
		if err == nil {
			artifact, err = installer.Convert(sem)
		}
		done = try(sem, artifact, def.Portable.Path, def.Portable.Raw, err)
	}
	for _, source := range agent.AllHarnesses {
		variant := def.Variant(source)
		if done || source == target || variant == nil {
			continue
		}
		sem, artifact, err := r.convertVariant(installer, source, def.ID, variant)
		done = try(sem, artifact, variant.Path, variant.Raw, err)
	}
	if best != nil {
		return best.artifact, best.path, best.raw, nil
	}
	if first == nil {
		first = &IncompatibleError{
			Harness: target,
			Reason:  "no authored variant and no portable definition",
		}
	}
	return Artifact{}, "", nil, first
}

// Conversion ranks, best first.
const (
	rankExact = iota
	rankRelaxed
	rankReduced
	rankWithoutShell
)

// conversionRank orders successful conversions for Generate.
func conversionRank(target agent.HarnessID, sem *Semantics, artifact Artifact) int {
	switch {
	case artifact.WithoutShell():
		return rankWithoutShell
	case len(artifact.Reduced) > 0:
		return rankReduced
	case sem.relaxedFor(target):
		return rankRelaxed
	}
	return rankExact
}

// generated is one successful conversion, its source file and its rank.
type generated struct {
	artifact Artifact
	path     string
	raw      []byte
	rank     int
}

func (r *Registry) convertVariant(
	installer Installer,
	source agent.HarnessID,
	id string,
	variant *agent.VariantFile,
) (*Semantics, Artifact, error) {
	sourceInstaller, err := r.Installer(source)
	if err != nil {
		return nil, Artifact{}, err
	}
	sem, err := sourceInstaller.Semantics(id, variant)
	if err != nil {
		var invalid *InvalidVariantError
		if errors.As(err, &invalid) {
			return nil, Artifact{}, &IncompatibleError{
				Harness: installer.ID(),
				Reason:  "cannot convert: " + invalid.Error(),
			}
		}
		return nil, Artifact{}, err
	}
	artifact, err := installer.Convert(sem)
	return sem, artifact, err
}
