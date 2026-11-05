package harness

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/pelletier/go-toml/v2"

	"github.com/agnthub-sh/agnthub/agent"
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
	identity := NativeAgent{ID: name, Description: description}
	switch {
	case name == "":
		return NativeAgent{}, fmt.Errorf("%s: requires a string name", filePath)
	case strings.TrimSpace(description) == "":
		return identity, fmt.Errorf("%s: requires a description", filePath)
	case strings.TrimSpace(instructions) == "":
		return identity, fmt.Errorf("%s: requires developer_instructions", filePath)
	}
	return identity, nil
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
		if err := requireAbsoluteDir(ctx, "CODEX_HOME", "Codex"); err != nil {
			return Destination{}, err
		}
		return Destination{Dir: filepath.Join(codexHome(ctx), "agents"), Label: "user"}, nil
	default:
		return Destination{}, fmt.Errorf("unsupported scope %q", scope)
	}
}

// codexHome is Codex's user directory: CODEX_HOME or ~/.codex.
func codexHome(ctx Context) string {
	if dir := ctx.Env.Get("CODEX_HOME"); filepath.IsAbs(dir) {
		return dir
	}
	return filepath.Join(ctx.Env.Home(), ".codex")
}

func (codexAdapter) Detect(ctx Context) bool {
	return detect(ctx, "codex", codexHome(ctx))
}

// Native installs an authored agent as written. One declaring sandbox_mode =
// "read-only" is refused: Codex runs custom agents in the parent session's
// sandbox, so the read-only mode it declares would not be enforced.
func (codexAdapter) Native(id string, variant *agent.VariantFile) (Artifact, error) {
	fields, notes, err := codexVariant(id, variant)
	if err != nil {
		return Artifact{}, err
	}
	if fields["sandbox_mode"] == codexReadOnly {
		return Artifact{}, &IncompatibleError{
			Harness: agent.Codex,
			Reason: `its codex variant declares sandbox_mode = "read-only", which Codex does ` +
				"not enforce: custom agents run in the parent session's sandbox, so the agent " +
				"could edit files",
		}
	}
	return Artifact{FileName: id + ".toml", Content: variant.Raw, Notes: notes}, nil
}

// codexVariant validates an authored agent for id and returns its fields and
// the notes on settings Codex parses but does not apply.
func codexVariant(id string, variant *agent.VariantFile) (map[string]any, []string, error) {
	invalid := func(reason string) error {
		return &InvalidVariantError{Harness: agent.Codex, Path: variant.Path, Reason: reason}
	}
	fields, err := decodeCodexAgent(variant.Path, variant.Raw)
	if err != nil {
		return nil, nil, invalid(err.Error())
	}
	identity, err := codexIdentity(variant.Path, fields)
	if err != nil {
		return nil, nil, invalid(err.Error())
	}
	if identity.ID != id {
		return nil, nil, invalid(fmt.Sprintf("name %q does not match agent %q", identity.ID, id))
	}
	notes, reason := codexUnsupported(fields)
	if reason != "" {
		return nil, nil, invalid(reason)
	}
	return fields, notes, nil
}

// codexReadOnly is the sandbox_mode that withholds edits.
const codexReadOnly = "read-only"

// codexSandboxModes are the sandbox_mode values Codex parses in an agent file
// and ignores: the parent session's sandbox always applies.
var codexSandboxModes = map[string]bool{
	codexReadOnly: true, "workspace-write": true, "danger-full-access": true,
}

// codexParentKeys are session settings Codex always takes from the parent
// configuration; an agent file declaring them would claim a constraint Codex
// does not apply. sandbox_mode is read separately (codexSandboxModes).
var codexParentKeys = map[string]bool{
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
// or "" when every key is supported, plus notes on keys Codex parses but does
// not apply.
func codexUnsupported(fields map[string]any) ([]string, string) {
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var notes []string
	for _, key := range keys {
		value := fields[key]
		switch {
		case key == "name" || key == "description" || key == "developer_instructions":
		case codexStringKeys[key]:
			if _, ok := value.(string); !ok {
				return nil, fmt.Sprintf("%s must be a string", key)
			}
		case key == "features":
			note, reason := codexFeatures(value)
			if reason != "" {
				return nil, reason
			}
			notes = append(notes, note...)
		case key == "skills":
			if reason := codexSkills(value); reason != "" {
				return nil, reason
			}
		case key == "nickname_candidates":
			if reason := codexNicknames(value); reason != "" {
				return nil, reason
			}
		case key == "sandbox_mode":
			mode, _ := value.(string)
			if !codexSandboxModes[mode] {
				return nil, "sandbox_mode must be read-only, workspace-write or danger-full-access"
			}
			if mode != codexReadOnly {
				notes = append(notes, fmt.Sprintf("sandbox_mode = %q is not applied: Codex "+
					"runs custom agents in the parent session's sandbox", mode))
			}
		case codexParentKeys[key]:
			return nil, fmt.Sprintf(
				"%q is parent session configuration that a Codex agent file cannot override",
				key,
			)
		default:
			return nil, fmt.Sprintf("%q is not a supported Codex agent field", key)
		}
	}
	return notes, ""
}

// codexNicknames checks nickname_candidates, the display names Codex picks
// from for an agent it spawns, the way Codex 0.164.0 loads an agent file: a
// non-empty list of distinct, non-blank names of ASCII letters, digits, spaces,
// hyphens and underscores. Codex skips an agent file with any other value.
func codexNicknames(value any) string {
	const reason = "nickname_candidates must be a non-empty list of distinct names of " +
		"ASCII letters, digits, spaces, hyphens and underscores"
	names, ok := value.([]any)
	if !ok || len(names) == 0 {
		return reason
	}
	seen := make(map[string]bool, len(names))
	for _, entry := range names {
		name, ok := entry.(string)
		if !ok || strings.TrimSpace(name) == "" || seen[name] {
			return reason
		}
		seen[name] = true
		for _, r := range name {
			if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') &&
				r != ' ' && r != '-' && r != '_' {
				return reason
			}
		}
	}
	return ""
}

// codexFeatures checks the features table: disable-only capability keys, and
// multi_agent, which Codex parses but does not apply from an agent file (the
// agent keeps the session's subagents), so it installs with a warning.
func codexFeatures(value any) ([]string, string) {
	table, ok := value.(map[string]any)
	if !ok {
		return nil, "features must be a table"
	}
	var notes []string
	for _, key := range sortedKeys(table) {
		enabled := table[key]
		switch {
		case key == codexMultiAgent:
			if _, isBool := enabled.(bool); !isBool {
				return nil, "features.multi_agent must be true or false"
			}
			if enabled == false {
				notes = append(notes, "Codex does not enforce features.multi_agent = false in "+
					"an agent file: on Codex the agent may still delegate; other harnesses "+
					"enforce it")
			}
		case !codexFeatureKeys[key]:
			return nil, fmt.Sprintf("features.%s cannot be set by a Codex agent file", key)
		case enabled != false:
			return nil, fmt.Sprintf("features.%s may only be set to false", key)
		}
	}
	return notes, ""
}

// codexMultiAgent is the feature that lets a Codex agent spawn subagents.
const codexMultiAgent = "multi_agent"

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
	fields, _, err := codexVariant(id, variant)
	if err != nil {
		return nil, err
	}
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
				switch {
				case feature == "shell_tool":
					sem.Tools.Denied |= CapShell
				case feature == codexMultiAgent && table[feature] == false:
					sem.Tools.Denied |= CapDelegate
					sem.relaxed = true
					sem.note("features.multi_agent = false: converted without delegation")
				case feature == codexMultiAgent:
					sem.relaxed = true
				default:
					sem.note("omitted features.%s = false: a Codex capability other harnesses "+
						"do not have", feature)
				}
			}
		case "sandbox_mode":
			sem.relaxed = true
			if fields[key] == codexReadOnly {
				sem.Tools.Denied |= CapEdit
				sem.note(`sandbox_mode = "read-only": converted without edit tools; ` +
					"shell commands are not sandboxed outside Codex")
				continue
			}
			sem.note("omitted sandbox_mode = %q: Codex ignores it in agent files; the "+
				"session's sandbox and approvals apply", fields[key])
		case "skills":
			sem.note("omitted skills settings: they only disable Codex skills")
		case "nickname_candidates":
			sem.note("omitted nickname_candidates: display names for Codex's spawned agents")
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
	if err != nil {
		return Artifact{}, err
	}
	sem = check.sem
	if err := check.requireWithheld(CapRead|CapSearch|CapEdit,
		"a Codex agent file has no tool allowlist or read-only mode to withhold it"); err != nil {
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
		Name:        sem.ID,
		Description: sem.Description,
		DeveloperInstructions: check.carry(additiveSupport{
			noUserMemory: "Codex's sandbox keeps agents from writing outside the project",
			loadSkill: "Each one is listed among your available skills with the path of its " +
				"SKILL.md: read that file.",
			sessionSkills: true,
			tools:         sem.Tools,
		}),
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
	return selfCheck(a, sem.ID, check.artifact(sem.ID+".toml", content))
}
