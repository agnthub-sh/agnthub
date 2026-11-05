package harness

import (
	"fmt"
	"slices"
	"strings"

	"github.com/agnthub-sh/agnthub/agent"
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
	// Dialect is the Harness whose field meanings a portable core is read
	// with: Claude Code (ADR 0012). It is "" for a native Variant.
	Dialect agent.HarnessID
	// Model is the source Harness's model choice. Model strings are not portable:
	// renderers never carry it to another Harness and note that the target's
	// default model applies.
	Model string
	// Tools is the tool policy; the zero value inherits every tool.
	Tools ToolPolicy
	// MaxTurns limits agentic turns; 0 leaves the Harness default.
	MaxTurns int
	// Background runs the Agent without blocking the caller.
	Background bool
	// PermissionMode is a Claude Code permission mode: one that only approves
	// more without asking (claudeRelaxingModes), which other Harnesses drop
	// with a note since they ask for approval as their session does, or plan,
	// which other Harnesses carry as a read-only tool policy.
	PermissionMode string
	// Spawns limits delegation to these agents; nil leaves it unlimited. Only
	// omp can limit it: other targets withhold delegation.
	Spawns []string
	// MCPServers are MCP servers scoped to the Agent. Only Gemini CLI has
	// per-agent servers; other targets refuse them.
	MCPServers []MCPServer
	// Memory is the Agent's persistent memory scope, or "" for none. Memory
	// and Skills are additive: renderers carry them natively, emulate them with
	// instructions, or omit them with a note (see additive.go).
	Memory MemoryScope
	// Skills are the skills preloaded into the Agent's context, in authored
	// order. agnthub never installs skills from this list.
	Skills []string
	// Notes are harmless omissions made while parsing (cosmetic metadata,
	// model tuning, session-only helpers).
	Notes []string
	// Unsupported are declared behaviors no other Harness can express; every
	// renderer refuses an Agent that declares one.
	Unsupported []Feature
	// relaxed marks a source read through a reading ADR 0012 added: a portable
	// core with Claude Code fields, or a Variant field its own Harness does not
	// apply (Codex sandbox_mode and features.multi_agent, Cursor tools, Copilot
	// CLI agents, allowed-tools and maxTurns, Claude Code allowed-tools).
	// Generate prefers sources that convert without one (relaxedFor).
	relaxed bool
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
	// MCP lists allowlisted MCP tools in authored order.
	MCP []MCPTool
	// Unknown lists allowlisted tool names with no known capability; renderers
	// leave them out of the allowlist with a note.
	Unknown []string
	// Shell limits a granted shell to some commands; nil when the shell, if
	// granted, runs any command. Only OpenCode carries it: other targets
	// withhold the shell.
	Shell *ShellScope
}

// ShellScope is shell access limited by command patterns, in OpenCode's
// pattern syntax ('*' matches anything; a trailing " *" also matches the bare
// command), evaluated like OpenCode: the last matching rule wins.
type ShellScope struct {
	// Default is the effect for commands no rule matches: allow, ask or deny.
	Default string
	// Rules are the command patterns in evaluation order.
	Rules []ShellRule
}

// ShellRule is one command pattern and its effect: allow, ask or deny.
type ShellRule struct {
	Pattern string
	Effect  string
}

// runs reports whether the scope lets the agent run any command, with or
// without approval.
func (s *ShellScope) runs() bool {
	if s.Default != "deny" {
		return true
	}
	return slices.ContainsFunc(s.Rules, func(r ShellRule) bool { return r.Effect != "deny" })
}

// String lists the rules for notes, e.g. "git diff * (allow), * (deny)".
func (s *ShellScope) String() string {
	parts := make([]string, 0, len(s.Rules)+1)
	parts = append(parts, "* ("+s.Default+")")
	for _, rule := range s.Rules {
		parts = append(parts, rule.Pattern+" ("+rule.Effect+")")
	}
	return strings.Join(parts, ", ")
}

// withhold removes the capabilities in c from the policy.
func (p *ToolPolicy) withhold(c Capability) {
	if p.Restricted {
		p.Allowed &^= c
	} else {
		p.Denied |= c
	}
}

// MCPServer is an MCP server scoped to one Agent: started for it, with the
// configuration the Agent declares.
type MCPServer struct {
	Name string
	// Transport is stdio, http (streamable HTTP) or sse.
	Transport string
	Command   string
	Args      []string
	Env       map[string]string
	Cwd       string
	URL       string
	Headers   map[string]string
	// Tools limits the server's tools; nil offers all of them.
	Tools []string
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

// MCPTool is one allowlisted tool of an MCP server, named as the server
// publishes it. agnthub never installs or configures MCP servers: a converted
// Agent reaches the tool only if the target Harness has a server of that name.
type MCPTool struct {
	// Server is the MCP server name.
	Server string
	// Tool is the tool name, or "" for every tool the server offers.
	Tool string
	// Plugin is the Claude Code plugin that provides Server, or "" for a
	// server the user configures.
	Plugin string
}

// String names the tool as server/tool, or server/* for the whole server.
func (t MCPTool) String() string {
	if t.Tool == "" {
		return t.Server + "/*"
	}
	return t.Server + "/" + t.Tool
}

// converterVersion is the conversion contract of every generated artifact.
const converterVersion = "v5"

// converterID names the conversion contract for h.
func converterID(h agent.HarnessID) string {
	return string(h) + "/" + converterVersion
}

// ConverterReproduces reports whether the current converter regenerates
// artifacts recorded under the converter id recorded. Each version renders
// what its predecessors converted byte for byte (v2 added native Variant
// sources, v3 MCP tool references, v4 persistent memory and preloaded
// skills, v5 portable cores read as Claude Code agents and reduced
// capability), so a lock written by an earlier version restores when its
// recorded digest still matches.
func ConverterReproduces(recorded, current string) bool {
	if recorded == current {
		return true
	}
	harnessID, version, ok := strings.Cut(recorded, "/")
	return ok && slices.Contains([]string{"v1", "v2", "v3", "v4"}, version) &&
		current == converterID(agent.HarnessID(harnessID))
}

// portableDelegationKeys limit which agents an Agent delegates to in other
// harnesses' dialects. Claude Code ignores them, so a portable core declaring
// one is refused rather than read as unlimited.
var portableDelegationKeys = []string{"spawns", "subagents", "agents", "handoffs"}

// portableRestrictionKeys, compared like claudeRestrictionLike, are other
// dialects' restrictions that Claude Code ignores: Gemini CLI's excluded tools
// and time limit, OpenCode's step limit and Codex features. A portable core
// may be written in those dialects, so declaring one is refused rather than
// read as unrestricted; a .claude/agents file keeps Claude Code's reading.
var portableRestrictionKeys = map[string]bool{
	"excludetools": true, "disabledtools": true, "steps": true, "maxsteps": true,
	"timeoutmins": true, "features": true,
}

// PortableSemantics parses the portable core as a Claude Code agent (ADR
// 0012): the same fields, values and refusals as a .claude/agents file, plus
// the delegation lists of other dialects, which still refuse. It requires a
// description and instructions.
func PortableSemantics(target agent.HarnessID, id string, doc *agent.Document) (*Semantics, error) {
	if doc == nil {
		return nil, &IncompatibleError{
			Harness: target,
			Reason:  "no authored variant and no portable definition",
		}
	}
	description, ok := doc.Frontmatter["description"].(string)
	if !ok || strings.TrimSpace(description) == "" {
		return nil, fmt.Errorf("%s: portable definition requires a description", doc.Path)
	}
	if strings.TrimSpace(doc.Body) == "" {
		return nil, fmt.Errorf("%s: portable definition has no instructions", doc.Path)
	}
	sem := &Semantics{
		ID:           id,
		Description:  description,
		Instructions: doc.Body,
		Dialect:      agent.Claude,
	}
	for key := range doc.Frontmatter {
		if key != "name" && key != "description" && !harmlessPortableKeys[key] {
			sem.relaxed = true
			break
		}
	}
	readClaudeFields(sem, doc)
	for _, key := range portableDelegationKeys {
		if value, set := doc.Frontmatter[key]; set && !emptyValue(value) {
			sem.unsupported(key, "limits which agents it delegates to, a field Claude Code "+
				"ignores")
		}
	}
	for _, key := range sortedKeys(doc.Frontmatter) {
		if portableRestrictionKeys[foldKey(key)] && !emptyValue(doc.Frontmatter[key]) {
			sem.unsupported(key, "a field Claude Code ignores, but it reads as a tool or "+
				"permission restriction in another harness's dialect")
		}
	}
	if variable := claudePluginVariable(doc.Body); variable != "" {
		sem.unsupported(variable, "instructions that only resolve inside an installed Claude "+
			"Code plugin")
	}
	return sem, nil
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
	// sem is what the target renders: the parsed Semantics, lowered by begin
	// to what the target can carry.
	sem   *Semantics
	notes []string
	// reduced are the capabilities the Agent uses that the target cannot grant.
	reduced []Reduction
	// memoryDir is the folder of emulated memory, set by carry.
	memoryDir string
	// skillGranted reports that skillTools added the skill tool.
	skillGranted bool
}

// begin validates the parts every target treats alike, lowers what only some
// targets carry, and starts the notes: unsupported features refuse; unknown
// tools, relaxing permission modes and the model are noted. Renderers render
// check.sem and map MCP tools themselves, with mcpNames or mcpSession.
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
	if len(sem.MCPServers) > 0 && target != agent.Gemini {
		names := make([]string, len(sem.MCPServers))
		for i, server := range sem.MCPServers {
			names[i] = server.Name
		}
		return nil, check.refuse("its %s declares MCP servers scoped to the agent (%s), which "+
			"%s cannot express; only Gemini CLI has per-agent MCP servers",
			sem.sourceLabel(), strings.Join(names, ", "), target)
	}
	native := sem.claudeNative(target)
	check.lower(native)
	sem = check.sem
	if mode := sem.PermissionMode; mode != "" && !native {
		check.note("permissionMode %s isn't available in %s; the harness asks for approval "+
			"as usual", mode, target)
	}
	// Leaving a tool out of an allowlist withholds it, which never widens what
	// the agent may do.
	if unknown := sem.Tools.Unknown; len(unknown) > 0 {
		check.note("omitted tools entries %s: no %s equivalent, so the agent goes without "+
			"them", strings.Join(unknown, ", "), target)
	}
	if model := strings.TrimSpace(sem.Model); model != "" && model != "inherit" && !native {
		check.note("model %q is specific to %s; the agent uses the harness default model",
			model, sem.dialectName())
	}
	if sem.Source != "" && sem.Source != target {
		check.notes = append([]string{"converted from the " + sem.sourceLabel()}, check.notes...)
	}
	return check, nil
}

// claudeNative reports whether target reads the Agent's Claude Code fields
// natively: Claude Code rendering a portable core read as a Claude Code agent
// keeps its model and permission mode.
func (s *Semantics) claudeNative(target agent.HarnessID) bool {
	return target == agent.Claude && s.Dialect == agent.Claude
}

// dialectName names whose field meanings the Agent's model follows.
func (s *Semantics) dialectName() string {
	if s.Source == "" && s.Dialect != "" {
		return string(s.Dialect)
	}
	return sourceName(s.Source)
}

// lower rewrites what the target cannot carry into what it can without
// widening the Agent's access: a plan-mode agent becomes read-only, a shell
// limited to some commands is withheld outside OpenCode, delegation limited
// to some agents is withheld outside omp, and background agents run in the
// foreground outside Claude Code and Cursor. A capability the Agent uses
// and loses is recorded as a reduction.
func (c *renderCheck) lower(native bool) {
	sem := *c.sem
	changed := false
	if sem.PermissionMode == "plan" && !native {
		shell := sem.Tools.Allows(CapShell)
		delegates := sem.Tools.Allows(CapDelegate)
		mcp := len(sem.Tools.MCP) > 0 || !sem.Tools.Restricted
		// Plan mode only reads: delegated agents and MCP tools could write.
		sem.Tools, _ = intersectPolicies(sem.Tools,
			ToolPolicy{Restricted: true, Allowed: readOnlyCaps})
		sem.PermissionMode = ""
		c.note("permissionMode plan converted as a read-only tool policy: no edits, shell, " +
			"delegation or MCP tools")
		if shell {
			c.reduce(ReducedShell, fmt.Sprintf("permissionMode plan allows read-only shell "+
				"commands, and %s cannot limit the shell to them", c.target))
		}
		if delegates && canWithholdDelegation(c.target) {
			c.reduce("delegation", "permissionMode plan is read-only, and a delegated agent "+
				"could edit")
		}
		if mcp && c.target != agent.OMP {
			c.reduce("MCP tools", "permissionMode plan is read-only, and MCP tools could write")
		}
		changed = true
	}
	if scope := sem.Tools.Shell; scope != nil && c.target != agent.OpenCode &&
		(!native || !scope.claudeForm()) {
		sem.Tools.Shell = nil
		sem.Tools.withhold(CapShell)
		if scope.runs() {
			c.reduce(ReducedShell, fmt.Sprintf("it may run only some shell commands (%s), and "+
				"%s cannot limit the shell to commands", scope, c.target))
		}
		changed = true
	}
	if spawns := sem.Spawns; spawns != nil && c.target != agent.OMP {
		delegates := sem.Tools.Allows(CapDelegate)
		sem.Spawns = nil
		switch {
		case !delegates || len(spawns) == 0:
			sem.Tools.withhold(CapDelegate)
		case !canWithholdDelegation(c.target):
			// As for an Agent that withholds delegation there, the limit is
			// noted: these targets offer delegation to every agent.
			c.note("it may delegate only to %s, but %s cannot limit or withhold "+
				"delegation", strings.Join(spawns, ", "), c.target)
		default:
			sem.Tools.withhold(CapDelegate)
			c.reduce("delegation", fmt.Sprintf("it may delegate only to %s, and %s cannot "+
				"limit which agents it delegates to", strings.Join(spawns, ", "), c.target))
		}
		changed = true
	}
	if sem.Background && c.target != agent.Claude && c.target != agent.Cursor {
		sem.Background = false
		c.note("runs in the foreground: %s cannot run an agent in the background", c.target)
		changed = true
	}
	if changed {
		c.sem = &sem
	}
}

// relaxedFor reports whether converting s for target relies on a reading
// ADR 0012 added: a relaxed source, or a declaration the target only carries
// lowered (plan mode, a shell or delegation limit, background execution) or
// agent-scoped MCP servers.
func (s *Semantics) relaxedFor(target agent.HarnessID) bool {
	return s.relaxed || len(s.MCPServers) > 0 || s.Tools.Shell != nil || s.Spawns != nil ||
		(s.PermissionMode == "plan" && !s.claudeNative(target)) ||
		(s.Background && target != agent.Claude && target != agent.Cursor)
}

// canWithholdDelegation reports whether target can take delegation away from
// an agent. Codex and Cursor always offer it (harness-agent-formats.md).
func canWithholdDelegation(target agent.HarnessID) bool {
	return target != agent.Codex && target != agent.Cursor
}

// reduce records a capability the Agent uses that the target cannot grant,
// and notes why.
func (c *renderCheck) reduce(capability, why string) {
	if slices.ContainsFunc(
		c.reduced,
		func(r Reduction) bool { return r.Capability == capability },
	) {
		return
	}
	c.reduced = append(c.reduced, Reduction{Capability: capability, Why: why})
	c.note("goes without %s: %s", capability, why)
}

// reduceGrant records a reduction for each capability in caps that an
// allowlist grants and the target cannot. An inherited policy grants
// whatever the target offers, so it loses nothing it named.
func (c *renderCheck) reduceGrant(caps Capability, why string) {
	if !c.sem.Tools.Restricted {
		return
	}
	for _, entry := range capabilityNames {
		if c.sem.Tools.Allowed&caps&entry.cap != 0 {
			c.reduce(entry.name, why)
		}
	}
}

// artifact finishes a generated artifact with the check's notes and
// reductions.
func (c *renderCheck) artifact(fileName string, content []byte) Artifact {
	return Artifact{
		FileName:  fileName,
		Content:   content,
		Generated: true,
		Converter: converterID(c.target),
		Notes:     c.notes,
		Reduced:   c.reduced,
	}
}

// mcpNames maps every allowlisted MCP tool to the target's native name, or
// returns the refusal native gives for a tool the target cannot address, and
// notes the servers the Agent needs.
func (c *renderCheck) mcpNames(native func(MCPTool) (name, reason string)) ([]string, error) {
	if len(c.sem.Tools.MCP) == 0 {
		return nil, nil
	}
	names := make([]string, 0, len(c.sem.Tools.MCP))
	for _, tool := range c.sem.Tools.MCP {
		name, reason := native(tool)
		if reason != "" {
			return nil, c.refuse("it allows MCP tool %s, but %s", tool, reason)
		}
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	c.noteMCPServers()
	return names, nil
}

// mcpSession notes the servers an Agent's MCP tools need on a target that
// offers every session MCP tool to every agent and cannot limit them; the
// target's own note states that residual surface.
func (c *renderCheck) mcpSession() {
	if len(c.sem.Tools.MCP) > 0 {
		c.noteMCPServers()
	}
}

// noteMCPServers names the MCP servers the Agent's tools come from, except
// those the Agent declares itself (they install with it).
func (c *renderCheck) noteMCPServers() {
	var servers []string
	for _, tool := range c.sem.Tools.MCP {
		if slices.ContainsFunc(c.sem.MCPServers,
			func(s MCPServer) bool { return s.Name == tool.Server }) {
			continue
		}
		server := tool.Server
		if tool.Plugin != "" {
			server += " (provided in Claude Code by plugin " + tool.Plugin + ")"
		}
		if !slices.Contains(servers, server) {
			servers = append(servers, server)
		}
	}
	if len(servers) == 0 {
		return
	}
	c.note("needs MCP servers configured for %s under these names: %s; "+
		"agnthub does not install or configure MCP servers",
		c.target, strings.Join(servers, ", "))
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
	// mcp reads a name as an MCP tool reference; names it cannot read fall
	// through to tools, helpers and finally unknown names.
	mcp func(name string) (MCPTool, bool)
}

// allow reads an authored allowlist.
func (s *Semantics) allow(field string, names []string, table toolTable) {
	s.Tools.Restricted = true
	for _, name := range names {
		if table.mcp != nil {
			if tool, ok := table.mcp(name); ok {
				s.Tools.MCP = append(s.Tools.MCP, tool)
				continue
			}
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
