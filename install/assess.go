package install

import (
	"errors"

	"github.com/agnthub-sh/agnthub/agent"
	"github.com/agnthub-sh/agnthub/harness"
)

// Status is how an Agent can be represented for one Harness, independent of scope,
// settings and local files.
type Status string

// Assessment statuses.
const (
	// StatusNative installs the authored Variant, which the Harness accepts.
	StatusNative Status = "native"
	// StatusGenerated converts the portable core or another Harness's Variant.
	StatusGenerated Status = "generated"
	// StatusIncompatible has no faithful representation for the Harness.
	StatusIncompatible Status = "incompatible"
	// StatusInvalid has an authored Variant or portable core the Harness rejects.
	StatusInvalid Status = "invalid"
)

// Assessment is the outcome of rendering one Agent for one Harness.
type Assessment struct {
	Harness agent.HarnessID
	Status  Status
	// Reason explains an incompatible or invalid status.
	Reason string
	// SourcePath is the definition file the artifact comes from.
	SourcePath string
	// Notes are the advisories a plan would show: harmless omissions and
	// Dependency semantics.
	Notes []string
	// Reduced lists capabilities the installed agent goes without because the
	// Harness cannot grant them within its restrictions, e.g. "web search".
	Reduced []string
	// OptIn names the flag, without its dashes, that installing it requires
	// (OptInWithoutShell), or "" when it installs without one.
	OptIn string
	// Dependencies are the Agent's common Dependencies plus its Variant's.
	Dependencies agent.Dependencies
}

// OptInWithoutShell is the opt-in (agnthub add --allow-without-shell) to
// install an Agent that loses the shell access it uses.
const OptInWithoutShell = "allow-without-shell"

// Assess renders def for harnessID exactly as Planner.Build does (an explicit
// Variant first, never falling back from an invalid one) without resolving a
// destination or reading local state.
func Assess(
	registry *harness.Registry,
	def *agent.Definition,
	harnessID agent.HarnessID,
) Assessment {
	out := Assessment{Harness: harnessID, Dependencies: dependencies(def, harnessID)}
	installer, err := registry.Installer(harnessID)
	if err != nil {
		out.Status, out.Reason = StatusIncompatible, err.Error()
		return out
	}
	artifact, sourcePath, _, err := render(registry, installer, def)
	if err != nil {
		var incompatible *harness.IncompatibleError
		var invalid *harness.InvalidVariantError
		switch {
		case errors.As(err, &incompatible):
			out.Status, out.Reason = StatusIncompatible, incompatible.Reason
		case errors.As(err, &invalid):
			out.Status, out.Reason = StatusInvalid, invalid.Error()
		default:
			out.Status, out.Reason = StatusInvalid, err.Error()
		}
		return out
	}
	out.Status, out.SourcePath, out.Notes = StatusGenerated, sourcePath, artifact.Notes
	out.Reduced = artifact.ReducedNames()
	if artifact.WithoutShell() {
		out.OptIn = OptInWithoutShell
	}
	if def.Variant(harnessID) != nil {
		out.Status = StatusNative
	}
	if advisor, ok := installer.(harness.DependencyAdvisor); ok {
		out.Notes = append(out.Notes, advisor.DependencyNotes(out.Dependencies)...)
	}
	return out
}
