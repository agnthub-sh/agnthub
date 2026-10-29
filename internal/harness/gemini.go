package harness

import (
	"fmt"
	"math"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/heynemann/agnthub/internal/agent"
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

func (geminiAdapter) Convert(id string, portable *agent.Document) (Artifact, error) {
	if portable != nil {
		for _, key := range delegationKeys {
			if _, ok := portable.Frontmatter[key]; ok {
				return Artifact{}, &IncompatibleError{
					Harness: agent.Gemini,
					Reason: fmt.Sprintf(
						"portable definition declares %q, but Gemini CLI subagents cannot invoke other agents",
						key,
					),
				}
			}
		}
	}
	core, err := ExtractPortable(agent.Gemini, id, portable)
	if err != nil {
		return Artifact{}, err
	}
	content, err := agent.RenderMarkdown([]agent.Field{
		{Key: "name", Value: core.ID},
		{Key: "description", Value: core.Description},
	}, core.Instructions)
	if err != nil {
		return Artifact{}, err
	}
	return Artifact{
		FileName:  id + ".md",
		Content:   content,
		Generated: true,
		Converter: "gemini/v1",
		Notes:     core.OmissionNotes(),
	}, nil
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
