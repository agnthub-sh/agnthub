package harness

import (
	"fmt"
	"slices"
	"strings"

	"github.com/heynemann/agnthub/internal/agent"
)

// Semantics is the harness-neutral meaning of one Agent: what every Installer
// renders into its native format. Parsers fill it from the portable core or from
// an authored native Variant; renderers carry what their Harness can express and
// refuse the rest.
type Semantics struct {
	ID           string
	Description  string
	Instructions string
	// Source is the Harness whose native Variant was parsed, or "" for the
	// portable core.
	Source agent.HarnessID
	// Model is the source Harness's model choice. Model strings are not portable:
	// renderers never carry it and note that the target's default model applies.
	Model string
	// Tools is the tool policy; the zero value inherits every tool.
	Tools ToolPolicy
	// MaxTurns limits agentic turns; 0 leaves the Harness default.
	MaxTurns int
	// Background runs the Agent without blocking the caller.
	Background bool
	// Notes are harmless omissions made while parsing (cosmetic metadata,
	// model tuning, session-only helpers).
	Notes []string
	// Unsupported are declared behaviors no other Harness can express; every
	// renderer refuses an Agent that declares one.
	Unsupported []Feature
}

// Feature is one declared behavior without a cross-harness equivalent.
type Feature struct {
	// Field is the native field or tool entry, e.g. "hooks" or "permission.bash".
	Field string
	// Meaning says what the declaration does in its source Harness.
	Meaning string
}

// Capability is a bit set of harness-neutral tool capabilities.
type Capability uint16

// Tool capabilities. Read through Delegate are boundaries: a renderer that cannot
// grant one the Agent allows, or withhold one it excludes, refuses unless the
// Harness documents a weaker equivalent it notes. Todo and Skill are session
// helpers whose absence or presence never changes what the Agent can affect.
const (
	CapRead Capability = 1 << iota
	CapSearch
	CapEdit
	CapShell
	CapWebFetch
	CapWebSearch
	CapDelegate
	CapTodo
	CapSkill

	// CapWeb is web fetch and web search together.
	CapWeb = CapWebFetch | CapWebSearch
	// capAll is every capability.
	capAll = CapRead | CapSearch | CapEdit | CapShell | CapWeb | CapDelegate | CapTodo | CapSkill
)

var capabilityNames = []struct {
	cap  Capability
	name string
}{
	{CapRead, "read"},
	{CapSearch, "search"},
	{CapEdit, "edit"},
	{CapShell, "shell"},
	{CapWebFetch, "web fetch"},
	{CapWebSearch, "web search"},
	{CapDelegate, "delegation"},
	{CapTodo, "todo list"},
	{CapSkill, "skills"},
}

// String lists the capabilities in c.
func (c Capability) String() string {
	var names []string
	for _, entry := range capabilityNames {
		if c&entry.cap != 0 {
			names = append(names, entry.name)
		}
	}
	return strings.Join(names, ", ")
}

// ToolPolicy is an Agent's tool access. Restricted policies are allowlists; the
// others inherit every tool the session offers except Denied.
type ToolPolicy struct {
	Restricted bool
	// Allowed are the allowlisted capabilities when Restricted.
	Allowed Capability
	// Denied are the withheld capabilities when not Restricted.
	Denied Capability
	// MCP lists allowlisted MCP tools, as authored.
	MCP []string
	// Unknown lists allowlisted tool names with no known capability.
	Unknown []string
}

// Allows reports whether the policy grants every capability in c.
func (p ToolPolicy) Allows(c Capability) bool {
	if p.Restricted {
		return p.Allowed&c == c
	}
	return p.Denied&c == 0
}

// Effective returns the granted capabilities.
func (p ToolPolicy) Effective() Capability {
	if p.Restricted {
		return p.Allowed
	}
	return capAll &^ p.Denied
}

// Limited reports whether the policy withholds anything.
func (p ToolPolicy) Limited() bool {
	return p.Restricted || p.Denied != 0
}

// converterVersion is the conversion contract of every generated artifact.
const converterVersion = "v2"

// converterID names the conversion contract for h.
func converterID(h agent.HarnessID) string {
	return string(h) + "/" + converterVersion
}

// ConverterReproduces reports whether the current converter regenerates
// artifacts recorded under the converter id recorded. v2 renders a portable core
// byte for byte like v1 (the only source v1 converted), so a lock written by v1
// restores when its recorded digest still matches.
func ConverterReproduces(recorded, current string) bool {
	if recorded == current {
		return true
	}
	harnessID, version, ok := strings.Cut(recorded, "/")
	return ok && version == "v1" && current == converterID(agent.HarnessID(harnessID))
}

// PortableSemantics parses the portable core: identity, description and
// instructions with no tool policy. Harmless metadata is noted; any other field
// refuses conversion for target.
func PortableSemantics(target agent.HarnessID, id string, doc *agent.Document) (*Semantics, error) {
	core, err := ExtractPortable(target, id, doc)
	if err != nil {
		return nil, err
	}
	return &Semantics{
		ID:           core.ID,
		Description:  core.Description,
		Instructions: core.Instructions,
		Notes:        core.OmissionNotes(),
	}, nil
}

// sourceLabel names where the semantics came from for notes and reasons.
func (s *Semantics) sourceLabel() string {
	if s.Source == "" {
		return "portable definition"
	}
	return string(s.Source) + " variant"
}

// note records a harmless omission.
func (s *Semantics) note(format string, args ...any) {
	s.Notes = append(s.Notes, fmt.Sprintf(format, args...))
}

// unsupported records a behavior no target can express.
func (s *Semantics) unsupported(field, meaning string) {
	s.Unsupported = append(s.Unsupported, Feature{Field: field, Meaning: meaning})
}

// renderCheck holds what a renderer accumulates for one target.
type renderCheck struct {
	target agent.HarnessID
	sem    *Semantics
	notes  []string
}

// begin validates the parts every target treats alike and starts the notes:
// unsupported features and MCP or unknown tools refuse; the model is noted.
func begin(target agent.HarnessID, sem *Semantics) (*renderCheck, error) {
	check := &renderCheck{target: target, sem: sem, notes: slices.Clone(sem.Notes)}
	if len(sem.Unsupported) > 0 {
		feature := sem.Unsupported[0]
		return nil, check.refuse("its %s declares %s (%s), which %s cannot express",
			sem.sourceLabel(), feature.Field, feature.Meaning, target)
	}
	if strings.TrimSpace(sem.Instructions) == "" {
		return nil, check.refuse("its %s has no instructions", sem.sourceLabel())
	}
	if len(sem.Tools.MCP) > 0 {
		return nil, check.refuse(
			"it allows MCP tool %s, which depends on an MCP server configured for %s; "+
				"agnthub cannot carry that server to %s",
			sem.Tools.MCP[0], sourceName(sem.Source), target)
	}
	if len(sem.Tools.Unknown) > 0 {
		return nil, check.refuse("it allows tool %q, which has no %s equivalent",
			sem.Tools.Unknown[0], target)
	}
	if model := strings.TrimSpace(sem.Model); model != "" && model != "inherit" {
		check.note("model %q is specific to %s; the agent uses the harness default model",
			model, sourceName(sem.Source))
	}
	if sem.Source != "" && sem.Source != target {
		check.notes = append([]string{"converted from the " + sem.sourceLabel()}, check.notes...)
	}
	return check, nil
}

func sourceName(h agent.HarnessID) string {
	if h == "" {
		return "the portable definition"
	}
	return string(h)
}

func (c *renderCheck) note(format string, args ...any) {
	c.notes = append(c.notes, fmt.Sprintf(format, args...))
}

func (c *renderCheck) refuse(format string, args ...any) error {
	return &IncompatibleError{Harness: c.target, Reason: fmt.Sprintf(format, args...)}
}

// requireGrant refuses when an allowlist grants a capability in caps the target
// cannot grant. An inherited policy grants whatever the target offers.
func (c *renderCheck) requireGrant(caps Capability, why string) error {
	if !c.sem.Tools.Restricted {
		return nil
	}
	if missing := c.sem.Tools.Allowed & caps; missing != 0 {
		return c.refuse("it uses %s, but %s", missing, why)
	}
	return nil
}

// requireWithheld refuses when the Agent withholds a capability in caps the
// target cannot withhold, which would widen its permissions.
func (c *renderCheck) requireWithheld(caps Capability, why string) error {
	if withheld := caps &^ c.sem.Tools.reach(); withheld != 0 {
		return c.refuse("it excludes %s, but %s", withheld, why)
	}
	return nil
}

// noteWithheld notes capabilities in caps the Agent excludes but the target
// cannot withhold.
func (c *renderCheck) noteWithheld(caps Capability, why string) {
	if withheld := caps &^ c.sem.Tools.reach(); withheld != 0 {
		c.note("excludes %s, but %s", withheld, why)
	}
}

// reach is what the policy can do in effect: a shell reads and searches files,
// so withholding read or search from an agent with a shell withholds nothing.
func (p ToolPolicy) reach() Capability {
	granted := p.Effective()
	if granted&CapShell != 0 {
		granted |= CapRead | CapSearch
	}
	return granted
}

// requireMaxTurns refuses a turn limit the target cannot express.
func (c *renderCheck) requireMaxTurns() error {
	if c.sem.MaxTurns > 0 {
		return c.refuse("its %s limits the agent to %d turns, which %s cannot express",
			c.sem.sourceLabel(), c.sem.MaxTurns, c.target)
	}
	return nil
}

// requireForeground refuses background execution the target cannot express.
func (c *renderCheck) requireForeground() error {
	if c.sem.Background {
		return c.refuse("its %s runs the agent in the background, which %s cannot express",
			c.sem.sourceLabel(), c.target)
	}
	return nil
}

// toolNames returns the native names granting caps, in table order.
func toolNames(table []nativeTool, caps Capability) []string {
	var names []string
	for _, tool := range table {
		if tool.core && caps&tool.cap != 0 && !slices.Contains(names, tool.name) {
			names = append(names, tool.name)
		}
	}
	return names
}

// nativeTool maps one native tool name to the capability it grants.
type nativeTool struct {
	name string
	cap  Capability
	// core marks a tool renderers emit for cap; an allowlist naming only some core
	// tools of a capability is noted as widened.
	core bool
}

// lookupTool finds name in table, case-sensitively.
func lookupTool(table []nativeTool, name string) (Capability, bool) {
	for _, tool := range table {
		if tool.name == name {
			return tool.cap, true
		}
	}
	return 0, false
}

// selfCheck validates generated content with the target's own Native validator.
func selfCheck(installer Installer, id string, artifact Artifact) (Artifact, error) {
	_, err := installer.Native(id, &agent.VariantFile{
		Harness: installer.ID(),
		Path:    artifact.FileName,
		Raw:     artifact.Content,
	})
	if err != nil {
		return Artifact{}, fmt.Errorf("generated %s agent %q is not valid: %w",
			installer.ID(), id, err)
	}
	return artifact, nil
}

// toolTable is one Harness's tool vocabulary.
type toolTable struct {
	// tools map native names to capabilities, in the order renderers emit them.
	tools []nativeTool
	// helpers are session-only tools whose omission elsewhere is harmless, with why.
	helpers map[string]string
	// mcp reports whether a name is an MCP tool.
	mcp func(name string) bool
}

// allow reads an authored allowlist.
func (s *Semantics) allow(field string, names []string, table toolTable) {
	s.Tools.Restricted = true
	for _, name := range names {
		if table.mcp != nil && table.mcp(name) {
			s.Tools.MCP = append(s.Tools.MCP, name)
			continue
		}
		if c, ok := lookupTool(table.tools, name); ok {
			s.Tools.Allowed |= c
			continue
		}
		if why, ok := table.helpers[name]; ok {
			s.note("omitted %s entry %s: %s", field, name, why)
			continue
		}
		s.Tools.Unknown = append(s.Tools.Unknown, name)
	}
	s.notePartial(field, names, table)
}

// deny reads an authored denylist. A denied name without a capability cannot be
// withheld elsewhere, so it is unsupported.
func (s *Semantics) deny(field string, names []string, table toolTable) Capability {
	var denied Capability
	for _, name := range names {
		if c, ok := lookupTool(table.tools, name); ok {
			denied |= c
			continue
		}
		if _, ok := table.helpers[name]; ok {
			continue
		}
		s.unsupported(field+" "+name, "withholds a tool other harnesses cannot withhold")
	}
	return denied
}

// notePartial notes an allowlist naming only some of the core tools of an edit
// or shell capability: other Harnesses grant the capability as a whole.
func (s *Semantics) notePartial(field string, names []string, table toolTable) {
	for _, c := range []Capability{CapEdit, CapShell} {
		if s.Tools.Allowed&c == 0 {
			continue
		}
		for _, tool := range table.tools {
			if tool.cap == c && tool.core && !slices.Contains(names, tool.name) {
				s.note("%s grants only part of %s (no %s); converted agents get %s as a whole",
					field, c, tool.name, c)
				break
			}
		}
	}
}

// nameList reads a YAML list of strings or a comma-separated string.
func nameList(value any) ([]string, bool) {
	switch value := value.(type) {
	case string:
		var names []string
		for name := range strings.SplitSeq(value, ",") {
			if name = strings.TrimSpace(name); name != "" {
				names = append(names, name)
			}
		}
		return names, true
	case []any:
		names := make([]string, 0, len(value))
		for _, entry := range value {
			name, ok := entry.(string)
			if !ok {
				return nil, false
			}
			names = append(names, strings.TrimSpace(name))
		}
		return names, true
	case nil:
		return nil, true
	default:
		return nil, false
	}
}

// sortedKeys returns the frontmatter keys in a stable order.
func sortedKeys(fields map[string]any) []string {
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}
