package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/heynemann/agnthub/internal/agent"
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
		return NativeAgent{}, fmt.Errorf("%s: frontmatter requires a description", doc.Path)
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
	doc, _ := agent.ParseMarkdownLenient(variant.Path, variant.Raw)
	return Artifact{
		FileName: id + ".md",
		Content:  variant.Raw,
		Notes:    executableFieldNotes(doc, "hooks", "mcpServers"),
	}, nil
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

func (claudeAdapter) Convert(id string, portable *agent.Document) (Artifact, error) {
	core, err := ExtractPortable(agent.Claude, id, portable)
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
		Converter: "claude/v1",
		Notes:     core.OmissionNotes(),
	}, nil
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
