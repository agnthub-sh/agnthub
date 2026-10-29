// Package harness defines Harness adapters: native layouts, identity rules,
// settings, destinations and conservative conversion of the portable core.
package harness

import (
	"errors"
	"fmt"
	"sort"

	"github.com/heynemann/agnthub/internal/agent"
	"github.com/heynemann/agnthub/internal/platform/env"
)

// Scope is an installation scope.
type Scope string

// Installation scopes.
const (
	ScopeProject Scope = "project"
	ScopeGlobal  Scope = "global"
)

// Context carries the local environment adapters resolve paths against.
type Context struct {
	Env env.Env
	// ProjectRoot is the absolute project directory for project-scope installs.
	ProjectRoot string
	// LookPath finds executables; nil disables executable detection.
	LookPath func(name string) (string, error)
}

// NativeLayout describes where a Harness keeps native Agent files in a source repository.
type NativeLayout struct {
	// Dir is the repository-relative native directory, e.g. ".claude/agents".
	Dir string
	// Recursive reports whether subdirectories are scanned.
	Recursive bool
	// Suffixes lists accepted file-name suffixes; the first is used for installs.
	Suffixes []string
}

// NativeAgent is the identity a Harness derives from one native file.
type NativeAgent struct {
	ID          string
	Description string
}

// SettingSpec declares one supported Harness Setting.
type SettingSpec struct {
	Key         string
	Description string
	Validate    func(value string) error
}

// Settings holds the effective Harness Settings for one Harness.
type Settings map[string]string

// Destination is a resolved native Agent directory.
type Destination struct {
	// Dir is the absolute Agent directory.
	Dir string
	// Label describes the destination for humans, e.g. "project" or "profile hep".
	Label string
	// Shared reports a project directory shared by every Harness Profile.
	Shared bool
}

// Artifact is the file content to install for one Agent and Harness.
type Artifact struct {
	// FileName is the name inside the destination directory.
	FileName string
	Content  []byte
	// Generated reports conversion from the portable core rather than an authored Variant.
	Generated bool
	// Converter identifies the conversion contract for generated content.
	Converter string
	// Notes lists harmless omissions or advisories.
	Notes []string
}

// IncompatibleError reports that an Agent cannot be represented for a Harness
// without losing declared behavior.
type IncompatibleError struct {
	Harness agent.HarnessID
	Reason  string
}

func (e *IncompatibleError) Error() string {
	return fmt.Sprintf("incompatible with %s: %s", e.Harness, e.Reason)
}

// InvalidVariantError reports an explicitly authored Variant that the Harness rejects.
type InvalidVariantError struct {
	Harness agent.HarnessID
	Path    string
	Reason  string
}

func (e *InvalidVariantError) Error() string {
	return fmt.Sprintf("invalid %s variant %s: %s", e.Harness, e.Path, e.Reason)
}

// Adapter describes a Harness for discovery and settings.
type Adapter interface {
	ID() agent.HarnessID
	Name() string
	Layout() NativeLayout
	// Identify validates a native file and returns its identity.
	Identify(path string, raw []byte) (NativeAgent, error)
	// Settings lists the Harness Settings the adapter accepts.
	Settings() []SettingSpec
}

// Installer is an Adapter that can place Agents.
type Installer interface {
	Adapter
	// Destination resolves the Agent directory for a scope and settings.
	Destination(ctx Context, scope Scope, settings Settings) (Destination, error)
	// Native returns the artifact for an authored Variant, validating it for id.
	Native(id string, variant *agent.VariantFile) (Artifact, error)
	// Convert generates an artifact from the portable core or refuses.
	Convert(id string, portable *agent.Document) (Artifact, error)
	// Detect reports whether the Harness appears to be available locally.
	Detect(ctx Context) bool
}

// DependencyAdvisor is an optional Installer extension for Harnesses where an
// installed Dependency does not carry the runtime meaning users may assume.
type DependencyAdvisor interface {
	// DependencyNotes returns plan notes for an Agent declaring deps.
	DependencyNotes(deps agent.Dependencies) []string
}

// Setting sources reported by SettingsReporter besides environment variable names.
const (
	SettingExplicit      = "explicit"
	SettingNativeDefault = "native default"
)

// EffectiveSetting is one Harness Setting value in effect for a plan.
type EffectiveSetting struct {
	Key   string
	Value string
	// Source is SettingExplicit for an --extra value, SettingNativeDefault, or the
	// environment variable the native default was detected from.
	Source string
}

// SettingsReporter is an optional Installer extension reporting the Settings in
// effect, including native defaults detected when no --extra was given. It returns
// nil when the settings cannot be resolved; Destination reports why.
type SettingsReporter interface {
	EffectiveSettings(ctx Context, settings Settings) []EffectiveSetting
}

// Registry holds every known Adapter.
type Registry struct {
	adapters map[agent.HarnessID]Adapter
}

// NewRegistry builds a registry from adapters.
func NewRegistry(adapters ...Adapter) *Registry {
	registry := &Registry{adapters: make(map[agent.HarnessID]Adapter, len(adapters))}
	for _, adapter := range adapters {
		registry.adapters[adapter.ID()] = adapter
	}
	return registry
}

// Default returns the registry of built-in adapters.
func Default() *Registry {
	return NewRegistry(builtins()...)
}

// Get returns the Adapter for id.
func (r *Registry) Get(id agent.HarnessID) (Adapter, bool) {
	adapter, ok := r.adapters[id]
	return adapter, ok
}

// Installer returns the Installer for id, or an error when installation is unsupported.
func (r *Registry) Installer(id agent.HarnessID) (Installer, error) {
	adapter, ok := r.adapters[id]
	if !ok {
		return nil, fmt.Errorf("unknown harness %q", id)
	}
	installer, ok := adapter.(Installer)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrInstallUnsupported, adapter.Name())
	}
	return installer, nil
}

// ErrInstallUnsupported reports a Harness without installation support.
var ErrInstallUnsupported = errors.New("installation is not supported for this harness")

// All returns adapters in display order.
func (r *Registry) All() []Adapter {
	adapters := make([]Adapter, 0, len(r.adapters))
	for _, id := range agent.AllHarnesses {
		if adapter, ok := r.adapters[id]; ok {
			adapters = append(adapters, adapter)
		}
	}
	return adapters
}

// Installers returns adapters that can install, in display order.
func (r *Registry) Installers() []Installer {
	installers := make([]Installer, 0, len(r.adapters))
	for _, adapter := range r.All() {
		if installer, ok := adapter.(Installer); ok {
			installers = append(installers, installer)
		}
	}
	return installers
}

// SettingKeys returns the sorted setting keys an adapter supports.
func SettingKeys(adapter Adapter) []string {
	specs := adapter.Settings()
	keys := make([]string, 0, len(specs))
	for _, spec := range specs {
		keys = append(keys, spec.Key)
	}
	sort.Strings(keys)
	return keys
}
