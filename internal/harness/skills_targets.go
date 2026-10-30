package harness

import (
	"errors"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Every adapter below declares the skills.sh target it installs through and the
// Skill locations its Harness discovers, highest precedence first. skills.sh 1.7.4
// installs a single-target Skill by copying it: "claude-code" into .claude/skills
// (CLAUDE_CONFIG_DIR/skills globally); the other targets into the shared
// <project>/.agents/skills or ~/.agents/skills. See docs/research/skill-dependencies.md.

// cursorSkillBases are Cursor's Skill roots under one base directory, in its
// within-base precedence order.
var cursorSkillBases = []string{".cursor", ".claude", ".codex", ".grok", ".agents"}

// SkillPlacement implements SkillHarness.
func (cursorAdapter) SkillPlacement(ctx Context, scope Scope, _ Settings) (SkillPlacement, error) {
	dir, err := scopedDir(ctx, scope, ".agents/skills", ".agents/skills")
	return SkillPlacement{Target: "cursor", Dir: dir}, err
}

// SkillVisibility implements SkillHarness. Cursor reads the workspace (cwd, no
// walk-up) and HOME roots; it sends project and user copies of one name to the
// model and resolves "/name" to the user copy, so every copy must be compatible.
func (cursorAdapter) SkillVisibility(
	ctx Context,
	scope Scope,
	_ Settings,
) (SkillVisibility, error) {
	var bases []string
	if scope == ScopeProject {
		bases = append(bases, ctx.ProjectRoot)
	}
	bases = append(bases, ctx.Env.Home())
	visibility := SkillVisibility{EveryCopyLoads: true}
	for i, base := range bases {
		if base == "" {
			continue
		}
		locScope, prefix := ScopeGlobal, "~/"
		if scope == ScopeProject && i == 0 {
			locScope, prefix = ScopeProject, "project "
		}
		for _, dir := range cursorSkillBases {
			visibility.Locations = append(visibility.Locations, SkillLocation{
				Dir:   filepath.Join(base, dir, "skills"),
				Scope: locScope,
				Label: prefix + dir + "/skills",
			})
		}
	}
	return visibility, nil
}

// SkillPlacement implements SkillHarness. skills.sh's gemini-cli target installs
// into $HOME/.agents/skills globally, which Gemini only reads when GEMINI_CLI_HOME
// is unset or equals HOME; verification reports the mismatch otherwise.
func (geminiAdapter) SkillPlacement(ctx Context, scope Scope, _ Settings) (SkillPlacement, error) {
	dir, err := scopedDir(ctx, scope, ".agents/skills", ".agents/skills")
	return SkillPlacement{Target: "gemini-cli", Dir: dir}, err
}

// SkillVisibility implements SkillHarness: workspace .agents then .gemini (cwd
// only, trusted folders only), then the user tier under GEMINI_CLI_HOME (or HOME),
// then extensions. Later tiers lose to earlier ones on a name conflict.
func (geminiAdapter) SkillVisibility(
	ctx Context,
	scope Scope,
	_ Settings,
) (SkillVisibility, error) {
	gemHome := ctx.Env.Get("GEMINI_CLI_HOME")
	if !filepath.IsAbs(gemHome) {
		gemHome = ctx.Env.Home()
	}
	var visibility SkillVisibility
	settings := []map[string]any{
		readJSONMap(geminiSystemSettings(ctx)),
		readJSONMap(filepath.Join(geminiConfigDir(ctx), "settings.json")),
	}
	if scope == ScopeProject {
		trusted, why := geminiTrusted(ctx, ctx.ProjectRoot, settings)
		if trusted {
			settings = append(settings,
				readJSONMap(filepath.Join(ctx.ProjectRoot, ".gemini", "settings.json")))
			for _, dir := range []string{".agents", ".gemini"} {
				visibility.Locations = append(visibility.Locations, SkillLocation{
					Dir:   filepath.Join(ctx.ProjectRoot, dir, "skills"),
					Scope: ScopeProject,
					Label: "project " + dir + "/skills",
				})
			}
		} else {
			visibility.Note = why
		}
	}
	for _, dir := range []string{".agents", ".gemini"} {
		visibility.Locations = append(visibility.Locations, SkillLocation{
			Dir:   filepath.Join(gemHome, dir, "skills"),
			Scope: ScopeGlobal,
			Label: "user " + filepath.Join(gemHome, dir, "skills"),
		})
	}
	extensions := filepath.Join(geminiConfigDir(ctx), "extensions")
	if entries, err := os.ReadDir(extensions); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				visibility.Locations = append(visibility.Locations, SkillLocation{
					Dir:   filepath.Join(extensions, entry.Name(), "skills"),
					Scope: ScopeGlobal,
					Label: "Gemini extension " + entry.Name(),
				})
			}
		}
	}
	visibility.Hidden = geminiHidden(settings)
	return visibility, nil
}

func geminiSystemSettings(ctx Context) string {
	if path := ctx.Env.Get("GEMINI_CLI_SYSTEM_SETTINGS_PATH"); filepath.IsAbs(path) {
		return path
	}
	return "/etc/gemini-cli/settings.json"
}

// geminiHidden applies skills.enabled and the union of skills.disabled lists.
func geminiHidden(layers []map[string]any) func(string) string {
	enabled := true
	var disabled []string
	for _, layer := range layers {
		if layer == nil {
			continue
		}
		if value, ok := lookupKey(layer, "skills.enabled"); ok && value == false {
			enabled = false
		}
		if value, ok := lookupKey(layer, "skills.disabled"); ok {
			items, _ := value.([]any)
			for _, item := range items {
				if name, ok := item.(string); ok {
					disabled = append(disabled, strings.ToLower(name))
				}
			}
		}
	}
	return func(name string) string {
		switch {
		case !enabled:
			return "Gemini CLI settings disable skills (skills.enabled: false)"
		case slices.Contains(disabled, strings.ToLower(name)):
			return "Gemini CLI settings disable it (skills.disabled)"
		default:
			return ""
		}
	}
}

// geminiTrusted applies Gemini CLI folder trust: workspace Skills load only in a
// trusted folder. It returns why the folder is untrusted otherwise.
func geminiTrusted(ctx Context, dir string, settings []map[string]any) (bool, string) {
	switch {
	case ctx.Env.Get("GEMINI_CLI_TRUST_WORKSPACE") == "true":
		return true, ""
	case ctx.Env.Get("GEMINI_CLI_TRUST_WORKSPACE") == "false",
		ctx.Env.Get("GEMINI_RESTRICTED_MODE") == "true":
		return false, "Gemini CLI is forced into an untrusted workspace, so it ignores project skills"
	}
	for _, layer := range settings {
		if value, ok := lookupKey(layer, "security.folderTrust.enabled"); ok && value == false {
			return true, ""
		}
	}
	rulesPath := ctx.Env.Get("GEMINI_CLI_TRUSTED_FOLDERS_PATH")
	if !filepath.IsAbs(rulesPath) {
		rulesPath = filepath.Join(geminiConfigDir(ctx), "trustedFolders.json")
	}
	best, trusted := -1, false
	for rule, value := range readJSONMap(rulesPath) {
		level, _ := value.(string)
		scopeDir := filepath.Clean(rule)
		if level == "TRUST_PARENT" {
			scopeDir = filepath.Dir(scopeDir)
		}
		if !within(dir, scopeDir) || len(scopeDir) <= best {
			continue
		}
		best, trusted = len(scopeDir), level == "TRUST_FOLDER" || level == "TRUST_PARENT"
	}
	if trusted {
		return true, ""
	}
	return false, "Gemini CLI ignores project skills until " + dir + " is a trusted folder " +
		"(trust it in Gemini CLI, or set GEMINI_CLI_TRUST_WORKSPACE=true)"
}

func within(dir, root string) bool {
	rel, err := filepath.Rel(root, filepath.Clean(dir))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// walkUp lists cwd and its ancestors, nearest first: up to the git root when cwd
// is inside a repository, otherwise up to HOME (excluded) or "/" outside HOME.
func walkUp(cwd, home string) []string {
	if cwd == "" {
		return nil
	}
	cwd, home = filepath.Clean(cwd), filepath.Clean(home)
	if root := gitRoot(cwd); root != "" {
		return ancestorsTo(cwd, root)
	}
	var dirs []string
	for dir := cwd; dir != home; dir = filepath.Dir(dir) {
		dirs = append(dirs, dir)
		if filepath.Dir(dir) == dir {
			break
		}
	}
	return dirs
}

// SkillPlacement implements SkillHarness.
func (opencodeAdapter) SkillPlacement(
	ctx Context,
	scope Scope,
	_ Settings,
) (SkillPlacement, error) {
	dir, err := scopedDir(ctx, scope, ".agents/skills", ".agents/skills")
	return SkillPlacement{Target: "opencode", Dir: dir}, err
}

// SkillVisibility implements SkillHarness, following OpenCode 2: project
// .opencode/skill(s) from cwd up to "/", the config directory, .agents (project
// then ~/.agents) and .claude (project then ~/.claude). OpenCode 1 resolves
// duplicates nondeterministically, so every copy must be compatible.
func (opencodeAdapter) SkillVisibility(
	ctx Context,
	scope Scope,
	_ Settings,
) (SkillVisibility, error) {
	home := ctx.Env.Home()
	configDir := ctx.Env.Get("OPENCODE_CONFIG_DIR")
	if !filepath.IsAbs(configDir) {
		configDir = opencodeConfigDir(ctx)
	}
	visibility := SkillVisibility{EveryCopyLoads: true}
	var ancestors []string
	if scope == ScopeProject {
		if ctx.Env.Get("OPENCODE_DISABLE_PROJECT_CONFIG") == "" {
			for dir := filepath.Clean(ctx.ProjectRoot); ; dir = filepath.Dir(dir) {
				ancestors = append(ancestors, dir)
				if filepath.Dir(dir) == dir {
					break
				}
			}
		} else {
			visibility.Note = "OPENCODE_DISABLE_PROJECT_CONFIG disables project skills"
		}
	}
	add := func(dir string, locScope Scope, label string) {
		if locScope == ScopeProject && (dir == filepath.Join(home, ".agents", "skills") ||
			dir == filepath.Join(home, ".claude", "skills")) {
			return
		}
		visibility.Locations = append(visibility.Locations,
			SkillLocation{Dir: dir, Scope: locScope, Label: label})
	}
	for _, dir := range ancestors {
		for _, name := range []string{"skills", "skill"} {
			add(filepath.Join(dir, ".opencode", name), ScopeProject,
				"project "+rel(ctx.ProjectRoot, dir, ".opencode/"+name))
		}
	}
	for _, name := range []string{"skills", "skill"} {
		add(filepath.Join(configDir, name), ScopeGlobal, "OpenCode config "+name)
	}
	for _, dir := range ancestors {
		add(filepath.Join(dir, ".agents", "skills"), ScopeProject,
			"project "+rel(ctx.ProjectRoot, dir, ".agents/skills"))
	}
	add(filepath.Join(home, ".agents", "skills"), ScopeGlobal, "~/.agents/skills")
	if ctx.Env.Get("OPENCODE_DISABLE_CLAUDE_CODE") == "" &&
		ctx.Env.Get("OPENCODE_DISABLE_CLAUDE_CODE_SKILLS") == "" {
		for _, dir := range ancestors {
			add(filepath.Join(dir, ".claude", "skills"), ScopeProject,
				"project "+rel(ctx.ProjectRoot, dir, ".claude/skills")+" (read by OpenCode)")
		}
		add(
			filepath.Join(home, ".claude", "skills"),
			ScopeGlobal,
			"~/.claude/skills (read by OpenCode)",
		)
	}
	configs := []map[string]any{readJSONMap(filepath.Join(configDir, "opencode.json"))}
	if scope == ScopeProject {
		configs = append(configs, readJSONMap(filepath.Join(ctx.ProjectRoot, "opencode.json")))
	}
	visibility.Hidden = opencodeHidden(configs)
	return visibility, nil
}

// opencodeHidden applies skill denials from opencode.json: policies denying
// "integration.use" on "skill:<glob>", permissions rules for action "skill" (last
// match wins) and the v1 permission.skill map.
func opencodeHidden(configs []map[string]any) func(string) string {
	return func(name string) string {
		denied := false
		for _, config := range configs {
			if config == nil {
				continue
			}
			for _, rule := range anyList(config["policies"]) {
				if rule["action"] == "integration.use" && rule["effect"] == "deny" &&
					globMatch(strings.TrimPrefix(text(rule["resource"]), "skill:"), name) &&
					strings.HasPrefix(text(rule["resource"]), "skill:") {
					return "an OpenCode policy denies it"
				}
			}
			for _, rule := range anyList(config["permissions"]) {
				if rule["action"] == "skill" && globMatch(text(rule["resource"]), name) {
					denied = rule["effect"] == "deny"
				}
			}
			if permission, ok := config["permission"].(map[string]any); ok {
				switch skill := permission["skill"].(type) {
				case string:
					denied = skill == "deny"
				case map[string]any:
					for pattern, action := range skill {
						if globMatch(pattern, name) {
							denied = action == "deny"
						}
					}
				}
			}
		}
		if denied {
			return "OpenCode permissions deny it"
		}
		return ""
	}
}

func anyList(value any) []map[string]any {
	items, _ := value.([]any)
	rules := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if rule, ok := item.(map[string]any); ok {
			rules = append(rules, rule)
		}
	}
	return rules
}

func text(value any) string {
	s, _ := value.(string)
	return s
}

func globMatch(pattern, name string) bool {
	ok, err := path.Match(pattern, name)
	return err == nil && ok
}

// SkillPlacement implements SkillHarness. skills.sh's github-copilot target
// installs into ~/.agents/skills globally, which Copilot reads only while
// COPILOT_HOME is the default ~/.copilot.
func (copilotAdapter) SkillPlacement(ctx Context, scope Scope, _ Settings) (SkillPlacement, error) {
	dir, err := scopedDir(ctx, scope, ".agents/skills", ".agents/skills")
	return SkillPlacement{Target: "github-copilot", Dir: dir}, err
}

// SkillVisibility implements SkillHarness, following Copilot CLI: project
// .github, then .agents, then .claude skills (each from cwd up to the git root),
// then $COPILOT_HOME/skills, ~/.agents/skills, COPILOT_SKILLS_DIRS and the
// skillDirectories setting. The first copy of a name wins.
func (copilotAdapter) SkillVisibility(
	ctx Context,
	scope Scope,
	_ Settings,
) (SkillVisibility, error) {
	home := ctx.Env.Home()
	copilotDir := copilotHome(ctx)
	settings := readJSONMap(filepath.Join(copilotDir, "settings.json"))
	var ignored []string
	for _, item := range stringList(settings["ignoredSkillsLocations"]) {
		if strings.HasPrefix(item, "~/") {
			item = filepath.Join(home, item[2:])
		}
		if filepath.IsAbs(item) {
			ignored = append(ignored, filepath.Clean(item))
		}
	}
	var visibility SkillVisibility
	add := func(dir string, locScope Scope, label string) {
		for _, root := range ignored {
			if within(dir, root) {
				return
			}
		}
		visibility.Locations = append(visibility.Locations,
			SkillLocation{Dir: dir, Scope: locScope, Label: label})
	}
	disabled := stringList(settings["disabledSkills"])
	if scope == ScopeProject {
		walk := walkUp(ctx.ProjectRoot, home)
		for _, name := range []string{".github", ".agents", ".claude"} {
			for _, dir := range walk {
				add(filepath.Join(dir, name, "skills"), ScopeProject,
					"project "+rel(ctx.ProjectRoot, dir, name+"/skills"))
			}
		}
		for _, file := range []string{"settings.json", "settings.local.json"} {
			repo := readJSONMap(filepath.Join(ctx.ProjectRoot, ".github", "copilot", file))
			disabled = append(disabled, stringList(repo["disabledSkills"])...)
		}
	}
	add(filepath.Join(copilotDir, "skills"), ScopeGlobal, filepath.Join(copilotDir, "skills"))
	if filepath.Clean(copilotDir) == filepath.Join(home, ".copilot") {
		add(filepath.Join(home, ".agents", "skills"), ScopeGlobal, "~/.agents/skills")
	} else {
		visibility.Note = "COPILOT_HOME=" + copilotDir +
			", and Copilot CLI only reads ~/.agents/skills with the default COPILOT_HOME"
	}
	for _, dir := range strings.Split(ctx.Env.Get("COPILOT_SKILLS_DIRS"), ",") {
		if dir = strings.TrimSpace(dir); filepath.IsAbs(dir) {
			add(dir, ScopeGlobal, "COPILOT_SKILLS_DIRS "+dir)
		}
	}
	for _, dir := range stringList(settings["skillDirectories"]) {
		if filepath.IsAbs(dir) {
			add(dir, ScopeGlobal, "Copilot skillDirectories "+dir)
		}
	}
	visibility.Hidden = func(name string) string {
		if slices.Contains(disabled, name) {
			return "Copilot CLI settings disable it (disabledSkills)"
		}
		return ""
	}
	return visibility, nil
}

func stringList(value any) []string {
	items, _ := value.([]any)
	values := make([]string, 0, len(items))
	for _, item := range items {
		if s, ok := item.(string); ok {
			values = append(values, s)
		}
	}
	return values
}

// claudeManagedSkills is Claude Code's managed (policy) Skill directory on Linux.
const claudeManagedSkills = "/etc/claude-code/.claude/skills"

// SkillPlacement implements SkillHarness: skills.sh's claude-code target uses
// .claude/skills and, globally, ${CLAUDE_CONFIG_DIR:-~/.claude}/skills.
func (claudeAdapter) SkillPlacement(ctx Context, scope Scope, _ Settings) (SkillPlacement, error) {
	if scope == ScopeGlobal {
		if ctx.Env.Home() == "" && ctx.Env.Get("CLAUDE_CONFIG_DIR") == "" {
			return SkillPlacement{}, errors.New("global scope requires HOME")
		}
		return SkillPlacement{
			Target: "claude-code", Dir: filepath.Join(claudeConfigDir(ctx), "skills"),
		}, nil
	}
	dir, err := scopedDir(ctx, scope, ".claude/skills", ".claude/skills")
	return SkillPlacement{Target: "claude-code", Dir: dir}, err
}

// SkillVisibility implements SkillHarness, following Claude Code: managed, then
// user (${CLAUDE_CONFIG_DIR:-~/.claude}/skills), then project .claude/skills from
// cwd up to the git root (nearest first). Claude identifies a Skill by its
// directory name and never reads .agents/skills.
func (claudeAdapter) SkillVisibility(
	ctx Context,
	scope Scope,
	_ Settings,
) (SkillVisibility, error) {
	var visibility SkillVisibility
	seen := map[string]bool{}
	add := func(dir string, locScope Scope, label string) {
		if !seen[dir] {
			seen[dir] = true
			visibility.Locations = append(visibility.Locations,
				SkillLocation{Dir: dir, Scope: locScope, Label: label})
		}
	}
	if ctx.Env.Get("CLAUDE_CODE_DISABLE_POLICY_SKILLS") == "" {
		add(claudeManagedSkills, ScopeGlobal, "Claude managed skills")
	}
	userDir := claudeConfigDir(ctx)
	add(filepath.Join(userDir, "skills"), ScopeGlobal, "user "+filepath.Join(userDir, "skills"))
	settings := []map[string]any{
		readJSONMap("/etc/claude-code/managed-settings.json"),
		readJSONMap(filepath.Join(userDir, "settings.json")),
	}
	if scope == ScopeProject {
		walk := []string{}
		if root := gitRoot(ctx.ProjectRoot); root != "" {
			walk = ancestorsTo(ctx.ProjectRoot, root)
		} else {
			for dir := filepath.Clean(ctx.ProjectRoot); ; dir = filepath.Dir(dir) {
				walk = append(walk, dir)
				if filepath.Dir(dir) == dir {
					break
				}
			}
		}
		for _, dir := range walk {
			add(filepath.Join(dir, ".claude", "skills"), ScopeProject,
				"project "+rel(ctx.ProjectRoot, dir, ".claude/skills"))
		}
		settings = append(settings,
			readJSONMap(filepath.Join(ctx.ProjectRoot, ".claude", "settings.json")),
			readJSONMap(filepath.Join(ctx.ProjectRoot, ".claude", "settings.local.json")))
	}
	visibility.Hidden = func(name string) string {
		for _, layer := range settings {
			overrides, _ := layer["skillOverrides"].(map[string]any)
			if overrides[name] == "off" {
				return "Claude Code settings turn it off (skillOverrides)"
			}
		}
		return ""
	}
	return visibility, nil
}

// codexAdminSkills is Codex's admin Skill directory.
const codexAdminSkills = "/etc/codex/skills"

// SkillPlacement implements SkillHarness.
func (codexAdapter) SkillPlacement(ctx Context, scope Scope, _ Settings) (SkillPlacement, error) {
	dir, err := scopedDir(ctx, scope, ".agents/skills", ".agents/skills")
	return SkillPlacement{Target: "codex", Dir: dir}, err
}

// SkillVisibility implements SkillHarness, following Codex: repo .agents/skills
// from cwd up to the git root (cwd only outside a repository) and the project
// .codex/skills, then ~/.agents/skills, the deprecated $CODEX_HOME/skills and
// the admin directory. Codex loads every copy and cannot select a name that two
// copies share, so a second copy of any kind is a conflict.
func (codexAdapter) SkillVisibility(ctx Context, scope Scope, _ Settings) (SkillVisibility, error) {
	visibility := SkillVisibility{EveryCopyLoads: true, UniqueName: true}
	add := func(dir string, locScope Scope, label string) {
		visibility.Locations = append(visibility.Locations,
			SkillLocation{Dir: dir, Scope: locScope, Label: label})
	}
	codexDir := codexHome(ctx)
	if scope == ScopeProject {
		walk := []string{filepath.Clean(ctx.ProjectRoot)}
		if root := gitRoot(ctx.ProjectRoot); root != "" {
			walk = ancestorsTo(ctx.ProjectRoot, root)
		}
		for _, dir := range walk {
			add(filepath.Join(dir, ".agents", "skills"), ScopeProject,
				"project "+rel(ctx.ProjectRoot, dir, ".agents/skills"))
		}
		add(filepath.Join(ctx.ProjectRoot, ".codex", "skills"), ScopeProject,
			"project .codex/skills")
	}
	add(filepath.Join(ctx.Env.Home(), ".agents", "skills"), ScopeGlobal, "~/.agents/skills")
	add(filepath.Join(codexDir, "skills"), ScopeGlobal, filepath.Join(codexDir, "skills"))
	add(codexAdminSkills, ScopeGlobal, "Codex admin skills")
	disabledNames, disabledPaths := codexDisabledSkills(filepath.Join(codexDir, "config.toml"))
	visibility.DisabledDirs = disabledPaths
	visibility.Hidden = func(name string) string {
		if slices.Contains(disabledNames, name) {
			return "Codex config disables it ([[skills.config]] enabled = false)"
		}
		return ""
	}
	return visibility, nil
}

// codexDisabledSkills reads [[skills.config]] entries with enabled = false; later
// entries for the same selector override earlier ones.
func codexDisabledSkills(configPath string) (names, dirs []string) {
	raw := readSettingsFile(configPath)
	if raw == nil {
		return nil, nil
	}
	var config struct {
		Skills struct {
			Config []struct {
				Name    string `toml:"name"`
				Path    string `toml:"path"`
				Enabled *bool  `toml:"enabled"`
			} `toml:"config"`
		} `toml:"skills"`
	}
	if toml.Unmarshal(raw, &config) != nil {
		return nil, nil
	}
	nameState, pathState := map[string]bool{}, map[string]bool{}
	for _, entry := range config.Skills.Config {
		if entry.Enabled == nil {
			continue
		}
		switch {
		case entry.Name != "":
			nameState[entry.Name] = !*entry.Enabled
		case filepath.IsAbs(entry.Path):
			pathState[filepath.Dir(filepath.Clean(entry.Path))] = !*entry.Enabled
		}
	}
	for name, off := range nameState {
		if off {
			names = append(names, name)
		}
	}
	for dir, off := range pathState {
		if off {
			dirs = append(dirs, dir)
		}
	}
	return names, dirs
}
