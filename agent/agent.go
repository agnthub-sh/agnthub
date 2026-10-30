// Package agent holds the source-side domain model: logical Agents, their portable
// core, authored Harness Variants and declared Dependencies.
package agent

import (
	"fmt"
	"regexp"
)

// HarnessID identifies a Harness (a host application that loads Agents).
type HarnessID string

// Supported Harness identifiers.
const (
	Claude   HarnessID = "claude"
	OpenCode HarnessID = "opencode"
	Codex    HarnessID = "codex"
	Copilot  HarnessID = "copilot"
	Cursor   HarnessID = "cursor"
	Gemini   HarnessID = "gemini"
	OMP      HarnessID = "omp"
)

// AllHarnesses lists every supported Harness in display order.
var AllHarnesses = []HarnessID{Claude, OpenCode, Codex, Copilot, Cursor, Gemini, OMP}

var harnessAliases = map[string]HarnessID{
	"claude":         Claude,
	"claude-code":    Claude,
	"opencode":       OpenCode,
	"codex":          Codex,
	"copilot":        Copilot,
	"copilot-cli":    Copilot,
	"github-copilot": Copilot,
	"cursor":         Cursor,
	"gemini":         Gemini,
	"gemini-cli":     Gemini,
	"omp":            OMP,
}

// ParseHarness resolves a user-supplied Harness name, accepting documented aliases.
func ParseHarness(name string) (HarnessID, error) {
	if id, ok := harnessAliases[name]; ok {
		return id, nil
	}
	return "", fmt.Errorf("unknown harness %q (supported: %v)", name, AllHarnesses)
}

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// ValidID reports whether id is an acceptable logical Agent identifier: lowercase
// letters, digits, hyphen and underscore, starting with a letter or digit.
func ValidID(id string) bool {
	return idPattern.MatchString(id)
}

// Document is a Markdown file with YAML frontmatter.
type Document struct {
	// Path is the repository-relative path of the file.
	Path string
	// Raw holds the exact file bytes.
	Raw []byte
	// Frontmatter is the decoded YAML frontmatter.
	Frontmatter map[string]any
	// Body is the Markdown after the frontmatter.
	Body string
}

// String returns a frontmatter string field, or "" when absent or not a string.
func (d *Document) String(key string) string {
	value, _ := d.Frontmatter[key].(string)
	return value
}

// VariantFile is an authored native representation of an Agent for one Harness.
type VariantFile struct {
	Harness HarnessID
	// Path is the repository-relative path of the native file.
	Path string
	// Raw holds the exact file bytes.
	Raw []byte
	// Dependencies are added to the Agent's common Dependencies for this Harness.
	Dependencies Dependencies
}

// AgentDependency requires another Agent to be installed.
type AgentDependency struct {
	// Agent is the logical identifier within Source (or the same snapshot).
	Agent string
	// Source is empty for a same-snapshot reference; otherwise a source locator.
	Source string
}

// SkillDependency requires a Skill to be installed and visible.
type SkillDependency struct {
	Source     string
	Skill      string
	MinVersion string
	MaxVersion string
}

// Dependencies groups required companion Agents and Skills.
type Dependencies struct {
	Agents []AgentDependency
	Skills []SkillDependency
}

// Empty reports whether no Dependencies are declared.
func (d Dependencies) Empty() bool {
	return len(d.Agents) == 0 && len(d.Skills) == 0
}

// Definition is one logical Agent discovered in a source snapshot.
type Definition struct {
	// ID is the logical identifier, unique within the source.
	ID string
	// Description is the delegation description, when known.
	Description string
	// Portable is the optional harness-neutral core definition.
	Portable *Document
	// Variants maps Harnesses to explicitly authored native files.
	Variants map[HarnessID]*VariantFile
	// Dependencies apply to every selected Variant.
	Dependencies Dependencies
	// FromManifest reports whether agnthub.yaml declared this Agent.
	FromManifest bool
}

// Variant returns the authored Variant for h, or nil.
func (d *Definition) Variant(h HarnessID) *VariantFile {
	if d.Variants == nil {
		return nil
	}
	return d.Variants[h]
}
