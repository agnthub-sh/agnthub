package harness

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/heynemann/agnthub/agent"
)

// omp profile rules mirror omp's normalizeProfileName (pi-utils dirs.ts): lowercase
// names usable as one directory component, never ending in '.', never a Windows
// reserved device name. "default" selects the default profile.
var (
	ompProfilePattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	ompReservedPattern = regexp.MustCompile(`^(con|prn|aux|nul|com[0-9]|lpt[0-9])(\..*)?$`)
)

const (
	ompProfileKey     = "profile"
	ompDefaultProfile = "default"
	ompConfigDirName  = ".omp"
)

// ompReservedNames are session sentinels omp refuses as custom agent names.
var ompReservedNames = map[string]bool{"main": true, "sub": true}

// ompAdapter installs omp (oh-my-pi) task agents: Markdown with YAML frontmatter in
// the nearest project .omp/agents or the selected profile's user agents directory.
type ompAdapter struct{}

func (ompAdapter) ID() agent.HarnessID { return agent.OMP }

func (ompAdapter) Name() string { return "omp" }

func (ompAdapter) Layout() NativeLayout {
	return NativeLayout{Dir: ".omp/agents", Suffixes: []string{".md"}}
}

func (ompAdapter) Settings() []SettingSpec {
	return []SettingSpec{
		{
			Key:         ompProfileKey,
			Description: "omp profile whose agent directory and configuration apply (\"default\" for the default profile)",
			Validate:    validateOMPProfile,
		},
	}
}

func validateOMPProfile(value string) error {
	if value == ompDefaultProfile {
		return nil
	}
	if !ompProfilePattern.MatchString(value) || strings.HasSuffix(value, ".") ||
		ompReservedPattern.MatchString(value) {
		return fmt.Errorf(
			"invalid omp profile %q: use lowercase letters, digits, '.', '_' or '-' "+
				"(max 64, not ending in '.', not a reserved device name), or %q",
			value, ompDefaultProfile,
		)
	}
	return nil
}

func (ompAdapter) Identify(filePath string, raw []byte) (NativeAgent, error) {
	doc, err := agent.ParseMarkdown(filePath, raw)
	if err != nil {
		return NativeAgent{}, err
	}
	identity, err := requireNamed(doc)
	if err != nil {
		return NativeAgent{}, err
	}
	if ompReservedNames[strings.ToLower(strings.TrimSpace(identity.ID))] {
		return NativeAgent{}, fmt.Errorf(
			"%s: name %q is reserved by omp for session rules",
			filePath,
			identity.ID,
		)
	}
	return identity, nil
}

// ompProfile is the effective profile of one omp binding.
type ompProfile struct {
	// name is the named profile, or "" for the default profile.
	name string
	// source is the environment variable the profile was detected from, or "" when
	// it was given explicitly or nothing selected one.
	source string
	// explicit reports a profile chosen with --extra omp.profile.
	explicit bool
}

func (p ompProfile) label() string {
	label := "default profile"
	if p.name != "" {
		label = "profile " + p.name
	}
	if p.source != "" {
		label += ", from " + p.source
	}
	return label
}

// normalizeOMPProfile applies omp's environment normalization: surrounding space is
// ignored and an empty value or "default" selects the default profile.
func normalizeOMPProfile(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || trimmed == ompDefaultProfile {
		return "", nil
	}
	if err := validateOMPProfile(trimmed); err != nil {
		return "", err
	}
	return trimmed, nil
}

// resolveOMPProfile prefers the explicit setting over omp's detected default:
// OMP_PROFILE when set (even empty), otherwise the legacy PI_PROFILE.
func resolveOMPProfile(ctx Context, settings Settings) (ompProfile, error) {
	if value, ok := settings[ompProfileKey]; ok {
		if err := validateOMPProfile(value); err != nil {
			return ompProfile{}, err
		}
		name, _ := normalizeOMPProfile(value)
		return ompProfile{name: name, explicit: true}, nil
	}
	for _, key := range []string{"OMP_PROFILE", "PI_PROFILE"} {
		value, ok := ctx.Env.Lookup(key)
		if !ok {
			continue
		}
		name, err := normalizeOMPProfile(value)
		if err != nil {
			return ompProfile{}, fmt.Errorf(
				"%s does not name a valid omp profile (%w); choose one with --extra omp.profile=<name>",
				key,
				err,
			)
		}
		return ompProfile{name: name, source: key}, nil
	}
	return ompProfile{}, nil
}

// EffectiveSettings reports the one profile the plan binds to.
func (ompAdapter) EffectiveSettings(ctx Context, settings Settings) []EffectiveSetting {
	profile, err := resolveOMPProfile(ctx, settings)
	if err != nil {
		return nil
	}
	setting := EffectiveSetting{Key: ompProfileKey, Value: profile.name, Source: profile.source}
	if setting.Value == "" {
		setting.Value = ompDefaultProfile
	}
	switch {
	case profile.explicit:
		setting.Source = SettingExplicit
	case setting.Source == "":
		setting.Source = SettingNativeDefault
	}
	return []EffectiveSetting{setting}
}

// ompConfigRoot is omp's profile-independent config root: $HOME joined with
// PI_CONFIG_DIR (default ".omp"), as omp joins it even when the value is absolute.
func ompConfigRoot(ctx Context) (string, error) {
	home := ctx.Env.Home()
	if home == "" {
		return "", errors.New("cannot resolve the omp config root: HOME is not set")
	}
	dirName := ctx.Env.Get("PI_CONFIG_DIR")
	if dirName == "" {
		dirName = ompConfigDirName
	}
	return filepath.Join(home, dirName), nil
}

func ompProfileRoot(root, profile string) string {
	if profile == "" {
		return root
	}
	return filepath.Join(root, "profiles", profile)
}

func (ompAdapter) Destination(ctx Context, scope Scope, settings Settings) (Destination, error) {
	switch scope {
	case ScopeProject:
		return ompProjectDestination(ctx, settings)
	case ScopeGlobal:
		return ompGlobalDestination(ctx, settings)
	default:
		return Destination{}, fmt.Errorf("unsupported scope %q", scope)
	}
}

// ompProjectDestination is the project directory every omp profile shares; the
// profile only names the context to verify in, never a per-profile copy.
func ompProjectDestination(ctx Context, settings Settings) (Destination, error) {
	if ctx.ProjectRoot == "" {
		return Destination{}, errors.New("project scope requires a project directory")
	}
	label := "project, shared by every omp profile"
	profile, err := resolveOMPProfile(ctx, settings)
	switch {
	case err != nil && profile.explicit:
		return Destination{}, err
	case err == nil:
		label += "; verify with " + profile.label()
	}
	return Destination{
		Dir:    filepath.Join(ctx.ProjectRoot, ".omp", "agents"),
		Label:  label,
		Shared: true,
	}, nil
}

// ompGlobalDestination is the user agents directory of exactly one profile. It
// never initializes a named profile: the profile directory must already exist.
func ompGlobalDestination(ctx Context, settings Settings) (Destination, error) {
	profile, err := resolveOMPProfile(ctx, settings)
	if err != nil {
		return Destination{}, err
	}
	root, err := ompConfigRoot(ctx)
	if err != nil {
		return Destination{}, err
	}
	profileRoot := ompProfileRoot(root, profile.name)
	agentDir := filepath.Join(profileRoot, "agent")
	if profile.name == "" {
		if err := ompCheckAgentDirOverride(ctx, root, agentDir, profile); err != nil {
			return Destination{}, err
		}
	} else if info, statErr := os.Stat(profileRoot); statErr != nil || !info.IsDir() {
		return Destination{}, fmt.Errorf(
			"omp profile %q does not exist at %s; agnthub does not initialize omp profiles "+
				"(run `omp --profile %s` once, or choose another --extra omp.profile)",
			profile.name, profileRoot, profile.name,
		)
	}
	return Destination{Dir: filepath.Join(agentDir, "agents"), Label: profile.label()}, nil
}

// ompCheckAgentDirOverride refuses the ambiguous default-profile case where
// PI_CODING_AGENT_DIR relocates omp's agent directory (documented) while omp 18
// still discovers user agents under the config root's agent directory (observed).
// An explicit profile resolves the ambiguity in favor of the discovered directory.
func ompCheckAgentDirOverride(ctx Context, root, agentDir string, profile ompProfile) error {
	if profile.explicit {
		return nil
	}
	override := ctx.Env.Get("PI_CODING_AGENT_DIR")
	if override == "" || filepath.Clean(override) == agentDir {
		return nil
	}
	// A value derived from a bypassed PI_PROFILE was propagated by a parent omp
	// process for that profile; omp ignores it in default mode.
	if legacy, err := normalizeOMPProfile(ctx.Env.Get("PI_PROFILE")); err == nil && legacy != "" &&
		filepath.Clean(override) == filepath.Join(ompProfileRoot(root, legacy), "agent") {
		return nil
	}
	return fmt.Errorf(
		"PI_CODING_AGENT_DIR=%s relocates omp's default agent directory, but omp discovers "+
			"user agents in %s; choose the profile explicitly with --extra omp.profile=<name> "+
			"(%q installs into %s)",
		override,
		filepath.Join(agentDir, "agents"),
		ompDefaultProfile,
		filepath.Join(agentDir, "agents"),
	)
}

func (a ompAdapter) Native(id string, variant *agent.VariantFile) (Artifact, error) {
	identity, err := a.Identify(variant.Path, variant.Raw)
	if err != nil {
		return Artifact{}, &InvalidVariantError{
			Harness: agent.OMP,
			Path:    variant.Path,
			Reason:  err.Error(),
		}
	}
	if identity.ID != id {
		return Artifact{}, &InvalidVariantError{
			Harness: agent.OMP, Path: variant.Path,
			Reason: fmt.Sprintf("name %q does not match agent %q", identity.ID, id),
		}
	}
	doc, _ := agent.ParseMarkdown(variant.Path, variant.Raw)
	var notes []string
	if _, ok := doc.Frontmatter["tools"]; ok {
		notes = append(notes,
			"tools selects omp built-in tools only; MCP, extension and custom tools remain "+
				"available to the agent, so it is not a sandbox")
	}
	return Artifact{FileName: id + ".md", Content: variant.Raw, Notes: notes}, nil
}

// ompTools maps omp built-in tool names (18.13, matched case-insensitively) to
// capabilities. omp's read also fetches URLs; there is no separate fetch tool.
var ompTools = []nativeTool{
	{name: "read", cap: CapRead | CapWebFetch, core: true},
	{name: "lsp", cap: CapRead},
	{name: "grep", cap: CapSearch, core: true},
	{name: "glob", cap: CapSearch, core: true},
	{name: "find", cap: CapSearch, core: true},
	{name: "search", cap: CapSearch},
	{name: "ast_grep", cap: CapSearch},
	{name: "edit", cap: CapEdit, core: true},
	{name: "write", cap: CapEdit, core: true},
	{name: "ast_edit", cap: CapEdit},
	{name: "bash", cap: CapShell, core: true},
	{name: "exec", cap: CapShell},
	{name: "eval", cap: CapShell},
	{name: "web_search", cap: CapWebSearch, core: true},
	{name: "task", cap: CapDelegate, core: true},
	{name: "todo", cap: CapTodo, core: true},
}

// ompHelpers are omp session tools whose omission elsewhere is harmless.
var ompHelpers = map[string]bool{
	"ask": true, "wait": true, "yield": true, "goal": true, "think": true,
	"checkpoint": true, "rewind": true, "context_notes": true, "new_context": true,
}

// ompOtherBuiltins are real omp tools with no cross-harness equivalent.
var ompOtherBuiltins = map[string]bool{
	"debug": true, "ida": true, "github": true, "security_scan": true,
	"memory_edit": true, "retain": true, "recall": true, "reflect": true,
	"learn": true, "manage_skill": true,
}

// ompBehaviors are omp agent fields with no cross-harness equivalent.
var ompBehaviors = map[string]string{
	"autoload-skills": "skills loaded into the agent's context",
	"output":          "a structured output schema",
	"structured":      "structured output",
	"blocking":        "omp blocking behavior",
	"read-summarize":  "omp read summarization",
	"prewalk":         "an omp prewalk pass",
	"advisor":         "an omp advisor",
}

func (a ompAdapter) Semantics(id string, variant *agent.VariantFile) (*Semantics, error) {
	if _, err := a.Native(id, variant); err != nil {
		return nil, err
	}
	doc, _ := agent.ParseMarkdown(variant.Path, variant.Raw)
	sem := &Semantics{
		ID:           id,
		Description:  doc.String("description"),
		Instructions: doc.Body,
		Source:       agent.OMP,
	}
	spawnsAll := false
	for _, key := range sortedKeys(doc.Frontmatter) {
		value := doc.Frontmatter[key]
		switch key {
		case "name", "description":
		case "model":
			if names, ok := nameList(value); ok {
				sem.Model = strings.Join(names, ", ")
			}
		case "thinking-level":
			sem.note("omitted thinking-level: model settings are model-specific")
		case "tools":
			names, _ := nameList(value)
			ompToolPolicy(sem, names)
		case "spawns":
			names, _ := nameList(value)
			if !slices.Equal(names, []string{"*"}) {
				sem.unsupported(key, "limits which agents it can delegate to")
			}
			spawnsAll = true
		default:
			if harmlessPortableKeys[key] {
				sem.note("omitted %s: descriptive metadata", key)
				continue
			}
			meaning := ompBehaviors[key]
			if meaning == "" {
				meaning = "an omp field with no verified equivalent"
			}
			sem.unsupported(key, meaning)
		}
	}
	if spawnsAll && sem.Tools.Restricted {
		sem.Tools.Allowed |= CapDelegate
	}
	return sem, nil
}

// ompToolPolicy reads an omp tools list. omp ignores names that are not
// built-in tools, so they grant nothing.
func ompToolPolicy(sem *Semantics, names []string) {
	sem.Tools.Restricted = true
	for _, name := range names {
		lower := strings.ToLower(name)
		if c, ok := lookupTool(ompTools, lower); ok {
			sem.Tools.Allowed |= c
			continue
		}
		switch {
		case ompHelpers[lower]:
			sem.note("omitted tools entry %s: an omp session helper", name)
		case ompOtherBuiltins[lower]:
			sem.Tools.Unknown = append(sem.Tools.Unknown, name)
		default:
			sem.note("omitted tools entry %s: omp ignores it, so it grants nothing", name)
		}
	}
}

// Convert writes an omp task agent. omp's tools list selects built-ins only:
// MCP, extension and custom tools stay available, so allowlisted MCP tools
// need no entry, and read also fetches URLs.
func (a ompAdapter) Convert(sem *Semantics) (Artifact, error) {
	if ompReservedNames[strings.ToLower(strings.TrimSpace(sem.ID))] {
		return Artifact{}, &IncompatibleError{
			Harness: agent.OMP,
			Reason:  fmt.Sprintf("agent name %q is reserved by omp", sem.ID),
		}
	}
	check, err := begin(agent.OMP, sem)
	if err == nil {
		err = check.requireMaxTurns()
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
		effective := sem.Tools.Effective()
		switch {
		case effective&CapRead != 0 && effective&CapWebFetch == 0:
			check.note("excludes web fetch, but omp's read tool also fetches URLs")
		case effective&CapRead == 0 && effective&CapWebFetch != 0:
			check.note("uses web fetch, which omp's read tool provides together with file reads")
		}
		if sem.Tools.Restricted {
			check.note("omp keeps MCP, extension and custom tools available to agents " +
				"with a tools list")
			check.mcpSession()
		}
		tools := toolNames(ompTools, effective)
		if tools == nil {
			tools = []string{}
		}
		fields = append(fields, agent.Field{Key: "tools", Value: tools})
	}
	content, err := agent.RenderMarkdown(fields, sem.Instructions)
	if err != nil {
		return Artifact{}, err
	}
	return selfCheck(a, sem.ID, Artifact{
		FileName:  sem.ID + ".md",
		Content:   content,
		Generated: true,
		Converter: converterID(agent.OMP),
		Notes:     check.notes,
	})
}

func (ompAdapter) Detect(ctx Context) bool {
	root, err := ompConfigRoot(ctx)
	if err != nil {
		return detect(ctx, "omp")
	}
	return detect(ctx, "omp", root)
}
