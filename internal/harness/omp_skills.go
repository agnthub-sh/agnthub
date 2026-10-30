package harness

import (
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bytedance/sonic"
	"go.yaml.in/yaml/v3"
)

// ompSkillTarget is the skills.sh target agnthub uses for omp. skills.sh has no
// omp target; its harness-neutral "universal" target installs into the shared
// <project>/.agents/skills and ~/.agents/skills directories, which omp's "agents"
// provider discovers in every profile.
const ompSkillTarget = "universal"

// SkillPlacement implements SkillHarness.
func (ompAdapter) SkillPlacement(ctx Context, scope Scope, _ Settings) (SkillPlacement, error) {
	dir, err := scopedDir(ctx, scope, ".agents/skills", ".agents/skills")
	return SkillPlacement{Target: ompSkillTarget, Dir: dir}, err
}

// ompSkillSettings are the omp settings that change Skill discovery, merged from
// the selected profile's config.yml and the project's settings layer.
type ompSkillSettings struct {
	layers []map[string]any
}

// loadOMPSkillSettings reads the project layer (cwd only, as omp does) ahead of
// the profile's global config.yml.
func loadOMPSkillSettings(projectRoot, agentDir string) ompSkillSettings {
	var settings ompSkillSettings
	if projectRoot != "" {
		settings.add(readYAMLMap(filepath.Join(projectRoot, ".omp", "config.yml")))
		settings.add(readJSONMap(filepath.Join(projectRoot, ".omp", "settings.json")))
		settings.add(readJSONMap(filepath.Join(projectRoot, ".claude", "settings.json")))
	}
	settings.add(readYAMLMap(filepath.Join(agentDir, "config.yml")))
	return settings
}

func (s *ompSkillSettings) add(layer map[string]any) {
	if layer != nil {
		s.layers = append(s.layers, layer)
	}
}

// value returns the first layer's value for a dotted key, nested or flat.
func (s ompSkillSettings) value(key string) (any, bool) {
	for _, layer := range s.layers {
		if value, ok := lookupKey(layer, key); ok {
			return value, true
		}
	}
	return nil, false
}

func (s ompSkillSettings) flag(key string, fallback bool) bool {
	if value, ok := s.value(key); ok {
		if b, ok := value.(bool); ok {
			return b
		}
	}
	return fallback
}

// list unions a string-list setting across layers.
func (s ompSkillSettings) list(key string) []string {
	var values []string
	for _, layer := range s.layers {
		raw, ok := lookupKey(layer, key)
		if !ok {
			continue
		}
		items, _ := raw.([]any)
		for _, item := range items {
			if text, ok := item.(string); ok && !slices.Contains(values, text) {
				values = append(values, text)
			}
		}
	}
	return values
}

func lookupKey(layer map[string]any, key string) (any, bool) {
	if value, ok := layer[key]; ok {
		return value, true
	}
	head, rest, nested := strings.Cut(key, ".")
	if !nested {
		return nil, false
	}
	child, ok := layer[head].(map[string]any)
	if !ok {
		return nil, false
	}
	return lookupKey(child, rest)
}

const maxSettingsBytes = 1 << 20

func readSettingsFile(name string) []byte {
	info, err := os.Stat(name)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxSettingsBytes {
		return nil
	}
	raw, err := os.ReadFile(name)
	if err != nil {
		return nil
	}
	return raw
}

func readYAMLMap(name string) map[string]any {
	raw := readSettingsFile(name)
	if raw == nil {
		return nil
	}
	var layer map[string]any
	if yaml.Unmarshal(raw, &layer) != nil {
		return nil
	}
	return layer
}

func readJSONMap(name string) map[string]any {
	raw := readSettingsFile(name)
	if raw == nil {
		return nil
	}
	var layer map[string]any
	if sonic.Unmarshal(raw, &layer) != nil {
		return nil
	}
	return layer
}

// ompAgentDir is the selected profile's agent directory. PI_CODING_AGENT_DIR
// relocates it only for the default profile, as omp's own directory resolution does.
func ompAgentDir(ctx Context, profile ompProfile) (string, error) {
	root, err := ompConfigRoot(ctx)
	if err != nil {
		return "", err
	}
	if profile.name == "" {
		if override := ctx.Env.Get("PI_CODING_AGENT_DIR"); filepath.IsAbs(override) {
			return filepath.Clean(override), nil
		}
	}
	return filepath.Join(ompProfileRoot(root, profile.name), "agent"), nil
}

// SkillVisibility implements SkillHarness, following omp 18's skill providers in
// priority order: custom directories, native .omp, Claude, shared .agents/.agent,
// Codex, OpenCode and GitHub. Within a provider project copies precede user copies
// (nearest ancestor first), except Claude, which lists its user copy first. Plugin,
// skillshare and managed skills are not modeled.
func (ompAdapter) SkillVisibility(
	ctx Context,
	scope Scope,
	settings Settings,
) (SkillVisibility, error) {
	profile, err := resolveOMPProfile(ctx, settings)
	if err != nil {
		return SkillVisibility{}, err
	}
	agentDir, err := ompAgentDir(ctx, profile)
	if err != nil {
		return SkillVisibility{}, err
	}
	home := ctx.Env.Home()
	projectRoot := ""
	if scope == ScopeProject {
		projectRoot = ctx.ProjectRoot
	}
	config := loadOMPSkillSettings(projectRoot, agentDir)
	disabled := config.list("disabledProviders")
	enabledProviders := config.list("enabledProviders")
	optedIn := func(provider string) bool {
		return slices.Contains(enabledProviders, provider) ||
			slices.Contains(enabledProviders, "*") || slices.Contains(enabledProviders, "all")
	}
	var locations []SkillLocation
	add := func(provider, dir string, locScope Scope, label string) {
		if dir == "" || slices.Contains(disabled, provider) {
			return
		}
		locations = append(locations, SkillLocation{Dir: dir, Scope: locScope, Label: label})
	}
	for _, dir := range config.list("skills.customDirectories") {
		if strings.HasPrefix(dir, "~/") {
			dir = filepath.Join(home, dir[2:])
		}
		if filepath.IsAbs(dir) {
			add("custom", dir, ScopeGlobal, "omp skills.customDirectories "+dir)
		}
	}
	walkGit := ompWalk(projectRoot, home, false)
	if config.flag("skills.enablePiProject", true) {
		for _, dir := range ompWalk(projectRoot, home, true) {
			add("native", filepath.Join(dir, ".omp", "skills"), ScopeProject,
				"omp project "+rel(projectRoot, dir, ".omp/skills"))
		}
	}
	if config.flag("skills.enablePiUser", true) {
		add("native", filepath.Join(agentDir, "skills"), ScopeGlobal,
			"omp "+profile.label()+" skills")
	}
	claudeUser := config.flag("skills.enableClaudeUser", false) || optedIn("claude") ||
		ctx.Env.Get("CLAUDE_CONFIG_DIR") != ""
	if claudeUser {
		add("claude", filepath.Join(claudeConfigDir(ctx), "skills"), ScopeGlobal,
			"Claude user skills (read by omp)")
	}
	if config.flag("skills.enableClaudeProject", true) {
		for _, dir := range walkGit {
			add("claude", filepath.Join(dir, ".claude", "skills"), ScopeProject,
				"project "+rel(projectRoot, dir, ".claude/skills")+" (read by omp)")
		}
	}
	if config.flag("skills.enableAgentsProject", true) {
		for _, dir := range walkGit {
			for _, name := range []string{".agent", ".agents"} {
				add("agents", filepath.Join(dir, name, "skills"), ScopeProject,
					"project "+rel(projectRoot, dir, name+"/skills"))
			}
		}
	}
	if config.flag("skills.enableAgentsUser", true) {
		for _, base := range ompUserBases(ctx, home) {
			for _, name := range []string{".agent", ".agents"} {
				add("agents", filepath.Join(base, name, "skills"), ScopeGlobal,
					"user "+filepath.Join(base, name, "skills"))
			}
		}
	}
	if projectRoot != "" {
		add("codex", filepath.Join(projectRoot, ".codex", "skills"), ScopeProject,
			"project .codex/skills (read by omp)")
	}
	if config.flag("skills.enableCodexUser", false) || optedIn("codex") {
		add("codex", filepath.Join(home, ".codex", "skills"), ScopeGlobal,
			"~/.codex/skills (read by omp)")
	}
	if projectRoot != "" {
		add("opencode", filepath.Join(projectRoot, ".opencode", "skills"), ScopeProject,
			"project .opencode/skills (read by omp)")
	}
	if optedIn("opencode") {
		add("opencode", filepath.Join(home, ".config", "opencode", "skills"), ScopeGlobal,
			"~/.config/opencode/skills (read by omp)")
	}
	if projectRoot != "" {
		add("github", filepath.Join(projectRoot, ".github", "skills"), ScopeProject,
			"project .github/skills (read by omp)")
	}
	return SkillVisibility{Locations: locations, Hidden: ompHidden(config, profile)}, nil
}

// ompHidden applies omp's skill filters: skills.enabled, disabledExtensions
// "skill:<name>", skills.ignoredSkills and the skills.includeSkills allow-list.
func ompHidden(config ompSkillSettings, profile ompProfile) func(string) string {
	enabled := config.flag("skills.enabled", true)
	disabled := config.list("disabledExtensions")
	ignored := config.list("skills.ignoredSkills")
	included := config.list("skills.includeSkills")
	context := "omp " + profile.label()
	return func(name string) string {
		switch {
		case !enabled:
			return context + " disables all skills (skills.enabled: false)"
		case slices.Contains(disabled, "skill:"+name):
			return context + " disables it (disabledExtensions: skill:" + name + ")"
		case matchesAny(ignored, name):
			return context + " ignores it (skills.ignoredSkills)"
		case len(included) > 0 && !matchesAny(included, name):
			return context + " does not include it (skills.includeSkills)"
		default:
			return ""
		}
	}
}

func matchesAny(patterns []string, name string) bool {
	for _, pattern := range patterns {
		if ok, err := path.Match(pattern, name); err == nil && ok {
			return true
		}
	}
	return false
}

// ompUserBases are the home directories omp's agents provider scans: HOME and,
// under WSL, the Windows profile directory.
func ompUserBases(ctx Context, home string) []string {
	bases := []string{home}
	_, distro := ctx.Env.Lookup("WSL_DISTRO_NAME")
	_, interop := ctx.Env.Lookup("WSL_INTEROP")
	if profile := ctx.Env.Get("USERPROFILE"); (distro || interop) && filepath.IsAbs(profile) &&
		filepath.Clean(profile) != filepath.Clean(home) {
		bases = append(bases, profile)
	}
	return bases
}

// ompWalk lists project directories from cwd upward, nearest first: to the git
// root when cwd is in a repository. Without one, native discovery stops below
// HOME and temp directories; the others continue to HOME (excluded), or to "/"
// when cwd is outside HOME.
func ompWalk(cwd, home string, native bool) []string {
	if cwd == "" {
		return nil
	}
	cwd = filepath.Clean(cwd)
	home = filepath.Clean(home)
	if root := gitRoot(cwd); root != "" {
		return ancestorsTo(cwd, root)
	}
	if native && cwd == home {
		return nil
	}
	var dirs []string
	for dir := cwd; ; dir = filepath.Dir(dir) {
		if dir == home || native && (dir == filepath.Clean(os.TempDir()) || dir == "/tmp" ||
			dir == "/var/tmp") {
			break
		}
		dirs = append(dirs, dir)
		if parent := filepath.Dir(dir); parent == dir {
			break
		}
	}
	return dirs
}
