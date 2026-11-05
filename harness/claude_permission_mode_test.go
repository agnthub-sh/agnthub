package harness_test

import (
	"strings"
	"testing"

	"github.com/agnthub-sh/agnthub/agent"
	"github.com/agnthub-sh/agnthub/harness"
)

// Agents from the refusal census, minimized (instructions shortened).
var relaxedModeAgents = map[string]string{
	// https://github.com/pekkah/SharpInference/blob/2e421da98c3c/.claude/agents/implementer.md
	"implementer": `---
name: reviewer
description: Sonnet workhorse that executes a written implementation spec (from the architect agent or the main thread). Use for any well-scoped coding task — always pass the full spec in the prompt
model: sonnet
tools: Read, Edit, Write, Grep, Glob, Bash
permissionMode: acceptEdits
color: blue
---

Implement the spec you are given.
`,
	// https://github.com/maxyeh0817/Forge/blob/be81d2eb099d/.claude/agents/devops.md
	"devops": `---
name: reviewer
description: >
  部署運維工程師。負責部署配置、CI/CD 管線、Docker、環境管理、git 分支管理。
  不負責業務邏輯開發、UI 實作。
permissionMode: acceptEdits
model: sonnet
skills:
  - code-style
tools:
  - Read
  - Edit
  - Write
  - Bash
  - Glob
  - Grep
---

Handle deployment configuration.
`,
	// The shape of udfnd/copypasta-generator's expert agents without their hooks:
	// https://github.com/udfnd/copypasta-generator/blob/01d84980fb5e/.claude/agents/moai/expert-frontend.md
	"expert-frontend": `---
name: reviewer
description: Frontend development and UI/UX design specialist.
tools: Read, Write, Edit, Grep, Glob, WebFetch, Bash, TodoWrite, Skill
model: opus
permissionMode: bypassPermissions
---

Build the frontend.
`,
	"auto": `---
name: reviewer
description: Runs long tasks.
tools: Read, Edit, Bash
permissionMode: auto
---

Work through the task.
`,
}

// acceptEdits, auto and bypassPermissions only approve more without asking.
// Another Harness asks for approval as its session does, which is at least as
// strict, so the mode is dropped with a note and the tool policy carries as it
// would without it.
func TestClaudeRelaxingPermissionModesConvert(t *testing.T) {
	for name, source := range relaxedModeAgents {
		mode := ""
		for line := range strings.SplitSeq(source, "\n") {
			if value, ok := strings.CutPrefix(line, "permissionMode: "); ok {
				mode = value
			}
		}
		without := strings.Replace(source, "permissionMode: "+mode+"\n", "", 1)
		for _, target := range nonClaude {
			plain, plainErr := generateFrom(t, agent.Claude, target, ".claude/agents/reviewer.md",
				without)
			artifact, err := generateFrom(t, agent.Claude, target, ".claude/agents/reviewer.md",
				source)
			if (err == nil) != (plainErr == nil) {
				t.Errorf("%s → %s: %v, but without the mode %v", name, target, err, plainErr)
				continue
			}
			if err != nil {
				continue
			}
			want := "permissionMode " + mode + " isn't available in " + string(target) +
				"; the harness asks for approval as usual"
			if !hasNote(artifact.Notes, want) {
				t.Errorf("%s → %s: want %q, got %q", name, target, want, artifact.Notes)
			}
			if string(artifact.Content) != string(plain.Content) {
				t.Errorf("%s → %s: the mode changed the file:\n%s\nwithout it:\n%s", name, target,
					artifact.Content, plain.Content)
			}
		}
	}
}

// dontAsk restricts the agent, and modes are case-sensitive: these stay
// refused. A relaxing mode never lifts another refusal. plan converts as a
// read-only tool policy (policy_test.go).
func TestClaudeRestrictingPermissionModesStayRefused(t *testing.T) {
	sources := []string{
		withField("permissionMode: dontAsk\n"),
		withField("permissionMode: AcceptEdits\n"),
		withField("permissionMode: [acceptEdits]\n"),
		withField("permissionMode: bypassPermissions\nisolation: worktree\n"),
		withField("permissionMode: acceptEdits\nhooks:\n  PreToolUse:\n    - matcher: Bash\n" +
			"      hooks:\n        - type: command\n          command: ./guard.sh\n"),
	}
	for _, source := range sources {
		for _, target := range nonClaude {
			_, err := generateFrom(t, agent.Claude, target, ".claude/agents/reviewer.md", source)
			if err == nil {
				t.Errorf("→ %s: want a refusal for\n%s", target, source)
				continue
			}
			requireIncompatible(t, err)
		}
	}
}

// Claude Code installs its own file as written, mode included.
func TestClaudeNativeKeepsPermissionMode(t *testing.T) {
	source := relaxedModeAgents["implementer"]
	artifact, err := installer(t, agent.Claude).Native("reviewer", variant(agent.Claude, source))
	if err != nil {
		t.Fatal(err)
	}
	if string(artifact.Content) != source {
		t.Errorf("want the file as written, got:\n%s", artifact.Content)
	}
	sem, err := installer(t, agent.Claude).Semantics("reviewer", variant(agent.Claude, source))
	if err != nil {
		t.Fatal(err)
	}
	if sem.PermissionMode != "acceptEdits" || len(sem.Unsupported) > 0 {
		t.Errorf("semantics: %+v", sem)
	}
	if sem.Tools.Effective()&harness.CapEdit == 0 {
		t.Errorf("the allowlist must still grant edits: %v", sem.Tools.Effective())
	}
}
