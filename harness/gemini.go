package harness

import (
	"fmt"
	"math"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/heynemann/agnthub/agent"
)

// geminiAdapter installs Gemini CLI subagents (Markdown with YAML frontmatter).
// Gemini validates frontmatter strictly, and its subagents cannot invoke other
// agents, so delegation is never carried into a Gemini artifact.
type geminiAdapter struct{}

// geminiAgentTool is the Gemini tool that delegates to another agent; Gemini
// withholds it from subagents.
const geminiAgentTool = "invoke_agent"

var (
	geminiName = regexp.MustCompile(`^[a-z0-9-_]+$`)

	geminiLocalFields = map[string]bool{
		"kind": true, "name": true, "description": true, "display_name": true,
		"tools": true, "mcp_servers": true, "model": true, "temperature": true,
		"max_turns": true, "timeout_mins": true,
	}
	geminiRemoteFields = map[string]bool{
		"kind": true, "name": true, "description": true, "display_name": true,
		"auth": true, "agent_card_url": true, "agent_card_json": true,
	}
	// delegationKeys are portable or foreign declarations of agent orchestration.
	delegationKeys = []string{"spawns", "subagents", "agents", "handoffs"}
)

func (geminiAdapter) ID() agent.HarnessID { return agent.Gemini }

func (geminiAdapter) Name() string { return "Gemini CLI" }

func (geminiAdapter) Layout() NativeLayout {
	return NativeLayout{Dir: ".gemini/agents", Suffixes: []string{".md"}}
}

func (geminiAdapter) Settings() []SettingSpec { return nil }

func (geminiAdapter) Identify(filePath string, raw []byte) (NativeAgent, error) {
	doc, err := agent.ParseMarkdown(filePath, raw)
	if err != nil {
		return NativeAgent{}, err
	}
	return requireNamed(doc)
}

func (geminiAdapter) Destination(ctx Context, scope Scope, _ Settings) (Destination, error) {
	switch scope {
	case ScopeProject:
		return Destination{
			Dir:    filepath.Join(ctx.ProjectRoot, ".gemini", "agents"),
			Label:  "project",
			Shared: true,
		}, nil
	case ScopeGlobal:
		return Destination{Dir: filepath.Join(geminiConfigDir(ctx), "agents"), Label: "user"}, nil
	default:
		return Destination{}, fmt.Errorf("unsupported scope %q", scope)
	}
}

// geminiConfigDir is Gemini CLI's user directory: GEMINI_CLI_HOME replaces the
// home directory, under which Gemini keeps .gemini.
func geminiConfigDir(ctx Context) string {
	home := ctx.Env.Get("GEMINI_CLI_HOME")
	if !filepath.IsAbs(home) {
		home = ctx.Env.Home()
	}
	return filepath.Join(home, ".gemini")
}

func (geminiAdapter) Native(id string, variant *agent.VariantFile) (Artifact, error) {
	doc, err := agent.ParseMarkdown(variant.Path, variant.Raw)
	if err != nil {
		return Artifact{}, &InvalidVariantError{
			Harness: agent.Gemini, Path: variant.Path, Reason: err.Error(),
		}
	}
	notes, reason := validateGeminiVariant(id, doc)
	if reason != "" {
		return Artifact{}, &InvalidVariantError{
			Harness: agent.Gemini, Path: variant.Path, Reason: reason,
		}
	}
	return Artifact{FileName: id + ".md", Content: variant.Raw, Notes: notes}, nil
}

// validateGeminiVariant mirrors Gemini CLI's strict frontmatter schema and returns
// plan notes, or why Gemini would reject or not honor the variant.
func validateGeminiVariant(id string, doc *agent.Document) ([]string, string) {
	name, _ := doc.Frontmatter["name"].(string)
	switch {
	case !geminiName.MatchString(name):
		return nil, "name must be a lowercase slug of letters, digits, '-' or '_'"
	case name != id:
		return nil, fmt.Sprintf("name %q does not match agent %q", name, id)
	case strings.TrimSpace(doc.String("description")) == "":
		return nil, "frontmatter requires a description"
	}
	for _, key := range []string{"description", "display_name"} {
		if value, ok := doc.Frontmatter[key]; ok {
			if _, isString := value.(string); !isString {
				return nil, key + " must be a string"
			}
		}
	}
	kind, ok := doc.Frontmatter["kind"]
	switch {
	case !ok || kind == "local":
		return validateGeminiLocal(doc)
	case kind == "remote":
		return validateGeminiRemote(doc)
	default:
		return nil, "kind must be local or remote"
	}
}

func validateGeminiLocal(doc *agent.Document) ([]string, string) {
	if key := unknownKey(doc, geminiLocalFields); key != "" {
		return nil, fmt.Sprintf("Gemini CLI rejects %q in a local agent", key)
	}
	if strings.TrimSpace(doc.Body) == "" {
		return nil, "local agent requires instructions"
	}
	if reason := geminiTools(doc.Frontmatter["tools"]); reason != "" {
		return nil, reason
	}
	if value, ok := doc.Frontmatter["model"]; ok {
		if model, isString := value.(string); !isString || model == "" {
			return nil, "model must be a model name or inherit"
		}
	}
	if value, ok := doc.Frontmatter["temperature"]; ok && !isNumber(value) {
		return nil, "temperature must be a number"
	}
	for _, key := range []string{"max_turns", "timeout_mins"} {
		if value, ok := doc.Frontmatter[key]; ok && !isPositiveInt(value) {
			return nil, key + " must be a positive integer"
		}
	}
	var notes []string
	if value, ok := doc.Frontmatter["mcp_servers"]; ok {
		if _, isMap := value.(map[string]any); !isMap {
			return nil, "mcp_servers must be a mapping of server names"
		}
		notes = executableFieldNotes(doc, "mcp_servers")
	}
	return notes, ""
}

func validateGeminiRemote(doc *agent.Document) ([]string, string) {
	if key := unknownKey(doc, geminiRemoteFields); key != "" {
		return nil, fmt.Sprintf("Gemini CLI rejects %q in a remote agent", key)
	}
	url, hasURL := doc.Frontmatter["agent_card_url"].(string)
	_, hasCard := doc.Frontmatter["agent_card_json"].(string)
	if hasURL == hasCard {
		return nil, "remote agent requires exactly one string agent_card_url or agent_card_json"
	}
	if value, ok := doc.Frontmatter["auth"]; ok {
		if _, isMap := value.(map[string]any); !isMap {
			return nil, "auth must be a mapping"
		}
	}
	if hasURL {
		return []string{"remote A2A agent: Gemini CLI sends delegated tasks to " + url}, ""
	}
	return []string{
		"remote A2A agent: Gemini CLI sends delegated tasks to the agent_card_json endpoint",
	}, ""
}

// geminiTools validates the tools list shape and refuses delegation Gemini
// subagents cannot perform.
func geminiTools(value any) string {
	if value == nil {
		return ""
	}
	tools, ok := value.([]any)
	if !ok {
		return "tools must be a list of tool names"
	}
	for _, entry := range tools {
		tool, isString := entry.(string)
		if !isString || strings.TrimSpace(tool) == "" || strings.ContainsAny(tool, " \t\n") {
			return "tools must be a list of tool names"
		}
		if tool == geminiAgentTool {
			return "Gemini CLI subagents cannot invoke other agents; remove " + geminiAgentTool
		}
		if strings.HasPrefix(tool, "mcp_") && !geminiValidMCPName(tool) {
			return "Gemini CLI rejects MCP tool name " + tool
		}
	}
	return ""
}

func unknownKey(doc *agent.Document, allowed map[string]bool) string {
	for key := range doc.Frontmatter {
		if !allowed[key] {
			return key
		}
	}
	return ""
}

func isNumber(value any) bool {
	switch value.(type) {
	case int, int64, uint64, float64:
		return true
	default:
		return false
	}
}

func isPositiveInt(value any) bool {
	switch number := value.(type) {
	case int:
		return number > 0
	case int64:
		return number > 0
	case uint64:
		return number > 0
	case float64:
		return number > 0 && number == math.Trunc(number)
	default:
		return false
	}
}

// geminiToolTable maps Gemini CLI built-in tool names (0.66) to capabilities.
// Core names are what conversion writes.
var geminiToolTable = toolTable{
	tools: []nativeTool{
		{name: "read_file", cap: CapRead, core: true},
		{name: "list_directory", cap: CapRead, core: true},
		{name: "read_many_files", cap: CapRead},
		{name: "glob", cap: CapSearch, core: true},
		{name: "grep_search", cap: CapSearch, core: true},
		{name: "search_file_content", cap: CapSearch},
		{name: "replace", cap: CapEdit, core: true},
		{name: "write_file", cap: CapEdit, core: true},
		{name: "run_shell_command", cap: CapShell, core: true},
		{name: "web_fetch", cap: CapWebFetch, core: true},
		{name: "google_web_search", cap: CapWebSearch, core: true},
		{name: "write_todos", cap: CapTodo, core: true},
		{name: "tracker_create_task", cap: CapTodo},
		{name: "tracker_update_task", cap: CapTodo},
		{name: "tracker_get_task", cap: CapTodo},
		{name: "tracker_list_tasks", cap: CapTodo},
		{name: "tracker_add_dependency", cap: CapTodo},
		{name: "tracker_visualize", cap: CapTodo},
		{name: "activate_skill", cap: CapSkill, core: true},
	},
	helpers: map[string]string{
		"ask_user":        "asks the user a question in Gemini CLI",
		"enter_plan_mode": "enters Gemini CLI plan mode",
		"exit_plan_mode":  "leaves Gemini CLI plan mode",
		"complete_task":   "Gemini CLI adds it to every subagent",
		"update_topic":    "Gemini CLI removes it from subagents",
	},
	mcp: geminiMCPTool,
}

// geminiAllMCP keeps every MCP tool in an enumerated tools list.
const geminiAllMCP = "mcp_*"

// geminiMaxToolName is the longest MCP tool name Gemini CLI keeps; it shortens
// longer names in a way a tools list cannot rely on.
const geminiMaxToolName = 63

// geminiMCPTool reads mcp_<server>_<tool> as Gemini CLI does: the server name
// ends at the first underscore, and mcp_<server>_* names every tool of the
// server.
func geminiMCPTool(name string) (MCPTool, bool) {
	if name == geminiAllMCP || !geminiValidMCPName(name) {
		return MCPTool{}, false
	}
	server, tool, _ := strings.Cut(strings.TrimPrefix(name, "mcp_"), "_")
	if tool == "*" {
		tool = ""
	}
	return MCPTool{Server: server, Tool: tool}, true
}

// geminiValidMCPName reports whether Gemini CLI 0.66 accepts an mcp_ entry in
// a tools list: mcp_*, or slug server and tool names, the tool possibly *.
func geminiValidMCPName(name string) bool {
	if name == geminiAllMCP {
		return true
	}
	rest, ok := strings.CutPrefix(name, "mcp_")
	if !ok || strings.HasPrefix(rest, "_") {
		return false
	}
	server, tool, found := strings.Cut(rest, "_")
	if !found || server == "" || tool == "" || !geminiSlug(server) {
		return false
	}
	return tool == "*" || strings.Trim(tool, "_") != "" && geminiSlug(tool)
}

// geminiSlug matches Gemini CLI's /^[a-z0-9_.:-]+$/i tool-name slug.
func geminiSlug(text string) bool {
	for _, r := range text {
		if !geminiNameRune(r) || r == '*' {
			return false
		}
	}
	return text != ""
}

// geminiNameRune reports a character Gemini CLI keeps in MCP tool names; it
// replaces any other with '_'.
func geminiNameRune(r rune) bool {
	return r == '_' || r == '-' || r == '.' || r == ':' || r >= 'a' && r <= 'z' ||
		r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
}

// geminiMCPName names an MCP tool as Gemini CLI registers it: mcp_ (unless the
// name already starts with it), then <server>_<tool> with characters it does
// not keep replaced by '_'. Gemini selects a whole server only by a server name
// without '_', since it ends the server name at the first underscore.
func geminiMCPName(tool MCPTool) (name, reason string) {
	if tool.Tool == "" {
		name = "mcp_" + tool.Server + "_*"
		if strings.Contains(tool.Server, "_") || !geminiValidMCPName(name) {
			return "", "Gemini CLI can only select every tool of a server named with " +
				"letters, digits, '.', ':' and '-'"
		}
		return name, ""
	}
	name = tool.Server + "_" + tool.Tool
	if !strings.HasPrefix(name, "mcp_") {
		name = "mcp_" + name
	}
	name = strings.Map(func(r rune) rune {
		if geminiNameRune(r) {
			return r
		}
		return '_'
	}, name)
	switch {
	case len(name) > geminiMaxToolName:
		return "", fmt.Sprintf("its Gemini CLI name %s is longer than the %d characters "+
			"Gemini CLI keeps", name, geminiMaxToolName)
	case !geminiValidMCPName(name):
		return "", "Gemini CLI rejects its tool name " + name
	}
	return name, ""
}

func (a geminiAdapter) Semantics(id string, variant *agent.VariantFile) (*Semantics, error) {
	if _, err := a.Native(id, variant); err != nil {
		return nil, err
	}
	doc, _ := agent.ParseMarkdown(variant.Path, variant.Raw)
	sem := &Semantics{
		ID:           id,
		Description:  doc.String("description"),
		Instructions: doc.Body,
		Source:       agent.Gemini,
	}
	for _, key := range sortedKeys(doc.Frontmatter) {
		value := doc.Frontmatter[key]
		switch key {
		case "name", "description":
		case "kind":
			if value == "remote" {
				sem.unsupported(key, "a remote A2A agent")
			}
		case "display_name":
			sem.note("omitted display_name: cosmetic")
		case "model":
			sem.Model, _ = value.(string)
		case "temperature":
			sem.note("omitted temperature: model tuning is model-specific")
		case "max_turns":
			sem.MaxTurns, _ = positiveInt(value)
		case "tools":
			names, _ := nameList(value)
			if !slices.Contains(names, "*") {
				sem.allow("tools", names, geminiToolTable)
			}
		default:
			meaning := map[string]string{
				"mcp_servers":  "MCP servers scoped to the agent",
				"timeout_mins": "stops the agent after a time limit",
			}[key]
			if meaning == "" {
				meaning = "a Gemini CLI field with no verified equivalent"
			}
			sem.unsupported(key, meaning)
		}
	}
	return sem, nil
}

// Convert writes a Gemini CLI local subagent. Gemini subagents cannot invoke
// other agents, so an allowlist granting delegation is refused. Gemini has no
// denylist: one becomes the remaining built-in tools plus every MCP tool.
func (a geminiAdapter) Convert(sem *Semantics) (Artifact, error) {
	check, err := begin(agent.Gemini, sem)
	if err == nil {
		err = check.requireGrant(CapDelegate, "Gemini CLI subagents cannot invoke other agents")
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
		tools := toolNames(geminiToolTable.tools, sem.Tools.Effective())
		if !sem.Tools.Restricted {
			tools = append(tools, geminiAllMCP)
		}
		mcp, err := check.mcpNames(geminiMCPName)
		if err != nil {
			return Artifact{}, err
		}
		tools = append(tools, mcp...)
		if tools == nil {
			tools = []string{}
		}
		fields = append(fields, agent.Field{Key: "tools", Value: tools})
	}
	if sem.MaxTurns > 0 {
		fields = append(fields, agent.Field{Key: "max_turns", Value: sem.MaxTurns})
	}
	content, err := agent.RenderMarkdown(fields, sem.Instructions)
	if err != nil {
		return Artifact{}, err
	}
	return selfCheck(a, sem.ID, Artifact{
		FileName:  sem.ID + ".md",
		Content:   content,
		Generated: true,
		Converter: converterID(agent.Gemini),
		Notes:     check.notes,
	})
}

// DependencyNotes reports that installing companion Agents does not let a Gemini
// subagent delegate to them.
func (geminiAdapter) DependencyNotes(deps agent.Dependencies) []string {
	if len(deps.Agents) == 0 {
		return nil
	}
	return []string{
		"companion agents are installed for the main Gemini CLI session; Gemini subagents cannot invoke other agents",
	}
}

func (geminiAdapter) Detect(ctx Context) bool {
	return detect(ctx, "gemini", geminiConfigDir(ctx))
}
