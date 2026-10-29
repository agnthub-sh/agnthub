package harness

import (
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/heynemann/agnthub/internal/agent"
)

// cursorAdapter installs Cursor subagents (Markdown with YAML frontmatter).
//
// Cursor does not parse the frontmatter as YAML: it reads one "key: value" pair
// per line, lowercases keys and keeps values verbatim, and only `readonly: true`
// enables its read-only permission mode. Validation and conversion therefore
// check what Cursor itself will read, not only what YAML decodes.
type cursorAdapter struct{}

// cursorFields are the documented Cursor subagent frontmatter controls.
var cursorFields = map[string]bool{
	"name":          true,
	"description":   true,
	"model":         true,
	"readonly":      true,
	"is_background": true,
}

var cursorNameSeparators = regexp.MustCompile(`[\s_]+`)

func (cursorAdapter) ID() agent.HarnessID { return agent.Cursor }

func (cursorAdapter) Name() string { return "Cursor" }

func (cursorAdapter) Layout() NativeLayout {
	return NativeLayout{Dir: ".cursor/agents", Suffixes: []string{".md"}}
}

func (cursorAdapter) Settings() []SettingSpec { return nil }

func (cursorAdapter) Identify(filePath string, raw []byte) (NativeAgent, error) {
	doc, err := agent.ParseMarkdown(filePath, raw)
	if err != nil {
		return NativeAgent{}, err
	}
	description, ok := doc.Frontmatter["description"].(string)
	if !ok || strings.TrimSpace(description) == "" {
		return NativeAgent{}, fmt.Errorf("%s: frontmatter requires a description", filePath)
	}
	id, _ := doc.Frontmatter["name"].(string)
	if id == "" {
		id = cursorFileName(filePath)
	}
	return NativeAgent{ID: id, Description: description}, nil
}

// cursorFileName is the name Cursor derives from a file without a declared name.
func cursorFileName(filePath string) string {
	base := path.Base(filepath.ToSlash(filePath))
	return cursorNameSeparators.ReplaceAllString(strings.TrimSuffix(base, path.Ext(base)), "-")
}

func (cursorAdapter) Destination(ctx Context, scope Scope, _ Settings) (Destination, error) {
	switch scope {
	case ScopeProject:
		return Destination{
			Dir:    filepath.Join(ctx.ProjectRoot, ".cursor", "agents"),
			Label:  "project",
			Shared: true,
		}, nil
	case ScopeGlobal:
		return Destination{Dir: cursorAgentsDir(ctx), Label: "user"}, nil
	default:
		return Destination{}, fmt.Errorf("unsupported scope %q", scope)
	}
}

func cursorAgentsDir(ctx Context) string {
	return filepath.Join(ctx.Env.Home(), ".cursor", "agents")
}

func (cursorAdapter) Native(id string, variant *agent.VariantFile) (Artifact, error) {
	if reason := validateCursorVariant(id, variant); reason != "" {
		return Artifact{}, &InvalidVariantError{
			Harness: agent.Cursor,
			Path:    variant.Path,
			Reason:  reason,
		}
	}
	return Artifact{FileName: id + ".md", Content: variant.Raw}, nil
}

// validateCursorVariant returns why Cursor would not load variant as id with its
// declared behavior, or "" when it would.
func validateCursorVariant(id string, variant *agent.VariantFile) string {
	doc, err := agent.ParseMarkdown(variant.Path, variant.Raw)
	if err != nil {
		return err.Error()
	}
	for key := range doc.Frontmatter {
		if !cursorFields[key] && !harmlessPortableKeys[key] {
			return fmt.Sprintf(
				"Cursor does not document %q; its behavior would not be enforced", key,
			)
		}
	}
	if strings.TrimSpace(doc.String("description")) == "" {
		return "frontmatter requires a description"
	}
	if strings.TrimSpace(doc.Body) == "" {
		return "Cursor skips subagents without instructions"
	}
	if value, ok := doc.Frontmatter["model"]; ok {
		if model, isString := value.(string); !isString || strings.TrimSpace(model) == "" {
			return "model must be a model ID or inherit"
		}
	}
	for _, key := range []string{"readonly", "is_background"} {
		if value, ok := doc.Frontmatter[key]; ok {
			if _, isBool := value.(bool); !isBool {
				return key + " must be a boolean"
			}
		}
	}
	return cursorReadsAs(id, doc)
}

// cursorReadsAs compares what Cursor's line reader sees with the decoded YAML and
// returns why they disagree on identity, description or restrictions.
func cursorReadsAs(id string, doc *agent.Document) string {
	seen := cursorFrontmatter(doc.Raw)
	name, declared := doc.Frontmatter["name"]
	switch {
	case declared && name != id:
		return fmt.Sprintf("name %v does not match agent %q", name, id)
	case declared && seen["name"] != id:
		return fmt.Sprintf(
			"Cursor reads the name as %q; write name: %s on one line",
			seen["name"],
			id,
		)
	case !declared && cursorFileName(id) != id:
		return fmt.Sprintf(
			"Cursor would name the file %q; declare name: %s",
			cursorFileName(id),
			id,
		)
	}
	if unquote(seen["description"]) != doc.String("description") {
		return "Cursor reads one line per key; write the description on a single line"
	}
	for _, key := range []string{"readonly", "is_background"} {
		declaredTrue, _ := doc.Frontmatter[key].(bool)
		readTrue := strings.EqualFold(seen[key], "true")
		if key == "is_background" && seen["background"] != "" {
			readTrue = strings.EqualFold(seen["background"], "true")
		}
		if declaredTrue != readTrue {
			return fmt.Sprintf(
				"Cursor would read %s as %t but the frontmatter declares %t; write it once as a top-level %q line",
				key,
				readTrue,
				declaredTrue,
				fmt.Sprintf("%s: %t", key, declaredTrue),
			)
		}
	}
	return ""
}

// cursorFrontmatter mirrors Cursor's subagent frontmatter reader: every non-empty,
// non-comment line with a colon sets a lowercased key to the trimmed raw value.
func cursorFrontmatter(raw []byte) map[string]string {
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	text = strings.TrimPrefix(text, "\uFEFF")
	fields := map[string]string{}
	rest, ok := strings.CutPrefix(text, "---\n")
	if !ok {
		return fields
	}
	header, _, _ := strings.Cut(rest, "\n---")
	for line := range strings.SplitSeq(header, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, ":")
		key = strings.ToLower(strings.TrimSpace(key))
		if !found || key == "" {
			continue
		}
		fields[key] = strings.TrimSpace(value)
	}
	return fields
}

// unquote strips one pair of matching YAML quotes Cursor would keep verbatim.
func unquote(value string) string {
	if len(value) >= 2 {
		first, last := value[0], value[len(value)-1]
		if first == last && (first == '"' || first == '\'') {
			return value[1 : len(value)-1]
		}
	}
	return value
}

func (cursorAdapter) Convert(id string, portable *agent.Document) (Artifact, error) {
	core, err := ExtractPortable(agent.Cursor, id, portable)
	if err != nil {
		return Artifact{}, err
	}
	notes := core.OmissionNotes()
	description := strings.Join(strings.Fields(core.Description), " ")
	if description != strings.TrimSpace(core.Description) {
		notes = append(notes, "description joined onto one line: Cursor reads one line per key")
	}
	content, err := agent.RenderMarkdown([]agent.Field{
		{Key: "name", Value: core.ID},
		{Key: "description", Value: description},
	}, core.Instructions)
	if err != nil {
		return Artifact{}, err
	}
	seen := cursorFrontmatter(content)
	if seen["name"] != core.ID || unquote(seen["description"]) != description {
		return Artifact{}, &IncompatibleError{
			Harness: agent.Cursor,
			Reason:  "description cannot be written in a form Cursor reads unchanged; author a native cursor variant",
		}
	}
	return Artifact{
		FileName:  id + ".md",
		Content:   content,
		Generated: true,
		Converter: "cursor/v1",
		Notes:     notes,
	}, nil
}

func (cursorAdapter) Detect(ctx Context) bool {
	return detect(ctx, "cursor-agent", filepath.Join(ctx.Env.Home(), ".cursor")) ||
		detect(ctx, "cursor")
}
