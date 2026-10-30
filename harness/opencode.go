package harness

import (
	"fmt"
	"maps"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/agnthub-sh/agnthub/agent"
)

// opencodeAdapter installs OpenCode agents (Markdown with YAML frontmatter).
// The file name is the identity; any frontmatter name is ignored by OpenCode.
type opencodeAdapter struct{}

func (opencodeAdapter) ID() agent.HarnessID { return agent.OpenCode }

func (opencodeAdapter) Name() string { return "OpenCode" }

func (opencodeAdapter) Layout() NativeLayout {
	return NativeLayout{Dir: ".opencode/agents", Suffixes: []string{".md"}}
}

func (opencodeAdapter) Settings() []SettingSpec { return nil }

func (opencodeAdapter) Identify(filePath string, raw []byte) (NativeAgent, error) {
	doc, err := agent.ParseMarkdown(filePath, raw)
	if err != nil {
		return NativeAgent{}, err
	}
	if err := validateOpenCode(doc); err != nil {
		return NativeAgent{}, fmt.Errorf("%s: %w", filePath, err)
	}
	return NativeAgent{
		ID:          strings.TrimSuffix(path.Base(filePath), ".md"),
		Description: doc.String("description"),
	}, nil
}

// opencodeModes are the documented agent modes; OpenCode drops agents declaring others.
var opencodeModes = map[string]bool{"primary": true, "subagent": true, "all": true}

// opencodePermissionEffects are the documented permission outcomes.
var opencodePermissionEffects = map[string]bool{"allow": true, "ask": true, "deny": true}

// validateOpenCode rejects frontmatter OpenCode would silently drop or misread,
// so an authored Variant never installs as an agent that does not load.
func validateOpenCode(doc *agent.Document) error {
	fields := doc.Frontmatter
	description, ok := fields["description"].(string)
	if !ok || strings.TrimSpace(description) == "" {
		return fmt.Errorf("frontmatter requires a description")
	}
	if value, set := fields["mode"]; set {
		mode, ok := value.(string)
		if !ok || !opencodeModes[mode] {
			return fmt.Errorf("mode must be primary, subagent or all, got %v", value)
		}
	}
	if value, set := fields["model"]; set {
		model, ok := value.(string)
		provider, id, found := strings.Cut(model, "/")
		if !ok || !found || provider == "" || id == "" {
			return fmt.Errorf("model must be a provider/model-id string, got %v", value)
		}
	}
	if value, set := fields["permission"]; set {
		if err := validateOpenCodePermission(value); err != nil {
			return err
		}
	}
	if value, set := fields["tools"]; set {
		tools, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("tools must map tool names to true or false")
		}
		for name, enabled := range tools {
			if _, ok := enabled.(bool); !ok {
				return fmt.Errorf("tools.%s must be true or false, got %v", name, enabled)
			}
		}
	}
	for _, key := range []string{"hidden", "disable"} {
		if value, set := fields[key]; set {
			if _, ok := value.(bool); !ok {
				return fmt.Errorf("%s must be true or false, got %v", key, value)
			}
		}
	}
	for _, key := range []string{"temperature", "top_p"} {
		if value, set := fields[key]; set && !isNumber(value) {
			return fmt.Errorf("%s must be a number, got %v", key, value)
		}
	}
	if value, set := fields["steps"]; set {
		if steps, ok := value.(int); !ok || steps < 1 {
			return fmt.Errorf("steps must be a positive integer, got %v", value)
		}
	}
	return nil
}

// validateOpenCodePermission accepts one effect for every action, or a map of
// actions to an effect or to command/path patterns mapped to effects. Unknown
// action names are kept: OpenCode applies them to custom tools.
func validateOpenCodePermission(value any) error {
	if effect, ok := value.(string); ok {
		if !opencodePermissionEffects[effect] {
			return fmt.Errorf("permission must be allow, ask or deny, got %q", effect)
		}
		return nil
	}
	actions, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("permission must be an effect or a map of actions")
	}
	for action, rule := range actions {
		switch rule := rule.(type) {
		case string:
			if !opencodePermissionEffects[rule] {
				return fmt.Errorf("permission.%s must be allow, ask or deny, got %q", action, rule)
			}
		case map[string]any:
			for pattern, effect := range rule {
				text, ok := effect.(string)
				if !ok || !opencodePermissionEffects[text] {
					return fmt.Errorf(
						"permission.%s[%q] must be allow, ask or deny, got %v",
						action, pattern, effect,
					)
				}
			}
		default:
			return fmt.Errorf("permission.%s must be an effect or a pattern map", action)
		}
	}
	return nil
}

func (opencodeAdapter) Destination(ctx Context, scope Scope, _ Settings) (Destination, error) {
	switch scope {
	case ScopeProject:
		return Destination{
			Dir:    filepath.Join(ctx.ProjectRoot, ".opencode", "agents"),
			Label:  "project",
			Shared: true,
		}, nil
	case ScopeGlobal:
		return Destination{Dir: filepath.Join(opencodeConfigDir(ctx), "agents"), Label: "user"}, nil
	default:
		return Destination{}, fmt.Errorf("unsupported scope %q", scope)
	}
}

func opencodeConfigDir(ctx Context) string {
	return filepath.Join(ctx.Env.ConfigHome(), "opencode")
}

func (opencodeAdapter) Native(id string, variant *agent.VariantFile) (Artifact, error) {
	doc, err := agent.ParseMarkdown(variant.Path, variant.Raw)
	if err == nil {
		err = validateOpenCode(doc)
	}
	if err != nil {
		return Artifact{}, &InvalidVariantError{
			Harness: agent.OpenCode,
			Path:    variant.Path,
			Reason:  err.Error(),
		}
	}
	var notes []string
	if _, set := doc.Frontmatter["mode"]; !set {
		notes = append(notes, "declares no mode, so OpenCode also offers it as a primary agent")
	}
	if disabled, _ := doc.Frontmatter["disable"].(bool); disabled {
		notes = append(notes, "declares disable: true, so OpenCode will not load it")
	}
	return Artifact{FileName: id + ".md", Content: variant.Raw, Notes: notes}, nil
}

// opencodeActions maps OpenCode permission actions, V1 and V2 spellings, to
// capabilities. Write, patch and multiedit are gated by edit; V2 renamed bash to
// shell and task to subagent.
var opencodeActions = map[string]Capability{
	"read":        CapRead,
	"glob":        CapSearch,
	"grep":        CapSearch,
	"edit":        CapEdit,
	"write":       CapEdit,
	"patch":       CapEdit,
	"apply_patch": CapEdit,
	"multiedit":   CapEdit,
	"bash":        CapShell,
	"shell":       CapShell,
	"webfetch":    CapWebFetch,
	"websearch":   CapWebSearch,
	"task":        CapDelegate,
	"subagent":    CapDelegate,
	"todowrite":   CapTodo,
	"skill":       CapSkill,
}

// opencodeRenderActions are the V1 permission keys conversion writes per
// capability; OpenCode V2 maps bash to shell and task to subagent.
var opencodeRenderActions = []struct {
	cap     Capability
	actions []string
}{
	{CapRead, []string{"read"}},
	{CapSearch, []string{"glob", "grep"}},
	{CapEdit, []string{"edit"}},
	{CapShell, []string{"bash"}},
	{CapWebFetch, []string{"webfetch"}},
	{CapWebSearch, []string{"websearch"}},
	{CapDelegate, []string{"task"}},
	{CapTodo, []string{"todowrite"}},
	{CapSkill, []string{"skill"}},
}

// opencodeInertActions grant nothing an agent could lose: removed V1 actions,
// the user question tool, loop detection and the always-denied browser.
var opencodeInertActions = map[string]bool{
	"list": true, "lsp": true, "codesearch": true, "todoread": true,
	"doom_loop": true, "question": true, "browser": true,
}

// opencodeTuning are model-tuning fields conversion omits with a note.
var opencodeTuning = map[string]bool{
	"temperature": true, "top_p": true, "variant": true, "options": true,
	"request": true, "reasoningEffort": true,
}

// opencodeRule is one permission rule; OpenCode applies the last match.
type opencodeRule struct {
	action string
	effect string
	// pattern is the command, path or agent pattern of a scoped rule.
	pattern string
	// scoped reports a rule limited to some commands, paths or agents.
	scoped bool
}

func (a opencodeAdapter) Semantics(id string, variant *agent.VariantFile) (*Semantics, error) {
	if _, err := a.Native(id, variant); err != nil {
		return nil, err
	}
	doc, _ := agent.ParseMarkdown(variant.Path, variant.Raw)
	sem := &Semantics{
		ID:           id,
		Description:  doc.String("description"),
		Instructions: doc.Body,
		Source:       agent.OpenCode,
	}
	var legacy, current, ordered []opencodeRule
	for _, key := range sortedKeys(doc.Frontmatter) {
		value := doc.Frontmatter[key]
		switch {
		case key == "description" || key == "name":
		case key == "model":
			sem.Model = fmt.Sprint(value)
		case key == "mode":
			if value == "primary" {
				sem.note(
					"OpenCode offers it as a primary agent; other harnesses install it as a subagent",
				)
			}
		case key == "steps" || key == "maxSteps":
			if turns, ok := positiveInt(value); ok {
				sem.MaxTurns = turns
			}
		case key == "color" || key == "hidden":
			sem.note("omitted %s: cosmetic", key)
		case key == "disable" || key == "disabled":
			if value == true {
				sem.unsupported(key, "OpenCode does not load the agent")
			}
		case key == "prompt" || key == "system":
			sem.unsupported(key, "a prompt outside the Markdown body")
		case key == "tools":
			legacy = opencodeToolRules(sem, doc.Raw, value)
		case key == "permission":
			current = opencodePermissionRules(sem, doc.Raw, value)
		case key == "permissions":
			ordered = opencodeOrderedRules(sem, value)
		case opencodeTuning[key]:
			sem.note("omitted %s: model tuning is model-specific", key)
		case harmlessPortableKeys[key]:
			sem.note("omitted %s: descriptive metadata", key)
		default:
			sem.note("omitted %s: OpenCode passes unknown fields to the model provider", key)
		}
	}
	opencodePolicy(sem, append(append(legacy, current...), ordered...))
	return sem, nil
}

// opencodeToolRules reads the legacy tools booleans in authored order.
func opencodeToolRules(sem *Semantics, raw []byte, value any) []opencodeRule {
	tools, _ := value.(map[string]any)
	rules := make([]opencodeRule, 0, len(tools))
	for _, name := range agent.MappingKeys(raw, "tools") {
		effect := "deny"
		if tools[name] == true {
			effect = "allow"
		}
		rules = append(rules, opencodeRule{action: name, effect: effect})
	}
	if len(rules) != len(tools) {
		sem.unsupported("tools", "a tool map agnthub cannot read")
	}
	return rules
}

// opencodePermissionRules reads the V1 permission field in authored order.
func opencodePermissionRules(sem *Semantics, raw []byte, value any) []opencodeRule {
	if effect, ok := value.(string); ok {
		return []opencodeRule{{action: "*", effect: effect}}
	}
	actions, _ := value.(map[string]any)
	rules := make([]opencodeRule, 0, len(actions))
	for _, action := range agent.MappingKeys(raw, "permission") {
		switch rule := actions[action].(type) {
		case string:
			rules = append(rules, opencodeRule{action: action, effect: rule})
		case map[string]any:
			for pattern, effect := range rule {
				text, _ := effect.(string)
				rules = append(rules, opencodeRule{
					action: action, effect: text, pattern: pattern, scoped: pattern != "*",
				})
			}
		}
	}
	if len(agent.MappingKeys(raw, "permission")) != len(actions) {
		sem.unsupported("permission", "a permission map agnthub cannot read")
	}
	return rules
}

// opencodeOrderedRules reads the V2 permissions list of {action, resource, effect}.
func opencodeOrderedRules(sem *Semantics, value any) []opencodeRule {
	entries, ok := value.([]any)
	if !ok {
		sem.unsupported("permissions", "a permission list agnthub cannot read")
		return nil
	}
	rules := make([]opencodeRule, 0, len(entries))
	for _, entry := range entries {
		fields, _ := entry.(map[string]any)
		action, _ := fields["action"].(string)
		effect, _ := fields["effect"].(string)
		resource, hasResource := fields["resource"].(string)
		if action == "" || effect == "" {
			sem.unsupported("permissions", "a permission list agnthub cannot read")
			return nil
		}
		rules = append(rules, opencodeRule{
			action: action, effect: effect, pattern: resource,
			scoped: hasResource && resource != "*",
		})
	}
	return rules
}

// opencodeEnvRules are OpenCode's built-in read rules protecting .env files.
// Conversion repeats them under read, since an agent's read: allow would
// otherwise override them, and parsing ignores them.
var opencodeEnvRules = map[string]string{
	"*":             "allow",
	"*.env":         "ask",
	"*.env.*":       "ask",
	"*.env.example": "allow",
}

// opencodePolicy evaluates rules like OpenCode: everything is allowed unless
// the last rule matching an action says otherwise. A trailing "*" deny makes the
// agent an allowlist. Approval prompts and scoped rules have no equivalent.
func opencodePolicy(sem *Semantics, all []opencodeRule) {
	rules := make([]opencodeRule, 0, len(all))
	for _, rule := range all {
		if rule.scoped && opencodeNormal(rule.action) == "read" &&
			opencodeEnvRules[rule.pattern] == rule.effect {
			continue
		}
		rules = append(rules, rule)
	}
	effectOf := func(action string) string {
		effect := "allow"
		for _, rule := range rules {
			if !rule.scoped && (rule.action == "*" || opencodeNormal(rule.action) == action) {
				effect = rule.effect
			}
		}
		return effect
	}
	for _, rule := range rules {
		field := "permission." + rule.action
		switch {
		case opencodeInertActions[rule.action]:
		case rule.scoped:
			sem.unsupported(field, "allows or denies only some commands, paths or agents")
		case rule.effect == "ask":
			sem.unsupported(field, "asks the user before each use, which other harnesses "+
				"cannot require for one agent")
		}
	}
	restricted := effectOf("*") == "deny"
	sem.Tools.Restricted = restricted
	for _, entry := range opencodeRenderActions {
		var allowed, denied bool
		for _, action := range entry.actions {
			switch effectOf(action) {
			case "allow":
				allowed = true
			case "deny":
				denied = true
			}
		}
		switch {
		case allowed && denied:
			sem.unsupported("permission "+strings.Join(entry.actions, "/"),
				"allows only part of "+entry.cap.String())
		case allowed && restricted:
			sem.Tools.Allowed |= entry.cap
		case denied && !restricted:
			sem.Tools.Denied |= entry.cap
		}
	}
	opencodeOtherActions(sem, rules, effectOf, restricted)
}

// opencodeOtherActions handles actions outside the capability set: MCP and
// custom tools, external directories and Code Mode.
func opencodeOtherActions(
	sem *Semantics,
	rules []opencodeRule,
	effectOf func(string) string,
	restricted bool,
) {
	seen := map[string]bool{}
	for _, rule := range rules {
		action := rule.action
		_, known := opencodeActions[action]
		if action == "*" || known || opencodeInertActions[action] || seen[action] {
			continue
		}
		seen[action] = true
		effect := effectOf(action)
		switch {
		case action == "external_directory" && effect != "allow":
			sem.unsupported("permission.external_directory",
				"limits access outside the project directory")
		case action == "external_directory":
		case action == "execute" && effect == "deny" && !restricted:
			sem.unsupported("permission.execute", "withholds MCP tools reached through Code Mode")
		case action == "execute":
		case effect == "allow" && restricted:
			sem.Tools.Unknown = append(sem.Tools.Unknown, action)
		case effect == "deny" && !restricted:
			sem.unsupported("permission."+action,
				"withholds a custom or MCP tool other harnesses cannot withhold")
		}
	}
}

// opencodeNormal maps V1 and V2 action spellings onto the keys conversion writes.
func opencodeNormal(action string) string {
	switch action {
	case "write", "patch", "apply_patch", "multiedit":
		return "edit"
	case "shell":
		return "bash"
	case "subagent":
		return "task"
	}
	return action
}

// Convert writes an explicit subagent: OpenCode's default mode would otherwise
// also register a registry role as a primary agent. Tool policies become a V1
// permission map, which OpenCode V2 still reads: an allowlist denies "*" first
// (OpenCode applies the last matching rule, so this also hides MCP tools) and
// then allows each granted action, MCP tools included.
func (a opencodeAdapter) Convert(sem *Semantics) (Artifact, error) {
	check, err := begin(agent.OpenCode, sem)
	if err == nil {
		err = check.requireForeground()
	}
	if err != nil {
		return Artifact{}, err
	}
	fields := []agent.Field{
		{Key: "description", Value: sem.Description},
		{Key: "mode", Value: "subagent"},
	}
	if sem.Tools.Limited() {
		permission := map[string]any{}
		if sem.Tools.Restricted {
			permission["*"] = "deny"
		}
		for _, entry := range opencodeRenderActions {
			for _, action := range entry.actions {
				switch {
				case sem.Tools.Restricted && sem.Tools.Allowed&entry.cap != 0 && action == "read":
					permission[action] = opencodeEnvRules
				case sem.Tools.Restricted && sem.Tools.Allowed&entry.cap != 0:
					permission[action] = "allow"
				case !sem.Tools.Restricted && sem.Tools.Denied&entry.cap != 0:
					permission[action] = "deny"
				}
			}
		}
		mcp, err := check.mcpNames(opencodeMCPAction)
		if err != nil {
			return Artifact{}, err
		}
		for _, action := range mcp {
			permission[action] = "allow"
		}
		if len(mcp) > 0 {
			permission[opencodeCodeMode] = "allow"
			if !sem.Tools.Allows(CapWebFetch) {
				check.note("excludes web fetch, but OpenCode reaches MCP tools through its " +
					"Code Mode execute tool, which can also make HTTP requests")
			}
		}
		fields = append(fields, agent.Field{Key: "permission", Value: permission})
	}
	if sem.MaxTurns > 0 {
		fields = append(fields, agent.Field{Key: "steps", Value: sem.MaxTurns})
	}
	content, err := agent.RenderMarkdown(fields, sem.Instructions)
	if err != nil {
		return Artifact{}, err
	}
	return selfCheck(a, sem.ID, Artifact{
		FileName:  sem.ID + ".md",
		Content:   content,
		Generated: true,
		Converter: converterID(agent.OpenCode),
		Notes:     check.notes,
	})
}

// opencodeCodeMode is the action of OpenCode's Code Mode execute tool, through
// which agents call MCP tools unless a server opts out of Code Mode.
const opencodeCodeMode = "execute"

// opencodeMCPAction is the permission action OpenCode checks before calling an
// MCP tool: <server>_<tool>, with characters other than letters, digits, '_'
// and '-' replaced by '_'; <server>_* matches every tool of the server. An
// action that would also match a built-in action, or OpenCode's own opencode_
// tools, is refused rather than granting that built-in.
func opencodeMCPAction(tool MCPTool) (action, reason string) {
	server := claudeNormalize(tool.Server)
	if server == "opencode" {
		return "", "OpenCode names its own tools opencode_<tool>"
	}
	prefix := server + "_"
	if tool.Tool != "" {
		action = prefix + claudeNormalize(tool.Tool)
	} else {
		action = prefix + "*"
	}
	builtins := slices.Concat([]string{opencodeCodeMode},
		slices.Sorted(maps.Keys(opencodeActions)), slices.Sorted(maps.Keys(opencodeInertActions)))
	for _, builtin := range builtins {
		if builtin == action || tool.Tool == "" && strings.HasPrefix(builtin, prefix) {
			return "", "its OpenCode permission " + action + " would also match the " +
				"built-in action " + builtin
		}
	}
	return action, ""
}

func (opencodeAdapter) Detect(ctx Context) bool {
	return detect(ctx, "opencode", opencodeConfigDir(ctx))
}
