package harness_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/agnthub-sh/agnthub/agent"
	"github.com/agnthub-sh/agnthub/harness"
)

// Conversion policy decided on 2026-11-05 (ADR 0012), each rule checked
// against minimized copies of real registry agents from the refusal census.

// generatePortable converts a portable core (agents/reviewer.md) for target.
func generatePortable(
	t *testing.T,
	target agent.HarnessID,
	content string,
) (harness.Artifact, error) {
	t.Helper()
	def := &agent.Definition{ID: "reviewer", Portable: portable(t, content)}
	artifact, _, _, err := harness.Default().Generate(installer(t, target), def)
	return artifact, err
}

// frontmatter parses a generated Markdown artifact.
func frontmatter(t *testing.T, artifact harness.Artifact) map[string]any {
	t.Helper()
	doc, err := agent.ParseMarkdown(artifact.FileName, artifact.Content)
	if err != nil {
		t.Fatalf("%s does not parse: %v\n%s", artifact.FileName, err, artifact.Content)
	}
	return doc.Frontmatter
}

func requireReduced(
	t *testing.T,
	label string,
	artifact harness.Artifact,
	err error,
	want ...string,
) {
	t.Helper()
	if err != nil {
		t.Errorf("%s: want an install without %v, got %v", label, want, err)
		return
	}
	if got := artifact.ReducedNames(); !slices.Equal(got, want) {
		t.Errorf("%s: reduced %v, want %v\n%s", label, got, want, artifact.Content)
	}
	if artifact.WithoutShell() != slices.Contains(want, harness.ReducedShell) {
		t.Errorf("%s: only a lost shell needs the opt-in", label)
	}
}

// P5: https://github.com/davidabeyer/formaltask/blob/a2ce84225302/agents/l2-module-mapper.md
// (minimized). A portable core is read as a Claude Code agent: Claude Code
// keeps its model, other harnesses get its tool policy and a note.
const moduleMapper = `---
name: reviewer
description: >
  Maps module-level dependencies: imports, exports, hotspots, coupling matrix.
tools:
  - Read
  - Glob
  - Grep
  - Bash
  - Write
model: sonnet
color: purple
---
Map the modules.
`

func TestPortableCoresReadAsClaudeCodeAgents(t *testing.T) {
	artifact, err := generatePortable(t, agent.Claude, moduleMapper)
	if err != nil {
		t.Fatal(err)
	}
	fields := frontmatter(t, artifact)
	if fields["model"] != "sonnet" || fields["tools"] != "Read, Grep, Glob, Edit, Write, Bash" ||
		fields["color"] != nil {
		t.Errorf("claude: want model and tools kept, color dropped: %v", fields)
	}
	for _, target := range nonClaude {
		artifact, err := generatePortable(t, target, moduleMapper)
		if err != nil {
			t.Errorf("%s: %v", target, err)
			continue
		}
		if strings.Contains(string(artifact.Content), "sonnet") ||
			!hasNote(artifact.Notes, `model "sonnet" is specific to claude`) {
			t.Errorf("%s: the Claude Code model must be noted, not carried: %v", target,
				artifact.Notes)
		}
	}
	gemini, _ := generatePortable(t, agent.Gemini, moduleMapper)
	if tools, _ := frontmatter(t, gemini)["tools"].([]any); slices.Contains(
		tools,
		any("web_fetch"),
	) ||
		!slices.Contains(tools, any("run_shell_command")) {
		t.Errorf("gemini: want exactly the allowlisted tools, got %v", tools)
	}
}

func TestPortableRestrictionsClaudeCodeIgnoresStillRefuse(t *testing.T) {
	for _, field := range []string{
		"permission:\n  edit: deny\n",
		"agents: [helper]\n",
		"spawns: [helper]\n",
		"readonly: true\n",
		"isolation: worktree\n",
		"hooks:\n  PreToolUse:\n    - matcher: Bash\n      hooks:\n        - type: command\n" +
			"          command: ./guard.sh\n",
	} {
		content := "---\nname: reviewer\ndescription: Reviews\n" + field + "---\nReview.\n"
		for _, target := range agent.AllHarnesses {
			_, err := generatePortable(t, target, content)
			var incompatible *harness.IncompatibleError
			if !errors.As(err, &incompatible) {
				t.Errorf("%q → %s: want a refusal, got %v", field, target, err)
			}
		}
	}
}

func TestPortableKeysClaudeCodeIgnoresAreNoted(t *testing.T) {
	content := "---\nname: reviewer\ndescription: Reviews\nmode: subagent\ncategory: quality\n" +
		"temperature: 0.2\npermissionMode: default\n---\nReview.\n"
	for _, target := range agent.AllHarnesses {
		artifact, err := generatePortable(t, target, content)
		if err != nil {
			t.Errorf("%s: %v", target, err)
			continue
		}
		if !hasNote(artifact.Notes, "omitted mode: Claude Code ignores it") ||
			!hasNote(artifact.Notes, "omitted permissionMode") {
			t.Errorf("%s: notes %v", target, artifact.Notes)
		}
	}
}

// P9: https://github.com/easonwang00/Startup-Agents/blob/dd10c9e1f21e/.claude/agents/growth-hacker.md
// (minimized). Copilot CLI grants custom agents no web search.
const growthHacker = `---
name: reviewer
description: Designs viral loops, runs growth experiments, and optimizes acquisition channels.
tools: Read, Write, Edit, Glob, Grep, Bash, WebFetch, WebSearch
model: sonnet
---
Grow.
`

func TestCopilotInstallsWithoutWebSearch(t *testing.T) {
	artifact, err := generateFrom(t, agent.Claude, agent.Copilot, ".claude/agents/reviewer.md",
		growthHacker)
	requireReduced(t, "copilot", artifact, err, "web search")
	tools, _ := frontmatter(t, artifact)["tools"].([]any)
	if !slices.Contains(tools, any("web")) || slices.Contains(tools, any("web_search")) {
		t.Errorf("copilot: want web fetch kept and no search entry, got %v", tools)
	}
	for _, target := range []agent.HarnessID{agent.OpenCode, agent.Gemini, agent.OMP} {
		artifact, err := generateFrom(t, agent.Claude, target, ".claude/agents/reviewer.md",
			growthHacker)
		requireReduced(t, string(target), artifact, err)
	}
}

// P10: https://github.com/davepoon/buildwithclaude/blob/dadd5c2678d0/plugins/shipwright/agents/shipwright.md
// (minimized). Gemini CLI subagents cannot delegate.
const shipwright = `---
name: reviewer
description: Use this agent to autonomously build, test, and deploy complete applications.
tools: Write, MultiEdit, Bash, Read, Glob, Task
---
Build it.
`

func TestGeminiInstallsWithoutDelegation(t *testing.T) {
	artifact, err := generateFrom(t, agent.Claude, agent.Gemini, ".claude/agents/reviewer.md",
		shipwright)
	requireReduced(t, "gemini", artifact, err, "delegation")
	if strings.Contains(string(artifact.Content), "invoke_agent") {
		t.Errorf("gemini: delegation must not be written:\n%s", artifact.Content)
	}
}

// P8: https://github.com/abyssparanoia/rapid-go/blob/56b39de68aa1/.claude/agents/convention-reviewer.md
// (minimized). Cursor's only restriction blocks the reviewer's shell.
const conventionReviewer = `---
name: reviewer
description: Checks compliance with project conventions and rules.
model: sonnet
tools: [Read, Glob, Grep, Bash]
---
Check the conventions.
`

func TestCursorReadonlyLosesTheShellAsAReduction(t *testing.T) {
	artifact, err := generateFrom(t, agent.Claude, agent.Cursor, ".claude/agents/reviewer.md",
		conventionReviewer)
	requireReduced(t, "cursor", artifact, err, "shell")
	if frontmatter(t, artifact)["readonly"] != true {
		t.Errorf("cursor: want readonly: true\n%s", artifact.Content)
	}
	// An agent whose edits Cursor keeps but whose shell it would have to drop
	// stays refused: readonly would remove the edits too.
	_, err = generateFrom(t, agent.Claude, agent.Cursor, ".claude/agents/reviewer.md",
		claudeAgent("Read", "Grep", "Glob", "Edit", "Write"))
	requireIncompatible(t, err)
}

// P6: https://github.com/Marcel-Schlanzke1994/auto-leads/blob/bc34f7fa08a8/.codex/agents/performance-engineer.toml
// and documentation-engineer.toml (minimized).
func codexSandbox(mode string) string {
	return "name = \"reviewer\"\ndescription = \"Use when a task needs performance " +
		"investigation.\"\nmodel = \"gpt-5.4\"\nmodel_reasoning_effort = \"high\"\n" +
		"sandbox_mode = \"" + mode + "\"\ndeveloper_instructions = \"\"\"\nInvestigate.\n\"\"\"\n"
}

func TestCodexReadOnlySandboxWithholdsEditsElsewhere(t *testing.T) {
	readOnly := codexSandbox("read-only")
	_, err := installer(t, agent.Codex).Native("reviewer", variant(agent.Codex, readOnly))
	requireIncompatible(t, err)
	claude, err := generateFrom(t, agent.Codex, agent.Claude, ".codex/agents/reviewer.toml",
		readOnly)
	requireReduced(t, "claude", claude, err)
	if denied, _ := frontmatter(t, claude)["disallowedTools"].(string); !strings.Contains(denied,
		"Edit") || strings.Contains(denied, "Bash") {
		t.Errorf("claude: want edits withheld and the shell kept, got %q", denied)
	}
	cursor, err := generateFrom(t, agent.Codex, agent.Cursor, ".codex/agents/reviewer.toml",
		readOnly)
	requireReduced(t, "cursor", cursor, err, "shell", "MCP tools")
	for _, mode := range []string{"workspace-write", "danger-full-access"} {
		native, err := installer(t, agent.Codex).Native("reviewer",
			variant(agent.Codex, codexSandbox(mode)))
		if err != nil || !hasNote(native.Notes, "parent session's sandbox") {
			t.Errorf("%s: want Codex to install it with a note, got %v %v", mode, native.Notes, err)
		}
		opencode, err := generateFrom(t, agent.Codex, agent.OpenCode,
			".codex/agents/reviewer.toml", codexSandbox(mode))
		if err != nil || strings.Contains(string(opencode.Content), "permission") {
			t.Errorf("%s → opencode: want no restriction, got %v\n%s", mode, err, opencode.Content)
		}
	}
}

// P15r: restriction-like fields a native Variant's own harness ignores install
// there with a warning and are honored elsewhere.
func TestRestrictionsTheirHarnessIgnoresAreHonoredElsewhere(t *testing.T) {
	// https://github.com/Cong-ty-TNNH-Q-Tech/Q-School/blob/1d7722f4c5eb/.cursor/agents/ecc-code-explorer.md
	const codeExplorer = "---\nname: reviewer\ndescription: Deeply analyzes existing codebase " +
		"features by tracing execution paths.\nmodel: sonnet\ntools: [Read, Grep, Glob, Bash]\n" +
		"---\nExplore.\n"
	native, err := installer(
		t,
		agent.Cursor,
	).Native("reviewer", variant(agent.Cursor, codeExplorer))
	if err != nil || !hasNote(native.Notes, "Cursor does not enforce tools") {
		t.Errorf("cursor: want it installed with a warning, got %v %v", native.Notes, err)
	}
	claude, err := generateFrom(t, agent.Cursor, agent.Claude, ".cursor/agents/reviewer.md",
		codeExplorer)
	if err != nil || frontmatter(t, claude)["tools"] != "Read, Grep, Glob, Bash" {
		t.Errorf("cursor → claude: want the allowlist enforced, got %v\n%s", err, claude.Content)
	}

	// https://github.com/sqlcss/sqlcsi/blob/763f93ba2bf5/.github/agents/tss-log-analysis.agent.md
	const logAnalysis = "---\nname: reviewer\ndescription: >-\n  General TSS log investigation.\n" +
		"tools: [execute, read, edit, search, agent, todo, web]\nagents: [errorlog-analysis, " +
		"import-xevent]\nmaxTurns: 12\n---\n\nInvestigate the logs.\n"
	path := ".github/agents/reviewer.agent.md"
	native, err = installer(
		t,
		agent.Copilot,
	).Native("reviewer", variant(agent.Copilot, logAnalysis))
	if err != nil || !hasNote(native.Notes, "Copilot CLI does not enforce agents") ||
		!hasNote(native.Notes, "Copilot CLI does not enforce maxTurns") {
		t.Errorf("copilot: want it installed with warnings, got %v %v", native.Notes, err)
	}
	omp, err := generateFrom(t, agent.Copilot, agent.OMP, path, logAnalysis)
	if spawns, _ := frontmatter(t, omp)["spawns"].([]any); err != nil ||
		!slices.Equal(spawns, []any{"errorlog-analysis", "import-xevent"}) {
		t.Errorf("copilot → omp: want spawns, got %v %v\n%s", spawns, err, omp.Content)
	}
	claude, err = generateFrom(t, agent.Copilot, agent.Claude, path, logAnalysis)
	requireReduced(t, "copilot → claude", claude, err, "delegation")
	if fields := frontmatter(t, claude); strings.Contains(fields["tools"].(string), "Agent") ||
		fields["maxTurns"] != 12 {
		t.Errorf("copilot → claude: want no delegation and the turn limit: %v", fields)
	}
	// Codex and Cursor cannot withhold delegation at all, so the limit is
	// noted as for any agent that withholds it there; nothing is reduced.
	for _, target := range []agent.HarnessID{agent.Codex, agent.Cursor} {
		artifact, err := generateFrom(t, agent.Copilot, target, path, logAnalysis)
		if err != nil || slices.Contains(artifact.ReducedNames(), "delegation") ||
			!hasNote(artifact.Notes, "cannot limit or withhold delegation") {
			t.Errorf("copilot → %s: want the delegation limit noted, not reduced: %v %v %v",
				target, artifact.ReducedNames(), artifact.Notes, err)
		}
	}

	const multiAgent = "name = \"reviewer\"\ndescription = \"Reviews\"\n" +
		"developer_instructions = \"Review.\"\n[features]\nmulti_agent = false\n"
	native, err = installer(t, agent.Codex).Native("reviewer", variant(agent.Codex, multiAgent))
	if err != nil || !hasNote(native.Notes, "Codex does not enforce features.multi_agent") {
		t.Errorf("codex: want it installed with a warning, got %v %v", native.Notes, err)
	}
	claude, err = generateFrom(t, agent.Codex, agent.Claude, ".codex/agents/reviewer.toml",
		multiAgent)
	if denied, _ := frontmatter(t, claude)["disallowedTools"].(string); err != nil ||
		!strings.Contains(denied, "Agent") {
		t.Errorf("codex → claude: want delegation withheld, got %q %v", denied, err)
	}
}

// P17: https://github.com/fattain-naime/cwp-superpowers/blob/65d6fd3c0936/agents/cwp-migration-planner.md
// (minimized).
const migrationPlanner = `---
name: reviewer
description: Use this agent when you need to plan or execute a server migration to or from CWP.
model: inherit
color: cyan
tools: ["Read", "Bash", "Grep"]
disallowedTools: ["Write", "Edit"]
maxConcurrent: 3
background: true
---
Plan the migration.
`

func TestBackgroundAgentsRunInTheForeground(t *testing.T) {
	claude, err := generatePortable(t, agent.Claude, migrationPlanner)
	if err != nil || frontmatter(t, claude)["background"] != true {
		t.Errorf("claude: want background kept, got %v\n%s", err, claude.Content)
	}
	for _, target := range []agent.HarnessID{agent.OpenCode, agent.Copilot, agent.Gemini, agent.OMP} {
		artifact, err := generatePortable(t, target, migrationPlanner)
		if err != nil || !hasNote(artifact.Notes, "runs in the foreground") {
			t.Errorf("%s: want a foreground install with a note, got %v %v", target, err,
				artifact.Notes)
		}
	}
}

// P16: scoped shell rules carry exactly between Claude Code and OpenCode and
// are withheld, with the opt-in, elsewhere.
func TestScopedShellRules(t *testing.T) {
	// https://github.com/shresthaAlex/spendly/blob/3d5491e4cf7e/.claude/agents/spendly-security-reviewer.md
	const securityReviewer = "---\nname: reviewer\ndescription: \"Reviews a completed " +
		"Spendly feature for security issues.\"\ntools: Read, Grep, Glob, Bash(git diff), " +
		"Bash(git log:*)\nmodel: inherit\ncolor: yellow\n---\nReview.\n"
	path := ".claude/agents/reviewer.md"
	opencode, err := generateFrom(t, agent.Claude, agent.OpenCode, path, securityReviewer)
	requireReduced(t, "opencode", opencode, err)
	if !strings.Contains(string(opencode.Content),
		"  bash:\n    '*': deny\n    git diff: allow\n    git log *: allow\n") {
		t.Errorf("opencode: want ordered bash patterns\n%s", opencode.Content)
	}
	// The generated OpenCode agent reads back as the same scope.
	sem, err := installer(t, agent.OpenCode).Semantics("reviewer",
		variant(agent.OpenCode, string(opencode.Content)))
	if err != nil || sem.Tools.Shell == nil || len(sem.Tools.Shell.Rules) != 2 {
		t.Errorf("opencode round trip: %+v %v", sem, err)
	}
	for _, target := range []agent.HarnessID{agent.Copilot, agent.Gemini, agent.OMP, agent.Cursor} {
		artifact, err := generateFrom(t, agent.Claude, target, path, securityReviewer)
		requireReduced(t, string(target), artifact, err, "shell")
	}
	_, err = generateFrom(t, agent.Claude, agent.Codex, path, securityReviewer)
	requireIncompatible(t, err)

	// https://github.com/NickMonrad/kernel-ai-assistant/blob/c0c530a88804/.opencode/agents/spec-writer.md
	const specWriter = "---\ndescription: Writes and maintains README and docs\nmode: subagent\n" +
		"model: llama.cpp/Qwen3.6-35B-A3B-UD-Q4_K_M\ntemperature: 0.4\npermission:\n  bash:\n" +
		"    \"*\": deny\n    \"grep *\": allow\n    \"find *\": allow\n    \"cat *\": allow\n" +
		"---\nWrite the docs.\n"
	for _, target := range []agent.HarnessID{agent.Claude, agent.Copilot, agent.Gemini, agent.OMP} {
		artifact, err := generateFrom(t, agent.OpenCode, target, ".opencode/agents/reviewer.md",
			specWriter)
		requireReduced(t, "opencode → "+string(target), artifact, err, "shell")
	}
	claude, _ := generateFrom(t, agent.OpenCode, agent.Claude, ".opencode/agents/reviewer.md",
		specWriter)
	if denied, _ := frontmatter(t, claude)["disallowedTools"].(string); !strings.Contains(denied,
		"Bash") {
		t.Errorf("opencode → claude: want the shell withheld, got %q", denied)
	}
}

// P20: https://github.com/Marcel-Bich/marcel-bich-claude-marketplace/blob/f2750081cbd5/plugins/dogma/agents/checklist-manager.md
// (minimized). Claude Code ignores allowed-tools in agents; other harnesses
// enforce it.
const checklistManager = `---
name: reviewer
description: Scan and analyze checklists in the project, return summary for user decision
model: inherit
allowed-tools:
  - Glob
  - Grep
  - Read
  - Edit
---
Scan the checklists.
`

func TestClaudeAllowedToolsIsTheAllowlistElsewhere(t *testing.T) {
	native, err := installer(t, agent.Claude).Native("reviewer",
		variant(agent.Claude, checklistManager))
	if err != nil || !hasNote(native.Notes, "Claude Code does not enforce allowed-tools") {
		t.Errorf("claude: want it installed with a warning, got %v %v", native.Notes, err)
	}
	omp, err := generateFrom(t, agent.Claude, agent.OMP, ".claude/agents/reviewer.md",
		checklistManager)
	tools, _ := frontmatter(t, omp)["tools"].([]any)
	if err != nil || slices.Contains(tools, any("bash")) || !slices.Contains(tools, any("edit")) {
		t.Errorf("omp: want the allowed-tools allowlist, got %v %v", tools, err)
	}
	// With a tools field too, the agent gets what both allow.
	both := strings.Replace(checklistManager, "model: inherit\n",
		"model: inherit\ntools: Read, Grep, Bash\n", 1)
	gemini, err := generateFrom(t, agent.Claude, agent.Gemini, ".claude/agents/reviewer.md", both)
	tools, _ = frontmatter(t, gemini)["tools"].([]any)
	if err != nil || slices.Contains(tools, any("run_shell_command")) ||
		slices.Contains(tools, any("replace")) || !slices.Contains(tools, any("read_file")) {
		t.Errorf("gemini: want the intersection, got %v %v", tools, err)
	}
}

// P2b: https://github.com/jagadeesh-profile/InstantDeploy/blob/8c0149e9b00e/.claude/agents/debugger.md
// (minimized). Plan mode becomes a read-only tool policy.
const planDebugger = `---
name: reviewer
description: Expert debugging and root cause analysis specialist.
tools: Read, Grep, Glob, Bash
model: sonnet
permissionMode: plan
---
Diagnose the failure.
`

func TestPlanModeConvertsAsReadOnly(t *testing.T) {
	path := ".claude/agents/reviewer.md"
	for _, target := range []agent.HarnessID{agent.OpenCode, agent.Copilot, agent.Gemini, agent.OMP, agent.Cursor} {
		artifact, err := generateFrom(t, agent.Claude, target, path, planDebugger)
		requireReduced(t, string(target), artifact, err, "shell")
	}
	opencode, _ := generateFrom(t, agent.Claude, agent.OpenCode, path, planDebugger)
	if content := string(opencode.Content); strings.Contains(content, "bash: allow") ||
		strings.Contains(content, "edit: allow") {
		t.Errorf("opencode: want no edits or shell\n%s", content)
	}
	_, err := generateFrom(t, agent.Claude, agent.Codex, path, planDebugger)
	requireIncompatible(t, err)
	// A plan-mode agent without a shell loses nothing.
	noShell := strings.Replace(planDebugger, "Glob, Bash", "Glob", 1)
	artifact, err := generateFrom(t, agent.Claude, agent.Gemini, path, noShell)
	requireReduced(t, "gemini without shell", artifact, err)
	// Claude Code keeps plan mode for a portable core read as its agent.
	claude, err := generatePortable(t, agent.Claude, planDebugger)
	if err != nil || frontmatter(t, claude)["permissionMode"] != "plan" ||
		claude.WithoutShell() {
		t.Errorf("claude: want permissionMode plan kept, got %v\n%s", err, claude.Content)
	}
}

// P19: agent-scoped MCP servers install natively on Gemini CLI only.
func TestAgentScopedMCPServersOnGeminiOnly(t *testing.T) {
	const context7 = "---\nname: reviewer\ndescription: Looks up library docs\n" +
		"tools: Read, mcp__context7__get-library-docs\nmcpServers:\n  - context7:\n" +
		"      type: http\n      url: https://mcp.context7.com/mcp\n---\nLook it up.\n"
	path := ".claude/agents/reviewer.md"
	gemini, err := generateFrom(t, agent.Claude, agent.Gemini, path, context7)
	if err != nil {
		t.Fatal(err)
	}
	servers, _ := frontmatter(t, gemini)["mcp_servers"].(map[string]any)
	if server, _ := servers["context7"].(map[string]any); server["httpUrl"] !=
		"https://mcp.context7.com/mcp" {
		t.Errorf("gemini: want an HTTP server, got %v\n%s", servers, gemini.Content)
	}
	for _, target := range []agent.HarnessID{agent.OpenCode, agent.Codex, agent.Copilot, agent.Cursor, agent.OMP} {
		_, err := generateFrom(t, agent.Claude, target, path, context7)
		requireIncompatible(t, err)
	}
	// https://github.com/shreyashjagtap157/Omni---New/blob/3b28d569a806/.github/agents/context7.agent.md
	// (minimized): GitHub secrets syntax has no Gemini CLI equivalent.
	const copilotContext7 = "---\nname: reviewer\ndescription: 'Expert in latest library " +
		"versions'\ntools: ['read', 'search', 'web', 'context7/*']\nmcp-servers:\n  context7:\n" +
		"    type: http\n    url: \"https://mcp.context7.com/mcp\"\n    headers: " +
		"{\"CONTEXT7_API_KEY\": \"${{ secrets.COPILOT_MCP_CONTEXT7 }}\"}\n" +
		"    tools: [\"get-library-docs\", \"resolve-library-id\"]\n---\nAnswer.\n"
	_, err = generateFrom(t, agent.Copilot, agent.Gemini, ".github/agents/reviewer.agent.md",
		copilotContext7)
	requireIncompatible(t, err)
	plain := strings.Replace(copilotContext7, "    headers: {\"CONTEXT7_API_KEY\": "+
		"\"${{ secrets.COPILOT_MCP_CONTEXT7 }}\"}\n", "", 1)
	gemini, err = generateFrom(t, agent.Copilot, agent.Gemini, ".github/agents/reviewer.agent.md",
		plain)
	servers, _ = frontmatter(t, gemini)["mcp_servers"].(map[string]any)
	if server, _ := servers["context7"].(map[string]any); err != nil ||
		len(server["includeTools"].([]any)) != 2 {
		t.Errorf("copilot → gemini: want the server and its tools, got %v %v", servers, err)
	}
}

// Restrictions a target can only express in prose or by loosening stay refused.
func TestContainmentStaysRefused(t *testing.T) {
	cases := []struct {
		name, path, content string
		source              agent.HarnessID
		targets             []agent.HarnessID
	}{
		{
			// P11: https://github.com/Yomiamy/flutter_inspector_kit/blob/1b65b9db0b37/.claude/agents/plan-verifier.md
			"P11 Codex withholding edits", ".claude/agents/reviewer.md",
			"---\nname: reviewer\ndescription: Adversarial plan review.\nmodel: opus\n" +
				"effort: xhigh\ntools: [Read, Glob, Grep]\n---\nReview.\n",
			agent.Claude,
			[]agent.HarnessID{agent.Codex},
		},
		{
			// P13: https://github.com/bengeek06/virtrtlab/blob/95d54b9d0c6e/.github/agents/spec-reviewer.agent.md
			"P13 Cursor withholding reads", ".github/agents/reviewer.agent.md",
			"---\ndescription: Specification reviewer\ntools:\n  - search/codebase\n" +
				"  - web/fetch\n---\nReview.\n",
			agent.Copilot,
			[]agent.HarnessID{agent.Cursor},
		},
		{
			// P18: https://github.com/davidgarciagordo/swarm/blob/3a30f8c6ee7c/agents/implementer.md
			"P18 isolation", ".claude/agents/reviewer.md",
			"---\nname: reviewer\ndescription: Writes a phase's code in a worktree.\n" +
				"tools: Read, Grep, Glob, Write, Edit, Bash\nisolation: worktree\n---\nImplement.\n",
			agent.Claude, nonClaude,
		},
		{
			// P21: https://github.com/slabgorb/arcade/blob/81b8c5889f55/.claude/agents/sm-setup.md
			"P21 real hooks",
			".claude/agents/reviewer.md",
			"---\nname: reviewer\ndescription: SM setup subagent\ntools: Bash, Read, Edit, Write\n" +
				"hooks:\n  PreToolUse:\n    - command: pf hooks schema-validation\n" +
				"      matcher: Write\n---\nSet up.\n",
			agent.Claude,
			nonClaude,
		},
	}
	for _, tc := range cases {
		for _, target := range tc.targets {
			_, err := generateFrom(t, tc.source, target, tc.path, tc.content)
			var incompatible *harness.IncompatibleError
			if !errors.As(err, &incompatible) {
				t.Errorf("%s → %s: want a refusal, got %v", tc.name, target, err)
			}
		}
	}
}

// P7: https://github.com/davidgarciagordo/swarm/blob/3a30f8c6ee7c/agents/review-orchestrator.md
// (minimized). A plugin agent that needs its plugin stays refused and points
// Claude Code users to the plugin itself.
func TestPluginAgentsPointToThePluginInstall(t *testing.T) {
	file := &agent.VariantFile{
		Harness: agent.Claude, Path: "plugins/swarm/agents/reviewer.md",
		Plugin: "swarm", Marketplace: "garcia-tools",
		Raw: []byte("---\nname: reviewer\ndescription: Independent review panel\n" +
			"tools: Read, Grep, Bash\n---\nRun ${CLAUDE_PLUGIN_ROOT}/scripts/panel.sh.\n"),
	}
	_, err := installer(t, agent.Claude).Native("reviewer", file)
	requireInvalid(t, err)
	if !strings.Contains(err.Error(), "/plugin install swarm@garcia-tools") {
		t.Errorf("want the plugin install command, got %v", err)
	}
	_, err = generatePortable(t, agent.OpenCode, "---\nname: reviewer\ndescription: Panel\n---\n"+
		"Run ${CLAUDE_PLUGIN_ROOT}/scripts/panel.sh.\n")
	requireIncompatible(t, err)
}

// A conversion that worked before ADR 0012 keeps its source: a source that
// only converts through the new readings, or only with reduced capability,
// never replaces it, so an existing install neither gains nor loses
// capabilities on update or restore.
func TestSourcesThatConvertAsBeforeKeepWinning(t *testing.T) {
	def := func(portableContent string, variants map[agent.HarnessID]string) *agent.Definition {
		d := &agent.Definition{ID: "reviewer", Variants: map[agent.HarnessID]*agent.VariantFile{}}
		if portableContent != "" {
			d.Portable = portable(t, portableContent)
		}
		for h, content := range variants {
			path := ".claude/agents/reviewer.md"
			if h == agent.OpenCode {
				path = ".opencode/agents/reviewer.md"
			}
			d.Variants[h] = &agent.VariantFile{Harness: h, Path: path, Raw: []byte(content)}
		}
		return d
	}
	generate := func(target agent.HarnessID, d *agent.Definition) string {
		t.Helper()
		_, source, _, err := harness.Default().Generate(installer(t, target), d)
		if err != nil {
			t.Fatalf("%s: %v", target, err)
		}
		return source
	}

	// A portable core with Claude Code fields and every tool, beside a
	// read-only Claude Code Variant: the Variant still decides elsewhere.
	readOnly := def("---\nname: reviewer\ndescription: Reviews.\nmodel: sonnet\n---\nReview.\n",
		map[agent.HarnessID]string{agent.Claude: "---\nname: reviewer\ndescription: Reviews.\n" +
			"tools: Read, Grep, Glob\n---\nReview.\n"})
	for _, target := range []agent.HarnessID{agent.OpenCode, agent.Gemini, agent.OMP} {
		if got := generate(target, readOnly); got != ".claude/agents/reviewer.md" {
			t.Errorf("%s: converted %s, want the Claude Code Variant", target, got)
		}
	}

	// A Claude Code Variant Copilot CLI can only install without web search,
	// beside an OpenCode Variant it installs in full.
	searcher := def("", map[agent.HarnessID]string{
		agent.Claude: "---\nname: reviewer\ndescription: Reviews.\n" +
			"tools: Read, Grep, Glob, WebSearch\n---\nReview.\n",
		agent.OpenCode: "---\ndescription: Reviews.\n---\nReview.\n",
	})
	if got := generate(agent.Copilot, searcher); got != ".opencode/agents/reviewer.md" {
		t.Errorf("copilot: converted %s, want the OpenCode Variant it installs in full", got)
	}

	// Alone, the portable core with Claude Code fields converts.
	alone := def("---\nname: reviewer\ndescription: Reviews.\nmodel: sonnet\n---\nReview.\n", nil)
	if got := generate(agent.OpenCode, alone); got != "agents/reviewer/AGENT.md" {
		t.Errorf("opencode: converted %s, want the portable core", got)
	}
}

// Plan mode is read-only: besides edits and shell, it withholds delegation
// and MCP tools, which could write, and shows each loss.
func TestPlanModeWithholdsEverythingThatCouldWrite(t *testing.T) {
	const planner = "---\nname: reviewer\ndescription: Plans.\npermissionMode: plan\n" +
		"tools: Read, Grep, Agent, mcp__github__create_issue\n---\nPlan.\n"
	artifact, err := generatePortable(t, agent.OpenCode, planner)
	requireReduced(t, "opencode", artifact, err, "delegation", "MCP tools")
	if content := string(artifact.Content); strings.Contains(content, "task: allow") ||
		strings.Contains(content, "github_create_issue") {
		t.Errorf("opencode: plan mode kept delegation or an MCP tool:\n%s", content)
	}
	artifact, err = generatePortable(t, agent.Copilot,
		"---\nname: reviewer\ndescription: Plans.\npermissionMode: plan\n---\nPlan.\n")
	requireReduced(t, "copilot without a tool list", artifact, err,
		harness.ReducedShell, "delegation", "MCP tools", "web search")
}

// Two shell command lists keep the commands both allow; sharing none, the
// agent is refused instead of silently losing its shell.
func TestTwoShellCommandListsIntersect(t *testing.T) {
	const twoLists = "---\nname: reviewer\ndescription: Reviews.\n" +
		"tools: Read, Edit, Bash(git diff *), Bash(git log *)\n" +
		"allowed-tools: Read, Edit, Bash(git log *)\n---\nReview.\n"
	artifact, err := generateFrom(t, agent.Claude, agent.OpenCode, ".claude/agents/reviewer.md",
		twoLists)
	if err != nil || !strings.Contains(string(artifact.Content), "git log *") ||
		strings.Contains(string(artifact.Content), "git diff") {
		t.Errorf("want only the shared git log rule, got %v\n%s", err, artifact.Content)
	}
	disjoint := strings.Replace(twoLists, "Bash(git log *)\n---", "Bash(git status *)\n---", 1)
	if _, err := generateFrom(t, agent.Claude, agent.OpenCode, ".claude/agents/reviewer.md",
		disjoint); err == nil {
		t.Error("two command lists sharing nothing: want a refusal")
	}
}

// Restriction-like keys of other dialects keep a portable core refused.
func TestPortableRestrictionsOfOtherDialectsRefuse(t *testing.T) {
	for _, key := range []string{
		"excludeTools: [Bash]", "exclude_tools: [run_shell_command]", "steps: 5",
		"maxSteps: 5", "timeout_mins: 5", "features: {multi_agent: false}",
	} {
		_, err := generatePortable(t, agent.OpenCode,
			"---\nname: reviewer\ndescription: Reviews.\n"+key+"\n---\nReview.\n")
		var incompatible *harness.IncompatibleError
		if !errors.As(err, &incompatible) {
			t.Errorf("%s: want a refusal, got %v", key, err)
		}
	}
	// A .claude/agents file keeps Claude Code's reading: the key is ignored.
	if _, err := generateFrom(t, agent.Claude, agent.OpenCode, ".claude/agents/reviewer.md",
		"---\nname: reviewer\ndescription: Reviews.\nsteps: 40\n---\nReview.\n"); err != nil {
		t.Errorf("claude variant with steps: want it converted, got %v", err)
	}
}

// An agent without a tool list uses the session's MCP tools, which Cursor's
// readonly blocks.
func TestCursorReadonlyShowsLostSessionMCPTools(t *testing.T) {
	artifact, err := generatePortable(t, agent.Cursor, "---\nname: reviewer\n"+
		"description: Reviews.\ndisallowedTools: Edit, Write, NotebookEdit, Bash\n---\nReview.\n")
	requireReduced(t, "cursor", artifact, err, "MCP tools")
}

func TestEmptyPortableModelIsNotWritten(t *testing.T) {
	artifact, err := generatePortable(t, agent.Claude,
		"---\nname: reviewer\ndescription: Reviews.\nmodel:\n---\nReview.\n")
	if err != nil || strings.Contains(string(artifact.Content), "model") {
		t.Errorf("want no model line, got %v\n%s", err, artifact.Content)
	}
}
