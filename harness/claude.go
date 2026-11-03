package harness

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"github.com/agnthub-sh/agnthub/agent"
)

// claudeAdapter installs Claude Code subagents (Markdown with YAML frontmatter).
type claudeAdapter struct{}

func (claudeAdapter) ID() agent.HarnessID { return agent.Claude }

func (claudeAdapter) Name() string { return "Claude Code" }

func (claudeAdapter) Layout() NativeLayout {
	return NativeLayout{Dir: ".claude/agents", Recursive: true, Suffixes: []string{".md"}}
}

func (claudeAdapter) Settings() []SettingSpec { return nil }

func (claudeAdapter) Identify(path string, raw []byte) (NativeAgent, error) {
	doc, err := agent.ParseMarkdownLenient(path, raw)
	if err != nil {
		return NativeAgent{}, err
	}
	return requireNamed(doc)
}

// requireNamed enforces the required name/description pair shared by several Harnesses.
func requireNamed(doc *agent.Document) (NativeAgent, error) {
	name, ok := doc.Frontmatter["name"].(string)
	if !ok || name == "" {
		return NativeAgent{}, fmt.Errorf("%s: frontmatter requires a string name", doc.Path)
	}
	description, ok := doc.Frontmatter["description"].(string)
	if !ok || strings.TrimSpace(description) == "" {
		return NativeAgent{ID: name}, fmt.Errorf("%s: frontmatter requires a description", doc.Path)
	}
	return NativeAgent{ID: name, Description: description}, nil
}

func (claudeAdapter) Destination(ctx Context, scope Scope, _ Settings) (Destination, error) {
	switch scope {
	case ScopeProject:
		return Destination{
			Dir:    filepath.Join(ctx.ProjectRoot, ".claude", "agents"),
			Label:  "project",
			Shared: true,
		}, nil
	case ScopeGlobal:
		return Destination{Dir: filepath.Join(claudeConfigDir(ctx), "agents"), Label: "user"}, nil
	default:
		return Destination{}, fmt.Errorf("unsupported scope %q", scope)
	}
}

func claudeConfigDir(ctx Context) string {
	if dir := ctx.Env.Get("CLAUDE_CONFIG_DIR"); filepath.IsAbs(dir) {
		return dir
	}
	return filepath.Join(ctx.Env.Home(), ".claude")
}

func (a claudeAdapter) Native(id string, variant *agent.VariantFile) (Artifact, error) {
	identity, err := a.Identify(variant.Path, variant.Raw)
	if err != nil {
		return Artifact{}, &InvalidVariantError{
			Harness: agent.Claude,
			Path:    variant.Path,
			Reason:  err.Error(),
		}
	}
	if identity.ID != id {
		return Artifact{}, &InvalidVariantError{
			Harness: agent.Claude, Path: variant.Path,
			Reason: fmt.Sprintf("name %q does not match agent %q", identity.ID, id),
		}
	}
	content, notes, err := claudePluginAgent(variant)
	if err != nil {
		return Artifact{}, &InvalidVariantError{
			Harness: agent.Claude, Path: variant.Path, Reason: err.Error(),
		}
	}
	doc, _ := agent.ParseMarkdownLenient(variant.Path, content)
	return Artifact{
		FileName: id + ".md",
		Content:  content,
		Notes:    append(notes, executableFieldNotes(doc, "hooks", "mcpServers")...),
	}, nil
}

// claudePluginIgnored are the frontmatter fields Claude Code ignores in a
// plugin's agents (https://code.claude.com/docs/en/plugins/components#agents).
// Installed into an agents directory they would take effect, so a plugin
// agent is installed as the plugin delivers it: without them.
var claudePluginIgnored = []string{"permissionMode", "hooks", "mcpServers", "initialPrompt"}

// claudePluginVariables only resolve in the body of an agent loaded from its
// plugin (https://code.claude.com/docs/en/plugins-reference#environment-variables);
// they name files and settings that only exist where the plugin is installed.
var claudePluginVariables = []string{
	"${CLAUDE_PLUGIN_ROOT}", "${CLAUDE_PLUGIN_DATA}", "${user_config.",
}

// claudePluginAgent returns the content a Claude Code variant installs and
// the notes on it: the file itself, or for a plugin's agent the file without
// the fields Claude Code ignores in plugins. A plugin agent whose instructions
// depend on its plugin's files or settings cannot stand alone and is refused.
func claudePluginAgent(variant *agent.VariantFile) ([]byte, []string, error) {
	if variant.Plugin == "" {
		return variant.Raw, nil, nil
	}
	doc, err := agent.ParseMarkdownLenient(variant.Path, variant.Raw)
	if err != nil {
		return nil, nil, err
	}
	for _, variable := range claudePluginVariables {
		if strings.Contains(doc.Body, variable) {
			return nil, nil, fmt.Errorf(
				"its instructions reference %s, which only resolves inside the installed "+
					"plugin %s", strings.Replace(variable, ".", ".<key>}", 1), variant.Plugin)
		}
	}
	content, omitted, err := agent.OmitKeys(variant.Path, variant.Raw, claudePluginIgnored...)
	if err != nil {
		return nil, nil, err
	}
	notes := make([]string, 0, len(omitted))
	for _, key := range omitted {
		notes = append(notes, fmt.Sprintf(
			"omitted %s: Claude Code ignores it in plugin %s's agents; installed on its own "+
				"it would take effect", key, variant.Plugin))
	}
	return content, notes, nil
}

// executableFieldNotes flags native fields that make the Harness run commands or
// servers when the Agent is used, so previews never hide them.
func executableFieldNotes(doc *agent.Document, keys ...string) []string {
	var notes []string
	for _, key := range keys {
		if _, ok := doc.Frontmatter[key]; ok {
			notes = append(
				notes,
				fmt.Sprintf("declares %s, which runs when the agent is used", key),
			)
		}
	}
	return notes
}

// claudeTools is Claude Code's tool vocabulary (2.1.309). Core names are what
// conversion writes; Grep and Glob only appear for agents without Bash, which
// then searches through embedded tools.
var claudeTools = toolTable{
	tools: []nativeTool{
		{name: "Read", cap: CapRead, core: true},
		{name: "LSP", cap: CapRead},
		{name: "Grep", cap: CapSearch, core: true},
		{name: "Glob", cap: CapSearch, core: true},
		{name: "Edit", cap: CapEdit, core: true},
		{name: "Write", cap: CapEdit, core: true},
		{name: "NotebookEdit", cap: CapEdit},
		{name: "Bash", cap: CapShell, core: true},
		{name: "PowerShell", cap: CapShell},
		{name: "Monitor", cap: CapShell},
		{name: "TaskStop", cap: CapShell},
		{name: "KillShell", cap: CapShell},
		{name: "KillBash", cap: CapShell},
		{name: "WebFetch", cap: CapWebFetch, core: true},
		{name: "WebSearch", cap: CapWebSearch, core: true},
		{name: "Agent", cap: CapDelegate, core: true},
		{name: "Task", cap: CapDelegate},
		{name: "TodoWrite", cap: CapTodo, core: true},
		{name: "TaskCreate", cap: CapTodo, core: true},
		{name: "TaskGet", cap: CapTodo, core: true},
		{name: "TaskList", cap: CapTodo, core: true},
		{name: "TaskUpdate", cap: CapTodo, core: true},
		{name: "Skill", cap: CapSkill, core: true},
	},
	helpers: map[string]string{
		"SendMessage": "messages other Claude Code agents and sessions, " +
			"which no other harness can address",
		"ListAgents":      "lists Claude Code agents SendMessage can reach",
		"ExitPlanMode":    "leaves Claude Code plan mode",
		"EnterPlanMode":   "enters Claude Code plan mode",
		"AskUserQuestion": "Claude Code removes it from subagents",
		"ToolSearch":      "loads deferred Claude Code tools",
		"MultiEdit":       "no longer a Claude Code tool, so it grants nothing",
		"LS":              "no longer a Claude Code tool, so it grants nothing",
		"NotebookRead":    "no longer a Claude Code tool, so it grants nothing",
		"TodoRead":        "no longer a Claude Code tool, so it grants nothing",
		"BashOutput":      "no longer a Claude Code tool, so it grants nothing",
		"TaskOutput":      "no longer a Claude Code tool, so it grants nothing",
	},
	mcp: claudeMCPTool,
}

// claudeMCPTool reads mcp__<server>__<tool> the way Claude Code splits it: the
// server name ends at the first double underscore, and mcp__<server> or
// mcp__<server>__* names every tool of the server. Other wildcards are not
// read. A plugin server, plugin_<plugin>_<server>, is named by its own server
// name; plugin names are kebab-case, so the first underscore ends the plugin.
func claudeMCPTool(name string) (MCPTool, bool) {
	rest, ok := strings.CutPrefix(name, "mcp__")
	if !ok {
		return MCPTool{}, false
	}
	server, tool, _ := strings.Cut(rest, "__")
	if tool == "*" {
		tool = ""
	}
	if server == "" || strings.Contains(server, "*") || strings.Contains(tool, "*") {
		return MCPTool{}, false
	}
	if inner, isPlugin := strings.CutPrefix(server, "plugin_"); isPlugin {
		plugin, pluginServer, found := strings.Cut(inner, "_")
		if found && plugin != "" && pluginServer != "" {
			return MCPTool{Server: pluginServer, Tool: tool, Plugin: plugin}, true
		}
	}
	return MCPTool{Server: server, Tool: tool}, true
}

// claudeIDEServer is Claude Code's built-in connection to the user's IDE.
const claudeIDEServer = "ide"

// claudeMCPName names an MCP tool as Claude Code exposes it: server and tool
// names with characters other than letters, digits, '_' and '-' replaced by '_'.
func claudeMCPName(tool MCPTool) (name, reason string) {
	server := claudeNormalize(tool.Server)
	if tool.Plugin != "" {
		server = "plugin_" + claudeNormalize(tool.Plugin) + "_" + server
	}
	if strings.Contains(server, "__") {
		return "", "Claude Code ends an MCP server name at its first double underscore"
	}
	if tool.Tool == "" {
		return "mcp__" + server + "__*", ""
	}
	return "mcp__" + server + "__" + claudeNormalize(tool.Tool), ""
}

// claudeNormalize spells a server or tool name the way Claude Code does in
// MCP tool names.
func claudeNormalize(name string) string {
	return strings.Map(func(r rune) rune {
		if r == '_' || r == '-' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' ||
			r >= '0' && r <= '9' {
			return r
		}
		return '_'
	}, name)
}

// claudeFields are the subagent frontmatter fields Claude Code 2.1.309
// recognizes, as its frontmatter reference lists them
// (https://code.claude.com/docs/en/sub-agents#frontmatter-reference). Claude
// Code ignores every other top-level key; its debug log names them as keys it
// "does not recognize or apply". The e2e suite checks that log against the
// Claude Code it runs, and TestClaudeFieldsMatchTheDocs checks this list
// against the current reference every night.
var claudeFields = []string{
	"name", "description", "tools", "disallowedTools", "model", "permissionMode",
	"maxTurns", "autoCompactWindow", "skills", "mcpServers", "hooks", "memory",
	"background", "omitClaudeMd", "effort", "isolation", "color", "initialPrompt",
	"experimental",
}

// ClaudeIgnores reports whether Claude Code ignores a subagent frontmatter key.
func ClaudeIgnores(key string) bool {
	return !slices.Contains(claudeFields, key)
}

// claudeRestrictionLike are keys, compared without case, '-' or '_', that
// Claude Code ignores but read as a tool or permission restriction: other
// harnesses' fields or misspelled Claude fields. Conversion refuses them rather
// than install an agent its author meant to restrict.
var claudeRestrictionLike = map[string]bool{
	"tools": true, "allowedtools": true, "disallowedtools": true, "deniedtools": true,
	"denytools": true, "blockedtools": true, "excludedtools": true, "readonly": true,
	"permission": true, "permissions": true, "permissionmode": true, "sandbox": true,
	"sandboxmode": true, "approval": true, "approvals": true, "approvalpolicy": true,
	"approvalmode": true, "allowedcommands": true, "deniedcommands": true,
	"allowedpaths": true, "deniedpaths": true, "hooks": true, "isolation": true,
	"mcpservers": true, "maxturns": true,
}

// restrictionLike reports whether an ignored key reads as a restriction.
func restrictionLike(key string) bool {
	folded := strings.Map(func(r rune) rune {
		if r == '-' || r == '_' || r == ' ' {
			return -1
		}
		return unicode.ToLower(r)
	}, key)
	return claudeRestrictionLike[folded]
}

// claudeBehaviors are Claude Code subagent fields with no cross-harness equivalent.
var claudeBehaviors = map[string]string{
	"permissionMode": "a Claude Code permission mode for tool approvals",
	"mcpServers":     "MCP servers scoped to the agent",
	"hooks":          "lifecycle hooks that run commands",
	"isolation":      "runs the agent in an isolated worktree",
	"initialPrompt":  "a first prompt submitted when the agent starts a session",
}

// claudeNotes are harmless Claude Code fields conversion omits, with why.
var claudeNotes = map[string]string{
	"color":             "cosmetic",
	"effort":            "reasoning effort is model-specific",
	"autoCompactWindow": "context compaction is a Claude Code session detail",
	"omitClaudeMd":      "other harnesses never load CLAUDE.md",
	"experimental":      "Claude Code experimental settings",
}

func (a claudeAdapter) Semantics(id string, variant *agent.VariantFile) (*Semantics, error) {
	if _, err := a.Native(id, variant); err != nil {
		return nil, err
	}
	content, notes, _ := claudePluginAgent(variant)
	doc, _ := agent.ParseMarkdownLenient(variant.Path, content)
	sem := &Semantics{
		ID:           id,
		Description:  doc.String("description"),
		Instructions: doc.Body,
		Source:       agent.Claude,
		Notes:        notes,
	}
	var allowed, disallowed []string
	var hasAllowlist bool
	for _, key := range sortedKeys(doc.Frontmatter) {
		value := doc.Frontmatter[key]
		switch key {
		case "name", "description":
		case "model":
			sem.Model = fmt.Sprint(value)
		case "tools":
			allowed, hasAllowlist = claudeToolEntries(sem, key, value), true
		case "disallowedTools":
			disallowed = claudeToolEntries(sem, key, value)
		case "maxTurns":
			if turns, ok := positiveInt(value); ok {
				sem.MaxTurns = turns
			} else {
				sem.unsupported(key, "a turn limit agnthub cannot read")
			}
		case "background":
			sem.Background = value == true || value == "true"
		case "memory":
			scope, ok := readMemoryScope(value)
			if !ok {
				sem.note("omitted memory: Claude Code ignores the value %v "+
					"(valid: user, project, local)", value)
				continue
			}
			sem.Memory = scope
		case "skills":
			readSkills(sem, key, value)
			if len(sem.Skills) > claudeMaxSkills {
				sem.note("omitted skills after the first %d: Claude Code preloads no more",
					claudeMaxSkills)
				sem.Skills = sem.Skills[:claudeMaxSkills]
			}
		default:
			switch {
			case claudeNotes[key] != "":
				sem.note("omitted %s: %s", key, claudeNotes[key])
			case claudeBehaviors[key] != "":
				sem.unsupported(key, claudeBehaviors[key])
			case restrictionLike(key):
				sem.unsupported(key, "a field Claude Code ignores, but it reads as a tool or "+
					"permission restriction")
			case harmlessPortableKeys[key]:
				sem.note("omitted %s: descriptive metadata", key)
			default:
				sem.note("omitted %s: Claude Code ignores it", key)
			}
		}
	}
	claudeToolPolicy(sem, hasAllowlist, allowed, disallowed)
	return sem, nil
}

// claudeToolEntries splits a tools value the way Claude Code does: a list or a
// string, each split on commas and spaces outside parentheses.
func claudeToolEntries(sem *Semantics, field string, value any) []string {
	var raw []string
	switch value := value.(type) {
	case string:
		raw = []string{value}
	case []any:
		for _, entry := range value {
			text, ok := entry.(string)
			if !ok {
				sem.unsupported(field, "a tool list agnthub cannot read")
				return nil
			}
			raw = append(raw, text)
		}
	case nil:
	default:
		sem.unsupported(field, "a tool list agnthub cannot read")
		return nil
	}
	var names []string
	for _, text := range raw {
		depth, start := 0, 0
		for i := 0; i <= len(text); i++ {
			if i < len(text) {
				switch text[i] {
				case '(':
					depth++
					continue
				case ')':
					depth--
					continue
				case ',', ' ', '\t':
					if depth > 0 {
						continue
					}
				default:
					continue
				}
			}
			if name := strings.TrimSpace(text[start:i]); name != "" {
				names = append(names, name)
			}
			start = i + 1
		}
	}
	return names
}

// claudeToolPolicy combines tools and disallowedTools. A "*" allows every tool.
// A denied entry with a specifier withholds the whole tool, as in Claude Code.
// Agent(...) lists only apply to a main-session agent, so a subagent's entry
// allows delegation; any other allowed specifier, such as Bash(git diff *),
// scopes a tool in a way other harnesses cannot express.
func claudeToolPolicy(sem *Semantics, hasAllowlist bool, allowed, disallowed []string) {
	bare := func(name string) (string, bool) {
		base, _, scoped := strings.Cut(name, "(")
		return base, scoped
	}
	denyNames := make([]string, 0, len(disallowed))
	for _, name := range disallowed {
		base, _ := bare(name)
		denyNames = append(denyNames, base)
	}
	denied := sem.deny("disallowedTools", denyNames, claudeTools)
	if !hasAllowlist || slices.Contains(allowed, "*") {
		sem.Tools.Denied = denied
		return
	}
	allowNames := make([]string, 0, len(allowed))
	for _, name := range allowed {
		base, scoped := bare(name)
		if tool, ok := claudeMCPTool(base); ok && tool.Server == claudeIDEServer &&
			tool.Plugin == "" {
			sem.note("omitted tools entry %s: Claude Code's built-in IDE connection, "+
				"which other harnesses do not offer as an MCP server", name)
			continue
		}
		switch {
		case !scoped:
		case base == "Agent" || base == "Task":
			sem.note("tools entry %s: other harnesses cannot limit which agents it delegates to",
				name)
		default:
			sem.unsupported(name, "a scoped tool rule other harnesses cannot express")
			continue
		}
		allowNames = append(allowNames, base)
	}
	sem.allow("tools", allowNames, claudeTools)
	sem.Tools.Allowed &^= denied
}

// positiveInt reads a positive whole number decoded from YAML or TOML; values
// beyond 32 bits are refused rather than truncated.
func positiveInt(value any) (int, bool) {
	if !isPositiveInt(value) {
		return 0, false
	}
	switch number := value.(type) {
	case int:
		return number, number <= math.MaxInt32
	case int64:
		return int(number), number <= math.MaxInt32
	case float64:
		return int(number), number <= math.MaxInt32
	}
	return 0, false
}

func (a claudeAdapter) Convert(sem *Semantics) (Artifact, error) {
	check, err := begin(agent.Claude, sem)
	if err != nil {
		return Artifact{}, err
	}
	fields := []agent.Field{
		{Key: "name", Value: sem.ID},
		{Key: "description", Value: sem.Description},
	}
	switch {
	case sem.Tools.Restricted && sem.Tools.Allowed == 0 && len(sem.Tools.MCP) == 0:
		return Artifact{}, check.refuse("it allows no tools, and Claude Code refuses to " +
			"spawn an agent with zero tools")
	case sem.Tools.Restricted:
		mcp, err := check.mcpNames(claudeMCPName)
		if err != nil {
			return Artifact{}, err
		}
		fields = append(fields, agent.Field{
			Key: "tools",
			Value: strings.Join(
				append(toolNames(claudeTools.tools, sem.Tools.Allowed), mcp...), ", "),
		})
	case sem.Tools.Denied != 0:
		fields = append(fields, agent.Field{
			Key:   "disallowedTools",
			Value: strings.Join(toolNames(claudeTools.tools, sem.Tools.Denied), ", "),
		})
	}
	if sem.MaxTurns > 0 {
		fields = append(fields, agent.Field{Key: "maxTurns", Value: sem.MaxTurns})
	}
	if sem.Background {
		fields = append(fields, agent.Field{Key: "background", Value: true})
	}
	if sem.Memory != "" {
		fields = append(fields, agent.Field{Key: "memory", Value: string(sem.Memory)})
	}
	if len(sem.Skills) > 0 {
		fields = append(fields, agent.Field{Key: "skills", Value: sem.Skills})
	}
	instructions := check.carry(additiveSupport{
		nativeMemory: true, nativeSkills: true, nativeTurns: true, tools: sem.Tools,
	})
	content, err := agent.RenderMarkdown(fields, instructions)
	if err != nil {
		return Artifact{}, err
	}
	return selfCheck(a, sem.ID, Artifact{
		FileName:  sem.ID + ".md",
		Content:   content,
		Generated: true,
		Converter: converterID(agent.Claude),
		Notes:     check.notes,
	})
}

func (claudeAdapter) Detect(ctx Context) bool {
	return detect(ctx, "claude", claudeConfigDir(ctx))
}

// detect reports whether an executable is on PATH or a config directory exists.
func detect(ctx Context, executable string, configDirs ...string) bool {
	if ctx.LookPath != nil {
		if _, err := ctx.LookPath(executable); err == nil {
			return true
		}
	}
	for _, dir := range configDirs {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return true
		}
	}
	return false
}
