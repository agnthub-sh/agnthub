package discovery

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"sort"

	"go.yaml.in/yaml/v3"

	"github.com/heynemann/agnthub/internal/agent"
	"github.com/heynemann/agnthub/internal/source"
)

type manifestFile struct {
	SchemaVersion int                      `yaml:"schemaVersion"`
	Agents        map[string]manifestAgent `yaml:"agents"`
}

type manifestAgent struct {
	Portable     string                     `yaml:"portable"`
	Variants     map[string]manifestVariant `yaml:"variants"`
	Dependencies manifestDependencies       `yaml:"dependencies"`
}

type manifestVariant struct {
	Path         string               `yaml:"path"`
	Dependencies manifestDependencies `yaml:"dependencies"`
}

type manifestDependencies struct {
	Agents []manifestAgentDependency `yaml:"agents"`
	Skills []manifestSkillDependency `yaml:"skills"`
}

type manifestAgentDependency struct {
	Agent  string `yaml:"agent"`
	Source string `yaml:"source"`
}

type manifestSkillDependency struct {
	Source     string `yaml:"source"`
	Skill      string `yaml:"skill"`
	MinVersion string `yaml:"minVersion"`
	MaxVersion string `yaml:"maxVersion"`
}

// ManifestError reports an invalid agnthub.yaml; the whole source is rejected.
type ManifestError struct {
	Reason string
}

func (e *ManifestError) Error() string {
	return ManifestFile + ": " + e.Reason
}

func manifestErr(format string, args ...any) error {
	return &ManifestError{Reason: fmt.Sprintf(format, args...)}
}

func decodeManifest(raw []byte) (*manifestFile, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	var manifest manifestFile
	if err := decoder.Decode(&manifest); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, manifestErr("empty manifest")
		}
		return nil, manifestErr("%v", err)
	}
	if manifest.SchemaVersion != 1 {
		return nil, manifestErr("unsupported schemaVersion %d (want 1)", manifest.SchemaVersion)
	}
	if len(manifest.Agents) == 0 {
		return nil, manifestErr("declares no agents")
	}
	return &manifest, nil
}

func (s *scanner) fromManifest(raw []byte) (*Inventory, error) {
	manifest, err := decodeManifest(raw)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(manifest.Agents))
	for id := range manifest.Agents {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	inv := &Inventory{Manifest: true}
	for _, id := range ids {
		def, err := s.manifestAgent(id, manifest.Agents[id])
		if err != nil {
			return nil, err
		}
		inv.Agents = append(inv.Agents, def)
	}
	return inv, nil
}

func (s *scanner) manifestAgent(id string, spec manifestAgent) (*agent.Definition, error) {
	if !agent.ValidID(id) {
		return nil, manifestErr("invalid agent identifier %q", id)
	}
	if spec.Portable == "" && len(spec.Variants) == 0 {
		return nil, manifestErr("agent %s declares neither a portable definition nor variants", id)
	}
	def := &agent.Definition{
		ID:           id,
		FromManifest: true,
		Variants:     map[agent.HarnessID]*agent.VariantFile{},
	}
	deps, err := convertDependencies(id, spec.Dependencies)
	if err != nil {
		return nil, err
	}
	def.Dependencies = deps
	if spec.Portable != "" {
		raw, err := s.declaredFile(id, spec.Portable)
		if err != nil {
			return nil, err
		}
		doc, err := agent.ParseMarkdownLenient(spec.Portable, raw)
		if err != nil {
			return nil, manifestErr("agent %s: %v", id, err)
		}
		if declared := doc.String("name"); declared != "" && declared != id {
			return nil, manifestErr(
				"agent %s: portable definition declares name %q",
				id,
				declared,
			)
		}
		def.Portable = doc
		def.Description = doc.String("description")
	}
	harnessNames := make([]string, 0, len(spec.Variants))
	for name := range spec.Variants {
		harnessNames = append(harnessNames, name)
	}
	sort.Strings(harnessNames)
	for _, name := range harnessNames {
		if err := s.manifestVariant(def, name, spec.Variants[name]); err != nil {
			return nil, err
		}
	}
	return def, nil
}

func (s *scanner) manifestVariant(def *agent.Definition, name string, spec manifestVariant) error {
	harnessID, err := agent.ParseHarness(name)
	if err != nil {
		return manifestErr("agent %s: %v", def.ID, err)
	}
	if _, dup := def.Variants[harnessID]; dup {
		return manifestErr("agent %s: duplicate %s variant", def.ID, harnessID)
	}
	if spec.Path == "" {
		return manifestErr("agent %s: %s variant requires a path", def.ID, harnessID)
	}
	raw, err := s.declaredFile(def.ID, spec.Path)
	if err != nil {
		return err
	}
	deps, err := convertDependencies(def.ID, spec.Dependencies)
	if err != nil {
		return err
	}
	cleaned, _ := source.CleanPath(spec.Path)
	def.Variants[harnessID] = &agent.VariantFile{
		Harness:      harnessID,
		Path:         cleaned,
		Raw:          raw,
		Dependencies: deps,
	}
	if adapter, ok := s.registry.Get(harnessID); ok {
		identity, identifyErr := adapter.Identify(cleaned, raw)
		switch {
		case identifyErr != nil:
			s.problems = append(s.problems, Problem{
				Agent: def.ID, Path: cleaned,
				Message: "invalid explicit variant: " + identifyErr.Error(),
			})
		case def.Description == "":
			def.Description = identity.Description
		}
	}
	return nil
}

// declaredFile reads a manifest-referenced path, which must be a regular file.
func (s *scanner) declaredFile(id, name string) ([]byte, error) {
	cleaned, err := source.CleanPath(name)
	if err != nil {
		return nil, manifestErr("agent %s: %v", id, err)
	}
	if err := s.rejectLinkedParents(id, cleaned); err != nil {
		return nil, err
	}
	entry, err := s.tree.Stat(cleaned)
	if err != nil {
		return nil, manifestErr("agent %s: %s: %v", id, cleaned, err)
	}
	if entry.Kind != source.EntryFile {
		return nil, manifestErr("agent %s: %s is a %s, not a regular file", id, cleaned, entry.Kind)
	}
	if err := s.countCandidate(); err != nil {
		return nil, err
	}
	return s.read(cleaned)
}

// rejectLinkedParents refuses paths that traverse a symlink or submodule.
func (s *scanner) rejectLinkedParents(id, cleaned string) error {
	dir := cleaned
	for {
		parent := parentDir(dir)
		if parent == "." {
			return nil
		}
		entry, err := s.tree.Stat(parent)
		if err != nil {
			return manifestErr("agent %s: %s: %v", id, cleaned, err)
		}
		if entry.Kind != source.EntryDir {
			return manifestErr("agent %s: %s traverses a %s", id, cleaned, entry.Kind)
		}
		dir = parent
	}
}

func parentDir(name string) string {
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] == '/' {
			return name[:i]
		}
	}
	return "."
}

func convertDependencies(id string, spec manifestDependencies) (agent.Dependencies, error) {
	var deps agent.Dependencies
	for _, dep := range spec.Agents {
		if !agent.ValidID(dep.Agent) {
			return deps, manifestErr("agent %s: invalid agent dependency %q", id, dep.Agent)
		}
		if dep.Source == "" && dep.Agent == id {
			return deps, manifestErr("agent %s depends on itself", id)
		}
		deps.Agents = append(
			deps.Agents,
			agent.AgentDependency{Agent: dep.Agent, Source: dep.Source},
		)
	}
	for _, dep := range spec.Skills {
		if dep.Source == "" || dep.Skill == "" {
			return deps, manifestErr("agent %s: skill dependency requires source and skill", id)
		}
		deps.Skills = append(deps.Skills, agent.SkillDependency{
			Source:     dep.Source,
			Skill:      dep.Skill,
			MinVersion: dep.MinVersion,
			MaxVersion: dep.MaxVersion,
		})
	}
	return deps, nil
}
