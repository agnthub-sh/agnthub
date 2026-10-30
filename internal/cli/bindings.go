package cli

import (
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/heynemann/agnthub/agent"
	"github.com/heynemann/agnthub/harness"
	"github.com/heynemann/agnthub/state"
)

// Where a binding's Settings came from, as shown next to effective settings.
const (
	bindingRecorded = "recorded binding"
	bindingExplicit = "explicit"
	bindingChosen   = "chosen"
	bindingDefault  = "native default"
)

// boundArtifact is one locked artifact with its absolute path and the user-local
// ownership records of its Harness.
type boundArtifact struct {
	agent    *state.LockedAgent
	artifact *state.LockedArtifact
	path     string
	records  []state.Record
}

// bindingContext is one Harness binding: the locked artifacts a lifecycle
// command handles with the same Harness Settings.
type bindingContext struct {
	harness  agent.HarnessID
	settings harness.Settings
	// origin is one of the binding* labels, or "" for a Harness without Settings.
	origin    string
	artifacts []boundArtifact
}

// bindingPolicy decides how a lifecycle command picks each artifact's binding.
type bindingPolicy struct {
	// explicit holds --extra Settings per Harness.
	explicit map[agent.HarnessID]harness.Settings
	// mutating commands never guess: an ambiguous or missing binding is asked
	// for interactively and refused otherwise.
	mutating    bool
	interactive bool
	// fresh lets an explicit binding that no record names bind a new destination
	// (restore); otherwise it is refused as a retarget (update).
	fresh bool
}

// bindingResolver picks bindings, asking each distinct question at most once.
type bindingResolver struct {
	app    *App
	policy bindingPolicy
	asked  map[string]harness.Settings
}

// bindContexts groups the artifacts of the included locked Agents by the binding
// each one resolves to: explicit --extra Settings, else the one recorded binding,
// else (for mutating commands) a prompt or an actionable refusal.
func (a *App) bindContexts(
	ctx context.Context,
	scope harness.Scope,
	lock *state.Lock,
	ownership *state.Ownership,
	include func(*state.LockedAgent) bool,
	policy bindingPolicy,
) ([]*bindingContext, error) {
	resolver := &bindingResolver{app: a, policy: policy, asked: map[string]harness.Settings{}}
	byKey := map[string]*bindingContext{}
	var contexts []*bindingContext
	for i := range lock.Agents {
		locked := &lock.Agents[i]
		if !include(locked) {
			continue
		}
		for j := range locked.Artifacts {
			artifact := &locked.Artifacts[j]
			bound := boundArtifact{
				agent: locked, artifact: artifact, path: a.artifactPath(scope, artifact.Path),
			}
			for _, record := range ownership.ForPath(bound.path) {
				if record.Harness == artifact.Harness {
					bound.records = append(bound.records, record)
				}
			}
			settings, origin, err := resolver.resolve(ctx, bound)
			if err != nil {
				return nil, err
			}
			key := artifact.Harness + "|" + state.SettingsKey(settings)
			group, ok := byKey[key]
			if !ok {
				group = &bindingContext{
					harness:  agent.HarnessID(artifact.Harness),
					settings: settings,
					origin:   origin,
				}
				byKey[key] = group
				contexts = append(contexts, group)
			}
			group.artifacts = append(group.artifacts, bound)
		}
	}
	return contexts, nil
}

// artifactPath is the absolute path of a locked artifact path.
func (a *App) artifactPath(scope harness.Scope, path string) string {
	if scope == harness.ScopeProject && !filepath.IsAbs(path) {
		return filepath.Join(a.WorkDir, filepath.FromSlash(path))
	}
	return path
}

// distinctSettings returns the distinct Settings recorded for an artifact, sorted.
func distinctSettings(records []state.Record) []harness.Settings {
	var keys []string
	byKey := map[string]harness.Settings{}
	for _, record := range records {
		key := state.SettingsKey(record.Settings)
		if _, ok := byKey[key]; ok {
			continue
		}
		byKey[key] = record.Settings
		keys = append(keys, key)
	}
	slices.Sort(keys)
	bindings := make([]harness.Settings, 0, len(keys))
	for _, key := range keys {
		bindings = append(bindings, byKey[key])
	}
	return bindings
}

func (r *bindingResolver) resolve(
	ctx context.Context,
	bound boundArtifact,
) (harness.Settings, string, error) {
	id := agent.HarnessID(bound.artifact.Harness)
	recorded := distinctSettings(bound.records)
	if explicit, ok := r.policy.explicit[id]; ok {
		key := state.SettingsKey(explicit)
		named := len(recorded) == 0
		for _, settings := range recorded {
			named = named || state.SettingsKey(settings) == key
		}
		if !named && !r.policy.fresh {
			return nil, "", fmt.Errorf(
				"%s is recorded for %s with %s; --extra %s names another binding, and "+
					"agnthub never retargets an installation (install into that binding "+
					"with `agnthub add`)",
				bound.path, id, describeBindings(recorded), extraFlags(id, explicit),
			)
		}
		return explicit, bindingExplicit, nil
	}
	switch {
	case len(recorded) == 1:
		return recorded[0], bindingRecorded, nil
	case len(recorded) > 1 && !r.policy.mutating:
		return recorded[0], bindingRecorded, nil
	case len(recorded) > 1:
		return r.ask(ctx, id, recorded, fmt.Sprintf(
			"%s is shared by several %s bindings (%s)", bound.path, id, describeBindings(recorded),
		))
	case !r.app.hasSettings(id):
		return nil, "", nil
	case !r.policy.mutating:
		return nil, bindingDefault, nil
	}
	installer, err := r.app.Registry.Installer(id)
	if err != nil {
		return nil, "", err
	}
	var candidate harness.Settings
	if reporter, ok := installer.(harness.SettingsReporter); ok {
		effective := reporter.EffectiveSettings(r.app.harnessContext(), nil)
		if len(effective) > 0 {
			candidate = harness.Settings{}
			for _, setting := range effective {
				candidate[setting.Key] = setting.Value
			}
		}
	}
	var candidates []harness.Settings
	if candidate != nil {
		candidates = append(candidates, candidate)
	}
	return r.ask(ctx, id, candidates, fmt.Sprintf(
		"no %s binding is recorded for %s on this machine", id, bound.path,
	))
}

// ask lets an interactive user choose one binding; noninteractive runs fail with
// the --extra flag that would decide it.
func (r *bindingResolver) ask(
	ctx context.Context,
	id agent.HarnessID,
	candidates []harness.Settings,
	why string,
) (harness.Settings, string, error) {
	keys := make([]string, 0, len(candidates))
	for _, settings := range candidates {
		keys = append(keys, state.SettingsKey(settings))
	}
	question := string(id) + "\x00" + strings.Join(keys, "\x00")
	if answer, ok := r.asked[question]; ok {
		return answer, bindingChosen, nil
	}
	example := extraFlags(id, harness.Settings{r.app.settingKey(id): "<value>"})
	if !r.policy.interactive || len(candidates) == 0 {
		return nil, "", fmt.Errorf(
			"%s; choose the binding explicitly with --extra %s",
			why,
			example,
		)
	}
	options := make([]Option, 0, len(candidates))
	for i, settings := range candidates {
		options = append(options, Option{Value: keys[i], Label: formatSettings(settings)})
	}
	values, err := r.app.Prompter.MultiSelect(ctx,
		fmt.Sprintf("Which %s binding should agnthub use?", id),
		why+".\nSelect exactly one; agnthub never moves an installation between bindings.",
		options)
	if err != nil {
		return nil, "", err
	}
	if len(values) != 1 {
		return nil, "", fmt.Errorf(
			"%s; select exactly one binding or pass --extra %s",
			why,
			example,
		)
	}
	answer := candidates[slices.Index(keys, values[0])]
	r.asked[question] = answer
	return answer, bindingChosen, nil
}

// hasSettings reports whether the Harness declares any Harness Settings.
func (a *App) hasSettings(id agent.HarnessID) bool {
	adapter, ok := a.Registry.Get(id)
	return ok && len(adapter.Settings()) > 0
}

// settingKey is the first Setting the Harness declares, for help text.
func (a *App) settingKey(id agent.HarnessID) string {
	if adapter, ok := a.Registry.Get(id); ok {
		if keys := harness.SettingKeys(adapter); len(keys) > 0 {
			return keys[0]
		}
	}
	return "<key>"
}

func describeBindings(bindings []harness.Settings) string {
	parts := make([]string, 0, len(bindings))
	for _, settings := range bindings {
		if len(settings) == 0 {
			parts = append(parts, "no settings")
			continue
		}
		parts = append(parts, formatSettings(settings))
	}
	return strings.Join(parts, "; ")
}

// extraFlags renders Settings as --extra values, e.g. "omp.profile=hep".
func extraFlags(id agent.HarnessID, settings harness.Settings) string {
	parts := make([]string, 0, len(settings))
	for _, key := range slices.Sorted(maps.Keys(settings)) {
		parts = append(parts, string(id)+"."+key+"="+settings[key])
	}
	return strings.Join(parts, " --extra ")
}
