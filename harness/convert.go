package harness

import (
	"errors"

	"github.com/agnthub-sh/agnthub/agent"
)

// Generate converts def for installer when it has no authored Variant for that
// Harness. Sources are tried in a fixed order: the portable core, then every
// other authored Variant, Claude Code first and the rest in display order. The
// first source the target can represent wins; otherwise the first refusal is
// returned. It returns the artifact plus the path and bytes of the source file.
func (r *Registry) Generate(
	installer Installer,
	def *agent.Definition,
) (Artifact, string, []byte, error) {
	target := installer.ID()
	var first error
	keep := func(err error) {
		if first == nil {
			first = err
		}
	}
	if def.Portable != nil {
		sem, err := PortableSemantics(target, def.ID, def.Portable)
		if err == nil {
			var artifact Artifact
			if artifact, err = installer.Convert(sem); err == nil {
				return artifact, def.Portable.Path, def.Portable.Raw, nil
			}
		}
		keep(err)
	}
	for _, source := range agent.AllHarnesses {
		variant := def.Variant(source)
		if source == target || variant == nil {
			continue
		}
		artifact, err := r.convertVariant(installer, source, def.ID, variant)
		if err == nil {
			return artifact, variant.Path, variant.Raw, nil
		}
		keep(err)
	}
	if first == nil {
		first = &IncompatibleError{
			Harness: target,
			Reason:  "no authored variant and no portable definition",
		}
	}
	return Artifact{}, "", nil, first
}

func (r *Registry) convertVariant(
	installer Installer,
	source agent.HarnessID,
	id string,
	variant *agent.VariantFile,
) (Artifact, error) {
	sourceInstaller, err := r.Installer(source)
	if err != nil {
		return Artifact{}, err
	}
	sem, err := sourceInstaller.Semantics(id, variant)
	if err != nil {
		var invalid *InvalidVariantError
		if errors.As(err, &invalid) {
			return Artifact{}, &IncompatibleError{
				Harness: installer.ID(),
				Reason:  "cannot convert: " + invalid.Error(),
			}
		}
		return Artifact{}, err
	}
	return installer.Convert(sem)
}
