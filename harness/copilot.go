package harness

import (
	"fmt"
	"path"
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
	if _, err := validateCopilot(doc); err != nil {
		return NativeAgent{}, fmt.Errorf("%s: %w", filePath, err)
	}
	base := path.Base(filePath)
	id := strings.TrimSuffix(strings.TrimSuffix(base, ".md"), ".agent")
	return NativeAgent{ID: id, Description: doc.String("description")}, nil
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
		case copilotIDEOnlyFields[key] || harmlessPortableKeys[key]:
			notes = append(notes, fmt.Sprintf("%s is not used by Copilot CLI", key))
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
// capabilities. Copilot's web alias, and its WebSearch compatibility alias, only
// expose web_fetch: the CLI has no web search tool. Core names are the primary
// aliases conversion writes.
var copilotToolNames = []nativeTool{
	{name: "read", cap: CapRead, core: true},
	{name: "view", cap: CapRead},
	{name: "notebookread", cap: CapRead},
	{name: "search", cap: CapSearch, core: true},
	{name: "grep", cap: CapSearch},
	{name: "glob", cap: CapSearch},
	{name: "search/codebase", cap: CapSearch},
	{name: "edit", cap: CapEdit, core: true},
	{name: "create", cap: CapEdit},
	{name: "write", cap: CapEdit},
	{name: "multiedit", cap: CapEdit},
	{name: "notebookedit", cap: CapEdit},
	{name: "editfiles", cap: CapEdit},
	{name: "execute", cap: CapShell, core: true},
	{name: "shell", cap: CapShell},
	{name: "bash", cap: CapShell},
	{name: "powershell", cap: CapShell},
	{name: "runcommands", cap: CapShell},
	{name: "web", cap: CapWebFetch, core: true},
	{name: "webfetch", cap: CapWebFetch},
	{name: "web_fetch", cap: CapWebFetch},
	{name: "websearch", cap: CapWebFetch},
	{name: "agent", cap: CapDelegate, core: true},
	{name: "custom-agent", cap: CapDelegate},
	{name: "task", cap: CapDelegate},
	{name: "runsubagent", cap: CapDelegate},
	{name: "todo", cap: CapTodo, core: true},
	{name: "todowrite", cap: CapTodo},
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
		Source:       agent.Copilot,
	}
	for _, key := range sortedKeys(doc.Frontmatter) {
		value := doc.Frontmatter[key]
		switch key {
		case "name", "description", "target", "include-custom-instructions":
		case "model":
			sem.Model, _ = value.(string)
		case "models", "modelPolicy", "reasoningEffort":
			sem.note("omitted %s: model settings are model-specific", key)
		case "tools":
			names, _ := nameList(value)
			copilotToolPolicy(sem, names)
		case "infer":
			if value == false {
				sem.unsupported(key, "Copilot never delegates to it automatically")
			}
		case "disable-model-invocation":
			if value == true {
				sem.unsupported(key, "Copilot never delegates to it automatically")
			}
		case "user-invocable":
			if value == false {
				sem.note("omitted user-invocable: false: other harnesses let users select it")
			}
		case "mcp-servers":
			sem.unsupported(key, "MCP servers scoped to the agent")
		default:
			sem.note("omitted %s: not used by Copilot CLI", key)
		}
	}
	return sem, nil
}

// copilotToolPolicy reads a Copilot tools list. Copilot ignores names it does
// not know, so they grant nothing; skill and sql are always present. An MCP
// tool is granted by its exposed name, <server>-<tool>; since server names may
// contain '-', such an entry cannot be read as a server and tool, so a
// hyphenated name that is not a built-in alias is kept as unknown. Copilot CLI
// grants nothing for documented <server>/<tool> entries naming session MCP
// servers.
func copilotToolPolicy(sem *Semantics, names []string) {
	if slices.Contains(names, "*") {
		return
	}
	sem.Tools.Restricted = true
	for _, name := range names {
		lower := strings.ToLower(name)
		if c, ok := lookupTool(copilotToolNames, lower); ok {
			sem.Tools.Allowed |= c
			continue
		}
		switch {
		case lower == "skill" || lower == "sql":
		case strings.Contains(name, "/"):
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
// built-in aliases.
func (a copilotAdapter) Convert(sem *Semantics) (Artifact, error) {
	check, err := begin(agent.Copilot, sem)
	if err == nil {
		err = check.requireGrant(CapWebSearch, "Copilot CLI has no web search tool")
	}
	if err == nil {
		err = check.requireMaxTurns()
	}
	if err == nil {
		err = check.requireForeground()
	}
	if err != nil {
		return Artifact{}, err
	}
	fields := []agent.Field{
		{Key: "name", Value: sem.ID},
		{Key: "description", Value: sem.Description},
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
	content, err := agent.RenderMarkdown(fields, sem.Instructions)
	if err != nil {
		return Artifact{}, err
	}
	return selfCheck(a, sem.ID, Artifact{
		FileName:  sem.ID + ".agent.md",
		Content:   content,
		Generated: true,
		Converter: converterID(agent.Copilot),
		Notes:     check.notes,
	})
}

func (copilotAdapter) Detect(ctx Context) bool {
	return detect(ctx, "copilot", copilotHome(ctx))
}
