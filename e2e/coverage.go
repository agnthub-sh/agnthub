package e2e

import "github.com/agnthub-sh/agnthub/agent"

// This file is the coverage contract of the suite: every step the containerized
// run executes (stepSpecs) and every agent type or behavior it proves for each
// Harness (scenarios). TestCoverage (coverage_test.go, part of go test ./...)
// fails when a (scenario, Harness) cell has neither a golden showing the claimed
// result nor an explicitly justified exclusion, and when COVERAGE.md no longer
// matches what the goldens prove.

// Scope and source of a step.
const (
	scopeNone   = ""
	scopeProj   = "project"
	scopeUser   = "global"
	sourceLocal = "local"
	sourceGit   = "git"
)

// Steps several scenarios refer to.
const (
	stepPlan      = "plan-layouts"
	stepPlanMan   = "plan-manifest"
	stepRefuseLy  = "refuse-layouts"
	stepPlanMkt   = "plan-marketplace"
	stepRefuseMkt = "refuse-marketplace"
)

// harnessIDs lists every supported Harness; adding one to agent.AllHarnesses
// makes TestCoverage demand its image, goldens or exclusions.
func harnessIDs() []string {
	ids := make([]string, 0, len(agent.AllHarnesses))
	for _, h := range agent.AllHarnesses {
		ids = append(ids, string(h))
	}
	return ids
}

// stepSpec describes one step of the containerized run.
type stepSpec struct {
	scope  string
	source string
	// native: after the step the real Harness CLI is asked to load what it
	// installed (see native_test.go).
	native bool
	// only limits the step to these Harnesses; empty means every Harness.
	only []string
	// except excludes these Harnesses.
	except []string
}

// appliesTo reports whether harness runs the step.
func (s stepSpec) appliesTo(harness string) bool {
	for _, h := range s.except {
		if h == harness {
			return false
		}
	}
	if len(s.only) == 0 {
		return true
	}
	for _, h := range s.only {
		if h == harness {
			return true
		}
	}
	return false
}

// layoutInstalls are the steps that install every installable layout agent, in
// each scope from each source kind.
var layoutInstalls = []string{
	"install-layouts-project-local",
	"install-layouts-project-git",
	"install-layouts-global-local",
	"install-layouts-global-git",
}

// manifestInstalls install the manifest's direct agents with their dependencies.
var manifestInstalls = []string{
	"install-manifest-project-local",
	"install-manifest-project-git",
	"install-manifest-global-git",
}

// marketplaceInstalls install every installable agent of the plugin marketplace.
var marketplaceInstalls = []string{
	"install-marketplace-project-local",
	"install-marketplace-global-git",
}

// stepSpecs lists every step TestHarness runs. A step it runs that is missing
// here fails the run; a step here without a golden fails TestCoverage.
var stepSpecs = map[string]stepSpec{
	"list-layouts":   {scope: scopeNone, source: sourceLocal},
	"list-ambiguous": {scope: scopeNone, source: sourceLocal},
	"list-manifest":  {scope: scopeNone, source: sourceGit},
	stepPlan:         {scope: scopeProj, source: sourceLocal},
	stepPlanMan:      {scope: scopeProj, source: sourceGit},

	"refuse-ambiguous-portable":    {scope: scopeProj, source: sourceLocal},
	"refuse-ambiguous-native":      {scope: scopeProj, source: sourceLocal},
	"install-ambiguous-unaffected": {scope: scopeProj, source: sourceLocal, native: true},

	"install-layouts-project-local": {scope: scopeProj, source: sourceLocal, native: true},
	"install-layouts-project-git":   {scope: scopeProj, source: sourceGit, native: true},
	"install-layouts-global-local":  {scope: scopeUser, source: sourceLocal, native: true},
	"install-layouts-global-git":    {scope: scopeUser, source: sourceGit, native: true},
	stepRefuseLy:                    {scope: scopeProj, source: sourceLocal},

	"refuse-manifest-project-untrusted": {
		scope: scopeProj, source: sourceGit, only: []string{"gemini"},
	},
	"install-manifest-project-local": {scope: scopeProj, source: sourceLocal, native: true},
	"install-manifest-project-git":   {scope: scopeProj, source: sourceGit, native: true},
	"install-manifest-global-git":    {scope: scopeUser, source: sourceGit, native: true},

	"list-marketplace":                  {scope: scopeNone, source: sourceGit},
	stepPlanMkt:                         {scope: scopeProj, source: sourceLocal},
	"install-marketplace-project-local": {scope: scopeProj, source: sourceLocal, native: true},
	"install-marketplace-global-git":    {scope: scopeUser, source: sourceGit, native: true},
	stepRefuseMkt:                       {scope: scopeProj, source: sourceLocal},

	"select-harness-all": {scope: scopeProj, source: sourceLocal, native: true},
	"select-agent-star":  {scope: scopeProj, source: sourceLocal, native: true},
	"select-all":         {scope: scopeProj, source: sourceLocal, native: true},
	"extra-unselected":   {scope: scopeProj, source: sourceLocal, except: []string{"omp"}},
	"extra-omp-profile-global": {
		scope: scopeUser, source: sourceLocal, native: true, only: []string{"omp"},
	},
	"extra-omp-profile-project": {
		scope: scopeProj, source: sourceLocal, native: true, only: []string{"omp"},
	},
	"extra-omp-profile-missing": {scope: scopeUser, source: sourceLocal, only: []string{"omp"}},

	"lifecycle-list":                    {scope: scopeProj, source: sourceGit},
	"lifecycle-check-current":           {scope: scopeProj, source: sourceGit},
	"lifecycle-check-drift":             {scope: scopeProj, source: sourceGit},
	"lifecycle-update":                  {scope: scopeProj, source: sourceGit, native: true},
	"lifecycle-update-refuse-modified":  {scope: scopeProj, source: sourceGit},
	"lifecycle-restore":                 {scope: scopeProj, source: sourceGit, native: true},
	"lifecycle-restore-refuse-modified": {scope: scopeProj, source: sourceGit},
	"lifecycle-restore-refuse-unbound": {
		scope: scopeProj, source: sourceGit, only: []string{"omp"},
	},
	"lifecycle-remove":                 {scope: scopeProj, source: sourceGit},
	"lifecycle-remove-refuse-modified": {scope: scopeProj, source: sourceGit},
	"lifecycle-clean-dependencies":     {scope: scopeProj, source: sourceGit},
	"lifecycle-global":                 {scope: scopeUser, source: sourceGit},
}

// nativeExclusions are (Harness, scope) pairs whose native check proves that
// the real Harness does NOT load agents from where agnthub installs them, with
// the reason that is still the right destination.
var nativeExclusions = map[string]map[string]string{
	"cursor": {
		scopeUser: "cursor-agent loads only workspace agents (.cursor/.claude/.grok " +
			"agents of the project); agnthub installs user agents where Cursor documents them " +
			"(~/.cursor/agents, read by the Cursor editor). The native check runs the real CLI " +
			"and asserts it still does not load them, so a Cursor release that starts " +
			"loading user agents fails the nightly run and removes this exclusion.",
	},
}

// evidence is how one scenario is proven for one Harness.
type evidence struct {
	// plan is a step whose plan lists the agent's outcome for the Harness:
	// installable (it must then appear written in every install step) or
	// refused (it must then appear refused in refuse, when given).
	plan    string
	install []string
	refuse  string
	// steps prove a behavior of the whole run (no single agent): each must
	// have a golden; its exit status says success or refusal.
	steps []string
	// contains must appear in the files.txt of every listed step that
	// succeeded.
	contains string
	// absent are steps whose golden must not install the agent for the
	// Harness: it is provably not part of that Harness's selection.
	absent []string
}

// scenario is one row of the coverage matrix.
type scenario struct {
	id    string
	group string
	title string
	// agent is the fixture agent the scenario is about; empty for run-level
	// behaviors proven by evidence.steps.
	agent string
	all   evidence
	// per overrides all for some Harnesses.
	per map[string]evidence
	// excluded gives the reason a Harness has no evidence. Every exclusion is
	// listed in COVERAGE.md.
	excluded map[string]string
}

// layoutEvidence proves a layout agent: its plan outcome, then a write in every
// scope and source when it installs, or the run-wide refusal when it does not.
var layoutEvidence = evidence{plan: stepPlan, install: layoutInstalls, refuse: stepRefuseLy}

// manifestEvidence proves a manifest agent the same way; refused manifest
// agents are refused in the plan, which writes nothing.
var manifestEvidence = evidence{plan: stepPlanMan, install: manifestInstalls}

// marketplaceEvidence proves a plugin agent of the marketplace fixture.
var marketplaceEvidence = evidence{
	plan: stepPlanMkt, install: marketplaceInstalls, refuse: stepRefuseMkt,
}

func plugin(id, title, agentID string) scenario {
	return scenario{
		id: id, group: "Claude Code plugins", title: title, agent: agentID,
		all: marketplaceEvidence,
	}
}

func layout(id, title, agentID string) scenario {
	return scenario{
		id:    id,
		group: "Discovery layouts",
		title: title,
		agent: agentID,
		all:   layoutEvidence,
	}
}

func conversion(group, id, title, agentID string) scenario {
	return scenario{id: id, group: group, title: title, agent: agentID, all: layoutEvidence}
}

// scenarios is the coverage matrix: every agent type and behavior agnthub
// supports, by Harness.
var scenarios = []scenario{
	layout("layout-root", "root `AGENT.md` (portable)", "root-helper"),
	layout("layout-agents-file", "`agents/<id>.md` (portable)", "planner"),
	layout("layout-agents-dir", "`agents/<id>/AGENT.md` (portable)", "researcher"),
	layout("layout-claude", "`.claude/agents/*.md` (Claude Code native)", "reviewer"),
	layout("layout-claude-recursive", "`.claude/agents/<dir>/*.md` (recursive)", "fixer"),
	layout("layout-opencode", "`.opencode/agents/*.md` (OpenCode native, CRLF)", "oc-native"),
	layout("layout-codex", "`.codex/agents/*.toml` (Codex TOML)", "cx-native"),
	layout("layout-copilot", "`.github/agents/*.agent.md` (Copilot CLI)", "gh-native"),
	layout("layout-copilot-md", "`.github/agents/*.md` (Copilot CLI, plain suffix)", "gh-plain"),
	layout("layout-cursor", "`.cursor/agents/*.md` (Cursor, `readonly`)", "cu-native"),
	layout("layout-gemini", "`.gemini/agents/*.md` (Gemini CLI)", "gm-native"),
	layout("layout-omp", "`.omp/agents/*.md` (omp)", "omp-native"),
	{
		id: "layout-not-agents", group: "Discovery layouts",
		title: "`AGENTS.md` and `README.md` are not agents (inventory)",
		all:   evidence{steps: []string{"list-layouts"}},
	},

	{
		id: "manifest-inventory", group: "agnthub.yaml manifest",
		title: "declared ids are the inventory (undeclared agent left out), categories",
		all:   evidence{steps: []string{"list-manifest"}},
	},
	{
		id: "manifest-variants", group: "agnthub.yaml manifest",
		title: "declared Variants (OpenCode, Codex) with a portable core", agent: "lead",
		all: manifestEvidence,
	},
	{
		id: "manifest-variant-only", group: "agnthub.yaml manifest",
		title: "agent declared only as a Claude Code Variant", agent: "native-only",
		all: manifestEvidence,
	},
	{
		id: "manifest-dep-same-repo", group: "agnthub.yaml manifest",
		title: "same-repository Agent Dependency", agent: "helper", all: manifestEvidence,
	},
	{
		id: "manifest-dep-cross-repo", group: "agnthub.yaml manifest",
		title: "Agent Dependency from another Git repository", agent: "companion",
		all: manifestEvidence,
	},
	{
		id: "manifest-dep-variant", group: "agnthub.yaml manifest",
		title: "dependency only the Codex Variant adds (elsewhere provably not installed)",
		agent: "codex-helper",
		all:   evidence{absent: manifestInstalls},
		per:   map[string]evidence{"codex": manifestEvidence},
	},
	{
		id: "manifest-skill-dep", group: "agnthub.yaml manifest",
		title: "Skill Dependency installed by skills.sh into the Harness's skill directory",
		all: evidence{
			steps: manifestInstalls, contains: "skills/e2e-skill/SKILL.md",
		},
		per: map[string]evidence{"gemini": {
			steps: append(
				[]string{"refuse-manifest-project-untrusted"}, manifestInstalls...),
			contains: "skills/e2e-skill/SKILL.md",
		}},
	},
	{
		id: "manifest-invalid-variant", group: "agnthub.yaml manifest",
		title: "every declared Variant invalid: refused, never falls back to the portable core",
		agent: "strict", all: manifestEvidence,
	},

	{
		id: "plugin-inventory", group: "Claude Code plugins",
		title: "marketplace inventory names each agent's plugin; a remote source and one " +
			"escaping the repository are reported, never read",
		all: evidence{steps: []string{"list-marketplace"}},
	},
	plugin("plugin-agents-array", "agent listed in a plugin's `plugin.json` `agents` array",
		"mk-lister"),
	plugin("plugin-default-dir", "agent in a plugin's default `agents/` directory",
		"mk-scanner"),
	plugin("plugin-default-dir-nested", "agent in a subfolder of a plugin's `agents/` directory",
		"mk-nested"),
	plugin("plugin-ignored-fields",
		"`permissionMode` and `hooks`, which Claude Code ignores in plugins: omitted with a note",
		"mk-guarded"),
	plugin("plugin-root-variable",
		"instructions that use `${CLAUDE_PLUGIN_ROOT}` need their plugin: refused", "mk-rooted"),

	conversion("Variants and conversion", "variant-precedence",
		"authored native Variant wins over conversion (portable + Claude + OpenCode)", "dual"),
	conversion("Variants and conversion", "variant-invalid",
		"invalid native Variant for the target: refused, never converted instead", "broken"),
	conversion("Variants and conversion", "convert-portable-metadata",
		"portable core with descriptive metadata (noted, omitted)", "tagged"),
	conversion("Variants and conversion", "unsupported-permission-mode",
		"Claude-only `permissionMode`: native on Claude Code, refused elsewhere", "guarded"),

	conversion("Tool restrictions", "tools-read-search-shell",
		"allowlist Read/Grep/Glob/Bash", "reviewer"),
	conversion("Tool restrictions", "tools-full", "allowlist with edit and shell", "fixer"),
	conversion("Tool restrictions", "tools-read-only-claude",
		"read-only allowlist (Read, Grep)", "colon-native"),
	conversion("Tool restrictions", "tools-read-only-opencode",
		"OpenCode `permission` edit/bash deny", "oc-native"),
	conversion("Tool restrictions", "tools-read-only-cursor",
		"Cursor `readonly: true`", "cu-native"),
	conversion("Tool restrictions", "tools-shell-withheld",
		"Codex `features.shell_tool = false`", "cx-native"),
	conversion("Tool restrictions", "tools-denylist",
		"denylist (`disallowedTools: Bash`)", "no-shell"),
	conversion("Tool restrictions", "tools-mcp-tool",
		"MCP tool reference (`mcp__github__get_issue`)", "issue-reader"),
	conversion("Tool restrictions", "tools-mcp-server",
		"whole MCP server (`mcp__github__*`)", "mcp-wide"),
	conversion("Tool restrictions", "tools-web", "web fetch and web search", "web-searcher"),
	conversion("Tool restrictions", "tools-delegate", "delegation (`Agent`)", "delegator"),
	conversion("Tool restrictions", "tools-max-turns", "turn limit (`maxTurns`)", "bounded"),
	conversion("Tool restrictions", "tools-background",
		"background execution (`background: true`)", "backgrounder"),
	conversion("Tool restrictions", "gemini-mcp-servers",
		"Gemini `mcp_servers` + `mcp_<server>_<tool>`: native on Gemini CLI", "gm-mcp"),
	conversion("Tool restrictions", "gemini-mcp-camel",
		"Gemini `mcpServers` (camelCase), which Gemini CLI rejects: refused", "gm-camel"),

	conversion(
		"Additive capabilities",
		"memory-project",
		"`memory: project` with edits: Claude Code's folder named in the instructions",
		"rememberer",
	),
	conversion("Additive capabilities", "memory-user",
		"`memory: user` with edits: emulated where agents may write under the home directory, "+
			"noted elsewhere", "journaler"),
	conversion("Additive capabilities", "memory-local",
		"`memory: local` with inherited tools", "local-notes"),
	conversion("Additive capabilities", "memory-read-only",
		"`memory: user` without edits: noted, never emulated or granted", "recaller"),
	conversion("Additive capabilities", "skills-preload",
		"`skills` preload: native `skills`/`autoload-skills`, else instructions plus the "+
			"skill tool", "skilled"),
	conversion(
		"Variants and conversion",
		"claude-ignored-keys",
		"keys Claude Code ignores (`capabilities`, `category`, `domain`): omitted with a note",
		"annotated",
	),
	conversion("Variants and conversion", "claude-ignored-restriction",
		"ignored key that reads as a restriction (`allowed-tools`): refused", "allowlisted"),
	conversion("Additive capabilities", "memory-skills-turns",
		"memory, skills and `maxTurns` together (the qa-lead shape)", "studio-qa"),

	conversion("Model fields", "model-native",
		"native `model: sonnet` kept byte-for-byte, dropped with a note on conversion",
		"reviewer"),
	conversion("Model fields", "model-portable",
		"portable `model`: uninstallable everywhere", "modelled"),

	conversion("Frontmatter edge cases", "fm-colon-portable",
		"unquoted colon in a portable description", "colon"),
	conversion("Frontmatter edge cases", "fm-colon-native",
		"unquoted colon in a Claude Code Variant", "colon-native"),
	conversion("Frontmatter edge cases", "fm-crlf-portable", "CRLF portable core", "researcher"),
	conversion("Frontmatter edge cases", "fm-crlf-native",
		"CRLF native Variant (installed with its CRLF)", "oc-native"),
	conversion("Frontmatter edge cases", "fm-bom-portable", "UTF-8 BOM portable core", "bom"),
	conversion("Frontmatter edge cases", "fm-bom-native",
		"UTF-8 BOM Claude Code Variant", "bom-native"),
	conversion("Frontmatter edge cases", "fm-unicode",
		"non-ASCII description and body", "planner"),
	conversion("Frontmatter edge cases", "fm-folded",
		"folded description (`>-`)", "researcher"),
	conversion("Frontmatter edge cases", "fm-literal",
		"literal block description (`|`)", "literal"),

	{
		id: "ambiguous-portable", group: "Ambiguous identities",
		title: "two portable files, one id: refused",
		all:   evidence{steps: []string{"list-ambiguous", "refuse-ambiguous-portable"}},
	},
	{
		id: "ambiguous-native", group: "Ambiguous identities",
		title: "two Claude Code files (recursive), one id: refused",
		all:   evidence{steps: []string{"refuse-ambiguous-native"}},
	},
	{
		id: "ambiguous-unaffected", group: "Ambiguous identities",
		title: "an unambiguous agent of the same source installs", agent: "solo",
		all: evidence{install: []string{"install-ambiguous-unaffected"}},
	},

	{
		id: "select-agent-star", group: "Selection",
		title: "`--agent '*'` selects every agent of the source",
		all:   evidence{steps: []string{"select-agent-star"}},
	},
	{
		id: "select-harness-all", group: "Selection",
		title: "`--harness all` installs into every Harness (detection: only this one)",
		all:   evidence{steps: []string{"select-harness-all"}},
	},
	{
		id: "select-all", group: "Selection",
		title: "`--all` = every agent, every Harness, no prompt",
		all:   evidence{steps: []string{"select-all"}},
	},
	{
		id: "select-yes-never-reduces", group: "Selection",
		title: "`--yes` never reduces a plan: one refused agent fails the run",
		all:   evidence{steps: []string{stepRefuseLy}},
	},
	{
		id: "extra-omp-profile", group: "Selection",
		title: "`--extra omp.profile`: omp profile destinations; other Harnesses refuse it",
		all:   evidence{steps: []string{"extra-unselected"}},
		per: map[string]evidence{"omp": {steps: []string{
			"extra-omp-profile-global", "extra-omp-profile-project",
			"extra-omp-profile-missing",
		}}},
	},

	lifecycle("lifecycle-list", "`list` shows installed agents and bindings"),
	lifecycle("lifecycle-check-current", "`check`: up to date"),
	lifecycle("lifecycle-check-drift", "`check`: local drift (modified file)"),
	lifecycle("lifecycle-update", "`update`: newer revision of the locked ref"),
	lifecycle("lifecycle-update-refuse-modified",
		"`update` refuses to overwrite a locally modified file"),
	{
		id: "lifecycle-restore", group: "Lifecycle",
		title: "`restore` recreates the lock's files byte for byte (omp: a machine without " +
			"recorded bindings must name the profile)",
		all: evidence{steps: []string{"lifecycle-restore"}},
		per: map[string]evidence{"omp": {steps: []string{
			"lifecycle-restore-refuse-unbound", "lifecycle-restore",
		}}},
	},
	lifecycle("lifecycle-restore-refuse-modified",
		"`restore` refuses when a locked file was modified"),
	lifecycle("lifecycle-remove",
		"`remove` deletes only owned files (unmanaged neighbor kept)"),
	lifecycle("lifecycle-remove-refuse-modified", "`remove` refuses a modified file"),
	lifecycle("lifecycle-clean-dependencies",
		"`remove --clean-dependencies` removes unused companions and skills"),
	lifecycle("lifecycle-global", "user scope: `add -g`, `list -g`, `check -g`, `remove -g`"),
}

func lifecycle(id, title string) scenario {
	return scenario{id: id, group: "Lifecycle", title: title, all: evidence{steps: []string{id}}}
}

// liveCredential is what the live tier needs to run one Harness with a real
// model, and how it invokes the installed agent.
type liveCredential struct {
	// secrets are environment variable names; the first one set is used.
	secrets []string
	how     string
}

// liveCredentials is the live tier per Harness (live_test.go).
var liveCredentials = map[string]liveCredential{
	"claude": {
		secrets: []string{"OPENROUTER_API_KEY"},
		how: "`claude -p --agent live-probe` through OpenRouter's Anthropic-compatible API " +
			"(`ANTHROPIC_BASE_URL=https://openrouter.ai/api`)",
	},
	"opencode": {
		secrets: []string{"OPENROUTER_API_KEY"},
		how:     "`opencode run --agent live-probe -m openrouter/<model>`",
	},
	"codex": {
		secrets: []string{"OPENROUTER_API_KEY"},
		how: "`codex exec --json` with an OpenRouter Responses provider; the main agent " +
			"spawns the `live-probe` role (Codex has no direct agent flag)",
	},
	"copilot": {
		secrets: []string{"OPENROUTER_API_KEY"},
		how: "`copilot -p --agent live-probe` with OpenRouter as BYOK provider " +
			"(`COPILOT_PROVIDER_BASE_URL`), no GitHub login",
	},
	"cursor": {
		secrets: []string{"CURSOR_API_KEY"},
		how: "`cursor-agent -p` signed in with a Cursor API key; the main agent runs the " +
			"`live-probe` subagent through its Task tool",
	},
	"gemini": {
		secrets: []string{"GEMINI_API_KEY"},
		how: "`gemini -p` with a Gemini API key; the main agent invokes the `live-probe` " +
			"subagent (`invoke_agent`)",
	},
	"omp": {
		secrets: []string{"OPENROUTER_API_KEY"},
		how:     "`omp -p --model openrouter/<model>`; the main agent runs `live-probe` via `task`",
	},
}
