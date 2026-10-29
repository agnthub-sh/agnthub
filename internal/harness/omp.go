package harness

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/heynemann/agnthub/internal/agent"
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

func (ompAdapter) Convert(id string, portable *agent.Document) (Artifact, error) {
	core, err := ExtractPortable(agent.OMP, id, portable)
	if err != nil {
		return Artifact{}, err
	}
	if ompReservedNames[strings.ToLower(strings.TrimSpace(core.ID))] {
		return Artifact{}, &IncompatibleError{
			Harness: agent.OMP,
			Reason:  fmt.Sprintf("agent name %q is reserved by omp", core.ID),
		}
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
		Converter: "omp/v1",
		Notes:     core.OmissionNotes(),
	}, nil
}

func (ompAdapter) Detect(ctx Context) bool {
	root, err := ompConfigRoot(ctx)
	if err != nil {
		return detect(ctx, "omp")
	}
	return detect(ctx, "omp", root)
}
