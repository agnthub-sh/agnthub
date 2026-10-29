package harness

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/heynemann/agnthub/internal/agent"
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

// Convert writes the portable core as an explicit subagent: OpenCode's default
// mode would otherwise also register a registry role as a primary agent.
func (opencodeAdapter) Convert(id string, portable *agent.Document) (Artifact, error) {
	core, err := ExtractPortable(agent.OpenCode, id, portable)
	if err != nil {
		return Artifact{}, err
	}
	content, err := agent.RenderMarkdown([]agent.Field{
		{Key: "description", Value: core.Description},
		{Key: "mode", Value: "subagent"},
	}, core.Instructions)
	if err != nil {
		return Artifact{}, err
	}
	return Artifact{
		FileName:  id + ".md",
		Content:   content,
		Generated: true,
		Converter: "opencode/v1",
		Notes:     core.OmissionNotes(),
	}, nil
}

func (opencodeAdapter) Detect(ctx Context) bool {
	return detect(ctx, "opencode", opencodeConfigDir(ctx))
}
