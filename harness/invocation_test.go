package harness_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/agnthub-sh/agnthub/agent"
	"github.com/agnthub-sh/agnthub/harness"
)

// https://github.com/TryGhost/Ghost/blob/f2846cdacb6d/.github/agents/agentic-workflows.agent.md
// (minimized): a Copilot CLI agent the model never delegates to.
const agenticWorkflows = `---
description: GitHub Agentic Workflows (gh-aw) - Create, debug, and upgrade AI-powered workflows
disable-model-invocation: true
---
Route the request to the right gh-aw prompt.
`

const manualOnlyAsk = "Use only when the user explicitly asks for this agent."

// A manual-only agent stays manual-only where the harness can say so
// (Copilot CLI's own field, OpenCode's primary mode) and elsewhere installs
// with a description asking delegating models not to pick it, shown as
// reduced. Its tools are the same as without the field.
func TestManualOnlyAgents(t *testing.T) {
	path := ".github/agents/reviewer.agent.md"
	plain := strings.Replace(agenticWorkflows, "disable-model-invocation: true\n", "", 1)
	for _, target := range agent.AllHarnesses {
		if target == agent.Copilot {
			continue
		}
		artifact, err := generateFrom(t, agent.Copilot, target, path, agenticWorkflows)
		if err != nil {
			t.Errorf("%s: want an install, got %v", target, err)
			continue
		}
		asks := strings.Contains(string(artifact.Content), manualOnlyAsk)
		reduced := slices.Contains(artifact.ReducedNames(), harness.ReducedManualOnly)
		if target == agent.OpenCode {
			if asks || reduced || frontmatter(t, artifact)["mode"] != "primary" {
				t.Errorf("opencode: want a primary agent, got %v\n%s", artifact.ReducedNames(),
					artifact.Content)
			}
			continue
		}
		if !asks || !reduced || artifact.WithoutShell() {
			t.Errorf("%s: want the description to ask and a reduction, got %v\n%s", target,
				artifact.ReducedNames(), artifact.Content)
		}
		bare, err := generateFrom(t, agent.Copilot, target, path, plain)
		if err != nil || strings.Replace(string(artifact.Content), manualOnlyAsk+" ", "", 1) !=
			string(bare.Content) {
			t.Errorf("%s: only the description may change\n%s\n%s", target, artifact.Content,
				bare.Content)
		}
	}
	native, err := installer(t, agent.Copilot).Native("reviewer",
		variant(agent.Copilot, agenticWorkflows))
	if err != nil || string(native.Content) != agenticWorkflows {
		t.Errorf("copilot: want the variant as written, got %v", err)
	}
}

// Copilot CLI's retired infer: false means the same, and
// disable-model-invocation takes precedence when both are set.
func TestManualOnlyPrecedence(t *testing.T) {
	path := ".github/agents/reviewer.agent.md"
	for field, manual := range map[string]bool{
		"infer: false\n": true,
		"infer: false\ndisable-model-invocation: false\n": false,
		"infer: true\ndisable-model-invocation: true\n":   true,
	} {
		content := "---\ndescription: Reviews.\n" + field + "---\nReview.\n"
		artifact, err := generateFrom(t, agent.Copilot, agent.Claude, path, content)
		if err != nil || strings.Contains(string(artifact.Content), manualOnlyAsk) != manual {
			t.Errorf("%q: want manual-only %v, got %v\n%s", field, manual, err, artifact.Content)
		}
	}
	// An OpenCode subagent is not manual-only.
	artifact, err := generateFrom(t, agent.OpenCode, agent.Copilot, ".opencode/agents/reviewer.md",
		"---\ndescription: Reviews.\nmode: subagent\n---\nReview.\n")
	if err != nil || frontmatter(t, artifact)["disable-model-invocation"] != nil {
		t.Errorf("a subagent is not manual-only: %v\n%s", err, artifact.Content)
	}
}

// https://github.com/mem0ai/mem0/blob/f1791daa86d4/integrations/claude-code-plugin/agents/sidekick.md
// (minimized): a Claude Code agent that runs in its own worktree.
const sidekick = `---
name: reviewer
description: A Sonnet coding agent that works in a separate Git worktree.
model: sonnet
tools: Read, Grep, Glob, Bash, Edit, Write, WebFetch
isolation: worktree
---
You are Mem0's Sonnet coding agent.
`

const isolationAsk = "IMPORTANT: Work only inside a dedicated git worktree."

func TestIsolatedAgents(t *testing.T) {
	path := ".claude/agents/reviewer.md"
	native, err := installer(t, agent.Claude).Native("reviewer", variant(agent.Claude, sidekick))
	if err != nil || string(native.Content) != sidekick {
		t.Errorf("claude: want the variant as written, got %v", err)
	}
	claude, err := generatePortable(t, agent.Claude, sidekick)
	if err != nil || frontmatter(t, claude)["isolation"] != "worktree" ||
		strings.Contains(string(claude.Content), isolationAsk) {
		t.Errorf("claude: want isolation kept natively, got %v\n%s", err, claude.Content)
	}
	for _, target := range nonClaude {
		artifact, err := generateFrom(t, agent.Claude, target, path, sidekick)
		if err != nil {
			t.Errorf("%s: want an install, got %v", target, err)
			continue
		}
		content := string(artifact.Content)
		ask, body := strings.Index(content, isolationAsk), strings.Index(content, "You are Mem0")
		if ask < 0 || body < ask || !slices.Contains(artifact.ReducedNames(),
			harness.ReducedIsolation) || artifact.WithoutShell() {
			t.Errorf("%s: want the worktree instruction first and a reduction, got %v\n%s",
				target, artifact.ReducedNames(), content)
		}
	}
}

// Without a shell an agent that edits cannot create its worktree, so it stays
// refused; one that can neither edit nor run commands changes nothing, so it
// installs as if not isolated.
func TestIsolationNeedsAShellToEmulate(t *testing.T) {
	path := ".claude/agents/reviewer.md"
	editor := strings.Replace(sidekick, "Bash, ", "", 1)
	for _, target := range nonClaude {
		_, err := generateFrom(t, agent.Claude, target, path, editor)
		requireIncompatible(t, err)
	}
	reader := strings.Replace(sidekick, "Bash, Edit, Write, ", "", 1)
	plain := strings.Replace(reader, "isolation: worktree\n", "", 1)
	for _, target := range nonClaude {
		if target == agent.Codex {
			// Codex cannot withhold edits, so it refuses a read-only agent anyway.
			continue
		}
		artifact, err := generateFrom(t, agent.Claude, target, path, reader)
		bare, bareErr := generateFrom(t, agent.Claude, target, path, plain)
		if err != nil || bareErr != nil || string(artifact.Content) != string(bare.Content) ||
			!slices.Equal(artifact.Notes, bare.Notes) {
			t.Errorf("%s: isolation of a read-only agent is moot, got %v %v\n%s", target, err,
				bareErr, artifact.Content)
		}
	}
	// On Cursor an agent without edits is readonly, which also blocks its
	// shell: isolation is moot there too.
	shellReader := strings.Replace(sidekick, "Edit, Write, ", "", 1)
	artifact, err := generateFrom(t, agent.Claude, agent.Cursor, path, shellReader)
	requireReduced(t, "cursor", artifact, err, "shell")
	// Other isolation values stay refused.
	remote := strings.Replace(sidekick, "worktree\n", "remote\n", 1)
	_, err = generateFrom(t, agent.Claude, agent.OpenCode, path, remote)
	requireIncompatible(t, err)
}
