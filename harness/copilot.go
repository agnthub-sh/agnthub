package harness

import (
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/agnthub-sh/agnthub/agent"
)

// copilotAdapter installs GitHub Copilot CLI custom agents. The file name minus
// ".agent.md" or ".md" is the identity; frontmatter name is only a display name.
// Support covers the Copilot CLI surface, not IDE or cloud custom agents.
type copilotAdapter struct{}

func (copilotAdapter) ID() agent.HarnessID { return agent.Copilot }

func (copilotAdapter) Name() string { return "GitHub Copilot CLI" }

func (copilotAdapter) Layout() NativeLayout {
	return NativeLayout{Dir: ".github/agents", Suffixes: []string{".agent.md", ".md"}}
}

func (copilotAdapter) Settings() []SettingSpec { return nil }

func (copilotAdapter) Identify(filePath string, raw []byte) (NativeAgent, error) {
	doc, err := agent.ParseMarkdown(filePath, raw)
	if err != nil {
		return NativeAgent{}, err
	}
	identity := NativeAgent{ID: agent.IDFromPath(filePath), Description: doc.String("description")}
	identity.DisplayName = displayName(identity.ID, doc.String("name"))
	if identity.ID == "" {
		return NativeAgent{}, fmt.Errorf("%s: the file name implies no identifier", filePath)
	}
	if _, err := validateCopilot(doc); err != nil {
		return identity, fmt.Errorf("%s: %w", filePath, err)
	}
	return identity, nil
}

// copilotFieldKind is the documented shape of a Copilot CLI frontmatter field.
type copilotFieldKind int

const (
	copilotString copilotFieldKind = iota
	copilotBool
	copilotStringList
	copilotTools
	copilotObject
	copilotStringMap
)

// copilotFields are the frontmatter fields the Copilot CLI documents. The CLI
// drops agents whose fields have the wrong shape.
var copilotFields = map[string]copilotFieldKind{
	"name":                        copilotString,
	"description":                 copilotString,
	"target":                      copilotString,
	"tools":                       copilotTools,
	"model":                       copilotString,
	"models":                      copilotStringList,
	"modelPolicy":                 copilotString,
	"reasoningEffort":             copilotString,
	"infer":                       copilotBool,
	"user-invocable":              copilotBool,
	"disable-model-invocation":    copilotBool,
	"include-custom-instructions": copilotBool,
	"mcp-servers":                 copilotObject,
	"metadata":                    copilotStringMap,
	"skills":                      copilotStringList,
}

// copilotIDEOnlyFields are documented for IDE custom agents and only affect IDE UI.
var copilotIDEOnlyFields = map[string]bool{"argument-hint": true, "handoffs": true}

// validateCopilot checks frontmatter against the Copilot CLI schema and returns
// advisory notes. A key with no Copilot CLI meaning is refused rather than
// installed: the CLI would silently ignore it, which could drop a restriction.
func validateCopilot(doc *agent.Document) ([]string, error) {
	description, ok := doc.Frontmatter["description"].(string)
	if !ok || strings.TrimSpace(description) == "" {
		return nil, fmt.Errorf("frontmatter requires a description")
	}
	keys := make([]string, 0, len(doc.Frontmatter))
	for key := range doc.Frontmatter {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var notes []string
	for _, key := range keys {
		value := doc.Frontmatter[key]
		kind, known := copilotFields[key]
		switch {
		case known:
			if err := checkCopilotField(key, kind, value); err != nil {
				return nil, err
			}
		case copilotIDEOnlyFields[key] || harmlessPortableKeys[key] || descriptiveKeys[key]:
			notes = append(notes, fmt.Sprintf("%s is not used by Copilot CLI", key))
		case copilotUnenforced[key] != "":
			if !copilotUnenforcedShape(key, value) {
				return nil, fmt.Errorf("%s has a shape agnthub cannot read: %v", key, value)
			}
			notes = append(notes, fmt.Sprintf("Copilot CLI does not enforce %s: %s; other "+
				"harnesses do", key, copilotUnenforced[key]))
		default:
			return nil, fmt.Errorf(
				"%s is not a Copilot CLI agent field; the CLI would silently ignore it",
				key,
			)
		}
	}
	switch target := doc.String("target"); target {
	case "", "github-copilot":
	case "vscode":
		return nil, fmt.Errorf("target vscode declares a VS Code agent, not a Copilot CLI agent")
	default:
		return nil, fmt.Errorf("target must be vscode or github-copilot, got %q", target)
	}
	switch policy := doc.String("modelPolicy"); policy {
	case "", "preferred", "required":
	default:
		return nil, fmt.Errorf("modelPolicy must be preferred or required, got %q", policy)
	}
	return notes, nil
}

// copilotUnenforced are restriction-like fields authors write into Copilot
// agents (from VS Code custom agents and Claude Code) that Copilot CLI 1.0.105
// ignores, with what that means there. The authored agent installs as written
// with that warning; other harnesses honor the restriction where they can
// (ADR 0012).
var copilotUnenforced = map[string]string{
	"agents":        "on Copilot CLI the agent may delegate to any agent",
	"allowed-tools": "on Copilot CLI only tools limits the agent",
	"maxTurns":      "on Copilot CLI the agent has no turn limit",
}

// copilotUnenforcedShape checks that an unenforced field reads as a list of
// names, or for maxTurns a positive number.
func copilotUnenforcedShape(key string, value any) bool {
	if key == "maxTurns" {
		_, ok := positiveInt(value)
		return ok
	}
	_, ok := nameList(value)
	return ok
}

func checkCopilotField(key string, kind copilotFieldKind, value any) error {
	var ok bool
	switch kind {
	case copilotString:
		_, ok = value.(string)
	case copilotBool:
		_, ok = value.(bool)
	case copilotStringList:
		ok = isStringList(value)
	case copilotTools:
		_, isCSV := value.(string)
		ok = isCSV || isStringList(value)
	case copilotObject:
		_, ok = value.(map[string]any)
	case copilotStringMap:
		var entries map[string]any
		entries, ok = value.(map[string]any)
		for _, entry := range entries {
			if _, isString := entry.(string); !isString {
				ok = false
			}
		}
	}
	if !ok {
		return fmt.Errorf("%s has an invalid shape for Copilot CLI: %v", key, value)
	}
	return nil
}

func isStringList(value any) bool {
	items, ok := value.([]any)
	if !ok {
		return false
	}
	for _, item := range items {
		if _, ok := item.(string); !ok {
			return false
		}
	}
	return true
}

func (copilotAdapter) Destination(ctx Context, scope Scope, _ Settings) (Destination, error) {
	switch scope {
	case ScopeProject:
		return Destination{
			Dir:    filepath.Join(ctx.ProjectRoot, ".github", "agents"),
			Label:  "project",
			Shared: true,
		}, nil
	case ScopeGlobal:
		if err := requireAbsoluteDir(ctx, "COPILOT_HOME", "Copilot CLI"); err != nil {
			return Destination{}, err
		}
		return Destination{Dir: filepath.Join(copilotHome(ctx), "agents"), Label: "user"}, nil
	default:
		return Destination{}, fmt.Errorf("unsupported scope %q", scope)
	}
}

// copilotHome is the Copilot CLI configuration directory: COPILOT_HOME or ~/.copilot.
func copilotHome(ctx Context) string {
	if dir := ctx.Env.Get("COPILOT_HOME"); filepath.IsAbs(dir) {
		return dir
	}
	return filepath.Join(ctx.Env.Home(), ".copilot")
}

func (copilotAdapter) Native(id string, variant *agent.VariantFile) (Artifact, error) {
	doc, err := agent.ParseMarkdown(variant.Path, variant.Raw)
	var notes []string
	if err == nil {
		notes, err = validateCopilot(doc)
	}
	if err != nil {
		return Artifact{}, &InvalidVariantError{
			Harness: agent.Copilot,
			Path:    variant.Path,
			Reason:  err.Error(),
		}
	}
	return Artifact{
		FileName: id + ".agent.md",
		Content:  variant.Raw,
		Notes:    append(notes, executableFieldNotes(doc, "mcp-servers")...),
	}, nil
}

// copilotToolNames maps Copilot CLI tools entries (matched case-insensitively) to
// capabilities: the documented aliases plus the VS Code tool names Copilot CLI
// 1.0.105 grants (runtime-verified 2026-11-07, see harness-agent-formats.md).
// The web alias, and web/fetch, only expose web_fetch: the CLI has no web
// search tool, and WebSearch grants nothing. Core names are the primary
// aliases conversion writes.
var copilotToolNames = []nativeTool{
	{name: "read", cap: CapRead, core: true},
	{name: "view", cap: CapRead},
	{name: "notebookread", cap: CapRead},
	{name: "readfile", cap: CapRead},
	{name: "search", cap: CapSearch, core: true},
	{name: "grep", cap: CapSearch},
	{name: "glob", cap: CapSearch},
	{name: "codebase", cap: CapSearch},
	{name: "filesearch", cap: CapSearch},
	{name: "textsearch", cap: CapSearch},
	{name: "edit", cap: CapEdit, core: true},
	{name: "create", cap: CapEdit},
	{name: "write", cap: CapEdit},
	{name: "multiedit", cap: CapEdit},
	{name: "notebookedit", cap: CapEdit},
	{name: "editfiles", cap: CapEdit},
	{name: "createfile", cap: CapEdit},
	{name: "createdirectory", cap: CapEdit},
	{name: "execute", cap: CapShell, core: true},
	{name: "shell", cap: CapShell},
	{name: "bash", cap: CapShell},
	{name: "powershell", cap: CapShell},
	{name: "runcommands", cap: CapShell},
	{name: "runinterminal", cap: CapShell},
	{name: "web", cap: CapWebFetch, core: true},
	{name: "webfetch", cap: CapWebFetch},
	{name: "web_fetch", cap: CapWebFetch},
	{name: "web/fetch", cap: CapWebFetch},
	{name: "agent", cap: CapDelegate, core: true},
	{name: "custom-agent", cap: CapDelegate},
	{name: "task", cap: CapDelegate},
	{name: "runsubagent", cap: CapDelegate},
	{name: "todo", cap: CapTodo, core: true},
	{name: "todos", cap: CapTodo},
	{name: "todowrite", cap: CapTodo},
}

// copilotToolSets are VS Code's tool sets, the namespaces of its
// <set>/<tool> names (edit/editFiles, read/problems…). Copilot CLI grants
// such an entry only through the alias its last segment names.
var copilotToolSets = []string{
	"agent", "edit", "execute", "read", "runcommands", "search", "todo", "vscode", "web",
}

// copilotLookup finds a tools entry the way Copilot CLI 1.0.105 does:
// case-insensitively, and a namespaced <prefix>/<name> entry by its last
// segment (read/readFile grants view, github/search grants grep and glob).
func copilotLookup(name string) (Capability, bool) {
	lower := strings.ToLower(name)
	if c, ok := lookupTool(copilotToolNames, lower); ok {
		return c, true
	}
	if i := strings.LastIndexByte(lower, '/'); i >= 0 {
		return lookupTool(copilotToolNames, lower[i+1:])
	}
	return 0, false
}

func (a copilotAdapter) Semantics(id string, variant *agent.VariantFile) (*Semantics, error) {
	if _, err := a.Native(id, variant); err != nil {
		return nil, err
	}
	doc, _ := agent.ParseMarkdown(variant.Path, variant.Raw)
	sem := &Semantics{
		ID:           id,
		Description:  doc.String("description"),
		Instructions: doc.Body,
		DisplayName:  displayName(id, doc.String("name")),
		Source:       agent.Copilot,
	}
	readCopilotFields(sem, doc, false)
	return sem, nil
}

// readCopilotFields reads a Copilot CLI agent's frontmatter into sem: a
// validated .github/agents file, or a portable core in Copilot's own
// <id>.agent.md form. A portable core was not validated, so a known field
// of the wrong shape is unsupported (Copilot CLI would drop the agent), and
// a key Copilot CLI ignores is omitted with a note unless it reads as a tool
// or permission restriction, which is unsupported as in a portable core read
// as a Claude Code agent.
func readCopilotFields(sem *Semantics, doc *agent.Document, portable bool) {
	var skillAllowlist []string
	hasSkillAllowlist := false
	// disable-model-invocation takes precedence over the retired infer
	// (https://docs.github.com/en/copilot/reference/custom-agents-configuration).
	modelInvocation, hasModelInvocation := doc.Frontmatter["disable-model-invocation"].(bool)
	sem.ManualOnly = modelInvocation ||
		(!hasModelInvocation && doc.Frontmatter["infer"] == false)
	for _, key := range sortedKeys(doc.Frontmatter) {
		value := doc.Frontmatter[key]
		if portable && !portableCopilotField(sem, key, value) {
			continue
		}
		switch key {
		case "name", "description", "target", "include-custom-instructions":
		case "model":
			sem.Model, _ = value.(string)
		case "models", "modelPolicy", "reasoningEffort":
			sem.note("omitted %s: model settings are model-specific", key)
		case "tools":
			names, _ := nameList(value)
			copilotToolPolicy(sem, names)
		case "allowed-tools":
			skillAllowlist, _ = nameList(value)
			hasSkillAllowlist = true
			sem.relaxed = true
		case "agents":
			sem.relaxed = true
			names, _ := nameList(value)
			if !slices.Contains(names, "*") {
				sem.Spawns = append([]string{}, names...)
			}
		case "maxTurns":
			sem.MaxTurns, _ = positiveInt(value)
			sem.relaxed = true
		case "infer", "disable-model-invocation":
		case "user-invocable":
			if value == false {
				sem.note("omitted user-invocable: false: other harnesses let users select it")
			}
		case "mcp-servers":
			if emptyValue(value) {
				sem.note("omitted mcp-servers: it declares none")
				continue
			}
			servers, reason := copilotMCPServers(value)
			if reason != "" {
				sem.unsupported(key, "MCP servers scoped to the agent; "+reason)
				continue
			}
			sem.MCPServers = servers
		case "skills":
			readSkills(sem, key, value)
		default:
			sem.note("omitted %s: not used by Copilot CLI", key)
		}
	}
	if hasSkillAllowlist {
		extra := &Semantics{Source: sem.Source}
		copilotToolPolicy(extra, skillAllowlist)
		sem.Notes = append(sem.Notes, extra.Notes...)
		sem.intersectTools("allowed-tools", extra.Tools)
		sem.note("read allowed-tools as a second tool allowlist: Copilot CLI ignores it, " +
			"other harnesses enforce it")
	}
	if sem.Spawns != nil && len(sem.Spawns) == 0 {
		sem.Spawns = nil
		sem.Tools.withhold(CapDelegate)
		sem.note("agents: [] converted without delegation")
	}
}

// portableCopilotField checks one key of a portable core read as a Copilot
// CLI agent and reports whether readCopilotFields reads it. It records why
// it does not: a restriction of a shape agnthub cannot read, or one Copilot
// CLI ignores, is unsupported; an empty value, another field of the wrong
// shape (VS Code's model list) or a key Copilot CLI ignores is omitted with
// a note.
func portableCopilotField(sem *Semantics, key string, value any) bool {
	kind, known := copilotFields[key]
	switch {
	case key == "name" || key == "description":
		return true
	case value == nil && key != "tools":
		sem.note("omitted %s: empty", key)
	case known && checkCopilotField(key, kind, value) == nil:
		return true
	case copilotUnenforced[key] != "" && copilotUnenforcedShape(key, value):
		return true
	case key == "tools" || key == "mcp-servers" || copilotUnenforced[key] != "":
		sem.unsupported(key, "a restriction of a shape agnthub cannot read")
	case known:
		sem.note("omitted %s: Copilot CLI cannot read its value", key)
	case restrictionLike(key, value):
		sem.unsupported(key, "a field Copilot CLI ignores, but it reads as a tool or "+
			"permission restriction")
	case copilotIDEOnlyFields[key]:
		sem.note("omitted %s: used only by IDE custom agents", key)
	case harmlessPortableKeys[key] || descriptiveKeys[key]:
		sem.note("omitted %s: descriptive metadata", key)
	default:
		sem.note("omitted %s: Copilot CLI ignores it", key)
	}
	return false
}

// copilotToolPolicy reads a Copilot tools list. Copilot ignores names it does
// not know, so they grant nothing; skill and sql are always present. A
// namespaced entry grants what its last segment's alias grants (copilotLookup);
// otherwise a VS Code tool-set entry (read/problems) names a VS Code tool
// Copilot CLI lacks, and any other <server>/<tool> entry grants nothing for
// session MCP servers. An MCP tool is granted by its exposed name,
// <server>-<tool>; since server names may contain '-', such an entry cannot
// be read as a server and tool, so a hyphenated name that is not a built-in
// alias is kept as unknown.
func copilotToolPolicy(sem *Semantics, names []string) {
	if slices.Contains(names, "*") {
		return
	}
	sem.Tools.Restricted = true
	for _, name := range names {
		if c, ok := copilotLookup(name); ok {
			sem.Tools.Allowed |= c
			continue
		}
		lower := strings.ToLower(name)
		set, _, namespaced := strings.Cut(lower, "/")
		switch {
		case lower == "skill" || lower == "sql":
		case namespaced && slices.Contains(copilotToolSets, set):
			sem.note("omitted tools entry %s: a VS Code tool Copilot CLI does not have, so "+
				"it grants nothing", name)
		case namespaced:
			sem.note("omitted tools entry %s: Copilot CLI grants nothing for a "+
				"<server>/<tool> entry naming a session MCP server", name)
		case strings.Contains(name, "-"):
			sem.Tools.Unknown = append(sem.Tools.Unknown, name)
		default:
			sem.note("omitted tools entry %s: Copilot CLI ignores it, so it grants nothing", name)
		}
	}
	sem.Tools.Allowed |= CapSkill
}

// copilotMaxToolName is the longest tool name Copilot CLI keeps; it truncates
// longer exposed MCP tool names.
const copilotMaxToolName = 64

// copilotMCPName names an MCP tool as Copilot CLI exposes it, which is also how
// a tools list selects it: <server>-<tool>, with characters other than
// letters, digits, '_' and '-' replaced by '-', matched case-insensitively.
// Copilot CLI has no entry selecting every tool of a server.
func copilotMCPName(tool MCPTool) (name, reason string) {
	if tool.Tool == "" {
		return "", "Copilot CLI cannot select every tool of an MCP server"
	}
	name = copilotNormalize(tool.Server) + "-" + copilotNormalize(tool.Tool)
	if len(name) > copilotMaxToolName {
		return "", fmt.Sprintf("its Copilot CLI name %s is longer than the %d characters "+
			"Copilot CLI keeps", name, copilotMaxToolName)
	}
	return name, ""
}

// copilotNormalize spells a server or tool name the way Copilot CLI does in
// exposed MCP tool names.
func copilotNormalize(name string) string {
	return strings.Map(func(r rune) rune {
		if r == '_' || r == '-' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' ||
			r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, name)
}

// Convert writes a Copilot CLI agent. Tool policies become primary aliases;
// Copilot has no denylist, so one is written as the allowlist of the remaining
// built-in aliases. Copilot CLI offers custom agents no web search tool, so an
// allowlist granting it installs without it, as a reduction.
func (a copilotAdapter) Convert(sem *Semantics) (Artifact, error) {
	check, err := begin(agent.Copilot, sem)
	if err != nil {
		return Artifact{}, err
	}
	sem = check.sem
	check.reduceGrant(CapWebSearch, copilotNoWebSearch)
	// Copilot CLI identifies the agent by its file name; name is its label.
	name := sem.ID
	if sem.DisplayName != "" {
		name = sem.DisplayName
	}
	fields := []agent.Field{
		{Key: "name", Value: name},
		{Key: "description", Value: sem.Description},
	}
	if sem.ManualOnly {
		fields = append(fields, agent.Field{Key: "disable-model-invocation", Value: true})
	}
	if model := strings.TrimSpace(sem.Model); model != "" && sem.dialectNative(agent.Copilot) {
		fields = append(fields, agent.Field{Key: "model", Value: model})
	}
	if sem.Tools.Limited() {
		if !sem.Tools.Restricted {
			check.note("Copilot CLI has no tool denylist, so the agent lists its built-in " +
				"tools and does not get MCP tools")
		}
		tools := toolNames(copilotToolNames, sem.Tools.Effective())
		if sem.Tools.Restricted {
			mcp, err := check.mcpNames(copilotMCPName)
			if err != nil {
				return Artifact{}, err
			}
			tools = append(tools, mcp...)
		}
		fields = append(fields, agent.Field{Key: "tools", Value: tools})
	}
	if len(sem.Skills) > 0 {
		fields = append(fields, agent.Field{Key: "skills", Value: sem.Skills})
	}
	instructions := check.carry(additiveSupport{
		nativeSkills: true,
		noUserMemory: "Copilot CLI asks before agents write outside the project, and denies " +
			"it in prompt mode",
		tools: sem.Tools,
	})
	content, err := agent.RenderMarkdown(fields, instructions)
	if err != nil {
		return Artifact{}, err
	}
	return selfCheck(a, sem.ID, check.artifact(sem.ID+".agent.md", content))
}

// copilotNoWebSearch is why a Copilot CLI agent goes without web search,
// observed with Copilot CLI 1.0.105: a tools entry web_search grants nothing,
// and github-mcp-server/web_search asks GitHub's MCP server for a tool it
// does not offer, so the agent gets no search tool.
const copilotNoWebSearch = "Copilot CLI offers custom agents no web search tool"

func (copilotAdapter) Detect(ctx Context) bool {
	return detect(ctx, "copilot", copilotHome(ctx))
}
