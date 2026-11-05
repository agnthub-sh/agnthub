package harness_test

import (
	"strings"
	"testing"

	"github.com/agnthub-sh/agnthub/agent"
	"github.com/agnthub-sh/agnthub/harness"
)

// withField adds header lines to an agent every target converts.
func withField(field string) string {
	return strings.Replace(claudeAgent("Read", "Edit", "Write", "Bash"),
		"color: blue\n", "color: blue\n"+field, 1)
}

// Claude Code reads these values like an omitted field (Claude Code 2.1.309's
// agent loader), so they convert everywhere and are noted as omitted.
func TestClaudeNoopFieldsConvert(t *testing.T) {
	for _, field := range []string{
		"permissionMode: default\n",
		"permissionMode: \"default\"\n",
		"permissionMode: 'default'\n",
		"permissionMode: manual\n",
		"permissionMode:\n",
		"disallowedTools: []\n",
		"disallowedTools:\n",
		"hooks: {}\n",
		"hooks:\n",
		"mcpServers: []\n",
		"mcpServers: {}\n",
		"isolation:\n",
		"isolation: \"\"\n",
		"initialPrompt: \"\"\n",
		"initialPrompt: \"  \"\n",
		"maxTurns:\n",
		"readonly: false\n",
		"permissions: {}\n",
		"disallowed-tools: []\n",
	} {
		source := withField(field)
		for _, target := range nonClaude {
			artifact, err := generateFrom(t, agent.Claude, target, ".claude/agents/reviewer.md",
				source)
			if err != nil {
				t.Errorf("%q → %s: %v", field, target, err)
				continue
			}
			// An empty denylist is a tool policy that withholds nothing, not an omission.
			key, _, _ := strings.Cut(field, ":")
			if key != "disallowedTools" && !hasNote(artifact.Notes, "omitted "+key) {
				t.Errorf("%q → %s: want the omission noted, got %q", field, target, artifact.Notes)
			}
		}
	}
}

// Values that change what the agent does, or that Claude Code rejects, stay
// refused on every other Harness.
func TestClaudeRestrictingFieldsStayRefused(t *testing.T) {
	for _, field := range []string{
		"permissionMode: plan\n",
		"permissionMode: acceptEdits\n",
		"permissionMode: dontAsk\n",
		"permissionMode: Default\n",
		"permissionMode: [default]\n",
		"hooks: []\n",
		"hooks:\n  Stop:\n    - command: echo\n",
		"mcpServers:\n  - github\n",
		"isolation: worktree\n",
		"isolation: remote\n",
		"initialPrompt: go\n",
		"maxTurns: 0\n",
		"readonly: true\n",
		"allowed-tools: []\n",
		"allowed-tools: [Read]\n",
		"permissions:\n  deny: [Bash]\n",
	} {
		source := withField(field)
		for _, target := range nonClaude {
			_, err := generateFrom(t, agent.Claude, target, ".claude/agents/reviewer.md", source)
			if err == nil {
				t.Errorf("%q → %s: want a refusal", field, target)
				continue
			}
			requireIncompatible(t, err)
		}
	}
}

// A denylist naming a tool withholds it wherever the agent converts.
func TestClaudeDenylistStillWithholds(t *testing.T) {
	source := strings.Replace(claudeAgent(), "color: blue\n", "disallowedTools: [Bash]\n", 1)
	for _, target := range nonClaude {
		artifact, err := generateFrom(t, agent.Claude, target, ".claude/agents/reviewer.md", source)
		if err != nil {
			requireIncompatible(t, err)
			continue
		}
		sem, err := installer(t, target).Semantics("reviewer", &agent.VariantFile{
			Harness: target, Path: artifact.FileName, Raw: artifact.Content,
		})
		if err != nil {
			t.Fatalf("%s: %v", target, err)
		}
		if sem.Tools.Allows(harness.CapShell) {
			t.Errorf("%s: the converted agent grants shell:\n%s", target, artifact.Content)
		}
	}
}

// Claude Code installs its own Variant as written; an empty hooks or
// mcpServers field declares nothing to run.
func TestClaudeNativeKeepsNoopFieldsVerbatim(t *testing.T) {
	source := withField("permissionMode: default\nhooks: {}\nmcpServers: []\n")
	artifact, err := installer(t, agent.Claude).Native("reviewer", variant(agent.Claude, source))
	if err != nil {
		t.Fatal(err)
	}
	if string(artifact.Content) != source {
		t.Errorf("want the file as written, got:\n%s", artifact.Content)
	}
	if hasNote(artifact.Notes, "runs when the agent is used") {
		t.Errorf("empty fields run nothing, got %q", artifact.Notes)
	}
}
