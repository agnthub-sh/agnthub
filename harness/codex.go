package harness

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/pelletier/go-toml/v2"

	"github.com/heynemann/agnthub/agent"
)

// codexAdapter installs Codex standalone TOML custom agents.
//
// Schema (developers.openai.com/codex, "Custom agent file schema"): required
// name, description and developer_instructions; optional model settings and
// capability reductions. Permissions, provider and MCP servers always come from
// the parent session, so agnthub never writes them and refuses Variants that
// declare them.
type codexAdapter struct{}

func (codexAdapter) ID() agent.HarnessID { return agent.Codex }

func (codexAdapter) Name() string { return "Codex" }

func (codexAdapter) Layout() NativeLayout {
	return NativeLayout{Dir: ".codex/agents", Suffixes: []string{".toml"}}
}

func (codexAdapter) Settings() []SettingSpec { return nil }

func (codexAdapter) Identify(filePath string, raw []byte) (NativeAgent, error) {
	fields, err := decodeCodexAgent(filePath, raw)
	if err != nil {
		return NativeAgent{}, err
	}
	return codexIdentity(filePath, fields)
}

func decodeCodexAgent(filePath string, raw []byte) (map[string]any, error) {
	var fields map[string]any
	if err := toml.Unmarshal(raw, &fields); err != nil {
		return nil, fmt.Errorf("%s: invalid TOML: %w", filePath, err)
	}
	return fields, nil
}

func codexIdentity(filePath string, fields map[string]any) (NativeAgent, error) {
	name, _ := fields["name"].(string)
	description, _ := fields["description"].(string)
	instructions, _ := fields["developer_instructions"].(string)
	switch {
	case name == "":
		return NativeAgent{}, fmt.Errorf("%s: requires a string name", filePath)
	case strings.TrimSpace(description) == "":
		return NativeAgent{}, fmt.Errorf("%s: requires a description", filePath)
	case strings.TrimSpace(instructions) == "":
		return NativeAgent{}, fmt.Errorf("%s: requires developer_instructions", filePath)
	}
	return NativeAgent{ID: name, Description: description}, nil
}

func (codexAdapter) Destination(ctx Context, scope Scope, _ Settings) (Destination, error) {
	switch scope {
	case ScopeProject:
		return Destination{
			Dir:    filepath.Join(ctx.ProjectRoot, ".codex", "agents"),
			Label:  "project",
			Shared: true,
		}, nil
	case ScopeGlobal:
		return Destination{Dir: filepath.Join(codexHome(ctx), "agents"), Label: "user"}, nil
	default:
		return Destination{}, fmt.Errorf("unsupported scope %q", scope)
	}
}

func codexHome(ctx Context) string {
	if dir := ctx.Env.Get("CODEX_HOME"); filepath.IsAbs(dir) {
		return dir
	}
	return filepath.Join(ctx.Env.Home(), ".codex")
}

func (codexAdapter) Detect(ctx Context) bool {
	return detect(ctx, "codex", codexHome(ctx))
}

func (codexAdapter) Native(id string, variant *agent.VariantFile) (Artifact, error) {
	invalid := func(reason string) error {
		return &InvalidVariantError{Harness: agent.Codex, Path: variant.Path, Reason: reason}
	}
	fields, err := decodeCodexAgent(variant.Path, variant.Raw)
	if err != nil {
		return Artifact{}, invalid(err.Error())
	}
	identity, err := codexIdentity(variant.Path, fields)
	if err != nil {
		return Artifact{}, invalid(err.Error())
	}
	if identity.ID != id {
		return Artifact{}, invalid(
			fmt.Sprintf("name %q does not match agent %q", identity.ID, id),
		)
	}
	if reason := codexUnsupported(fields); reason != "" {
		return Artifact{}, invalid(reason)
	}
	return Artifact{FileName: id + ".toml", Content: variant.Raw}, nil
}

// codexParentKeys are session settings Codex always takes from the parent
// configuration; an agent file declaring them would claim a constraint Codex
// does not apply.
var codexParentKeys = map[string]bool{
	"sandbox_mode":             true,
	"sandbox_workspace_write":  true,
	"approval_policy":          true,
	"approvals_reviewer":       true,
	"default_permissions":      true,
	"permissions":              true,
	"model_provider":           true,
	"model_providers":          true,
	"mcp_servers":              true,
	"shell_environment_policy": true,
	"profile":                  true,
	"profiles":                 true,
	"hooks":                    true,
	"service_tier":             true,
}

// codexStringKeys are the documented optional model settings.
var codexStringKeys = map[string]bool{
	"model":                   true,
	"model_reasoning_effort":  true,
	"model_reasoning_summary": true,
	"model_verbosity":         true,
}

// codexFeatureKeys are the capabilities an agent file may disable.
var codexFeatureKeys = map[string]bool{
	"shell_tool":               true,
	"apps":                     true,
	"plugins":                  true,
	"memories":                 true,
	"request_permissions_tool": true,
}

// codexUnsupported returns why fields fall outside the documented agent schema,
// or "" when every key is supported.
func codexUnsupported(fields map[string]any) string {
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := fields[key]
		switch {
		case key == "name" || key == "description" || key == "developer_instructions":
		case codexStringKeys[key]:
			if _, ok := value.(string); !ok {
				return fmt.Sprintf("%s must be a string", key)
			}
		case key == "features":
			if reason := codexFeatures(value); reason != "" {
				return reason
			}
		case key == "skills":
			if reason := codexSkills(value); reason != "" {
				return reason
			}
		case codexParentKeys[key]:
			return fmt.Sprintf(
				"%q is parent session configuration that a Codex agent file cannot override",
				key,
			)
		default:
			return fmt.Sprintf("%q is not a supported Codex agent field", key)
		}
	}
	return ""
}

func codexFeatures(value any) string {
	table, ok := value.(map[string]any)
	if !ok {
		return "features must be a table"
	}
	for key, enabled := range table {
		if !codexFeatureKeys[key] {
			return fmt.Sprintf("features.%s cannot be set by a Codex agent file", key)
		}
		if enabled != false {
			return fmt.Sprintf("features.%s may only be set to false", key)
		}
	}
	return ""
}

func codexSkills(value any) string {
	table, ok := value.(map[string]any)
	if !ok {
		return "skills must be a table"
	}
	for key, setting := range table {
		switch key {
		case "include_instructions":
			if setting != false {
				return "skills.include_instructions may only be set to false"
			}
		case "bundled":
			bundled, ok := setting.(map[string]any)
			if !ok || len(bundled) != 1 || bundled["enabled"] != false {
				return "skills.bundled may only set enabled = false"
			}
		case "config":
			if reason := codexSkillConfig(setting); reason != "" {
				return reason
			}
		default:
			return fmt.Sprintf("skills.%s cannot be set by a Codex agent file", key)
		}
	}
	return ""
}

func codexSkillConfig(value any) string {
	const reason = "skills.config entries may only disable a skill by path (enabled = false)"
	entries, ok := value.([]any)
	if !ok {
		return reason
	}
	for _, entry := range entries {
		skill, ok := entry.(map[string]any)
		if !ok || len(skill) != 2 || skill["enabled"] != false {
			return reason
		}
		if path, ok := skill["path"].(string); !ok || path == "" {
			return reason
		}
	}
	return ""
}

// codexAgentFile is a generated agent, in schema field order.
type codexAgentFile struct {
	Name                  string             `toml:"name"`
	Description           string             `toml:"description"`
	DeveloperInstructions string             `toml:"developer_instructions,multiline"`
	Features              *codexFeatureTable `toml:"features,omitempty"`
}

// codexFeatureTable holds the capability reductions conversion writes.
type codexFeatureTable struct {
	ShellTool bool `toml:"shell_tool"`
}

func (a codexAdapter) Semantics(id string, variant *agent.VariantFile) (*Semantics, error) {
	if _, err := a.Native(id, variant); err != nil {
		return nil, err
	}
	fields, _ := decodeCodexAgent(variant.Path, variant.Raw)
	sem := &Semantics{Source: agent.Codex}
	sem.ID, _ = fields["name"].(string)
	sem.Description, _ = fields["description"].(string)
	sem.Instructions, _ = fields["developer_instructions"].(string)
	for _, key := range sortedKeys(fields) {
		switch key {
		case "name", "description", "developer_instructions":
		case "model":
			sem.Model, _ = fields[key].(string)
		case "features":
			table, _ := fields[key].(map[string]any)
			for _, feature := range sortedKeys(table) {
				if feature == "shell_tool" {
					sem.Tools.Denied |= CapShell
					continue
				}
				sem.note(
					"omitted features.%s = false: a Codex capability other harnesses do not have",
					feature,
				)
			}
		case "skills":
			sem.note("omitted skills settings: they only disable Codex skills")
		default:
			sem.note("omitted %s: model settings are model-specific", key)
		}
	}
	return sem, nil
}

// Convert writes a standalone agent. Codex has no per-agent tool allowlist and
// no agent-local read-only mode, so an Agent withholding reads, searches or
// edits is refused; withheld shell becomes features.shell_tool = false. Web
// search, delegation and MCP servers follow the parent session and are noted.
func (a codexAdapter) Convert(sem *Semantics) (Artifact, error) {
	check, err := begin(agent.Codex, sem)
	if err == nil {
		err = check.requireWithheld(CapRead|CapSearch|CapEdit,
			"a Codex agent file has no tool allowlist or read-only mode to withhold it")
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
	if !utf8.ValidString(sem.Description) || !utf8.ValidString(sem.Instructions) {
		return Artifact{}, check.refuse("TOML requires UTF-8 text; the %s is not valid UTF-8",
			sem.sourceLabel())
	}
	check.noteWithheld(CapWeb|CapDelegate,
		"Codex agents use the session's web search and subagent settings")
	if sem.Tools.Restricted {
		check.note(
			"Codex agents keep the session's MCP servers; an agent file cannot withhold them",
		)
		check.mcpSession()
	}
	file := codexAgentFile{
		Name:                  sem.ID,
		Description:           sem.Description,
		DeveloperInstructions: sem.Instructions,
	}
	if !sem.Tools.Allows(CapShell) {
		file.Features = &codexFeatureTable{ShellTool: false}
	}
	content, err := toml.Marshal(file)
	if err != nil {
		return Artifact{}, fmt.Errorf("serialize Codex agent %q: %w", sem.ID, err)
	}
	var parsed codexAgentFile
	if err := toml.Unmarshal(content, &parsed); err != nil || parsed.Name != file.Name ||
		parsed.Description != file.Description ||
		parsed.DeveloperInstructions != file.DeveloperInstructions ||
		(parsed.Features == nil) != (file.Features == nil) {
		return Artifact{}, fmt.Errorf("serialize Codex agent %q: text does not round-trip", sem.ID)
	}
	return selfCheck(a, sem.ID, Artifact{
		FileName:  sem.ID + ".toml",
		Content:   content,
		Generated: true,
		Converter: converterID(agent.Codex),
		Notes:     check.notes,
	})
}
