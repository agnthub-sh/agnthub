package harness

import (
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/heynemann/agnthub/internal/agent"
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

func (copilotAdapter) Convert(id string, portable *agent.Document) (Artifact, error) {
	core, err := ExtractPortable(agent.Copilot, id, portable)
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
		FileName:  id + ".agent.md",
		Content:   content,
		Generated: true,
		Converter: "copilot/v1",
		Notes:     core.OmissionNotes(),
	}, nil
}

func (copilotAdapter) Detect(ctx Context) bool {
	return detect(ctx, "copilot", copilotHome(ctx))
}
