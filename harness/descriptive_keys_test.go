package harness_test

import (
	"strings"
	"testing"

	"github.com/agnthub-sh/agnthub/agent"
)

// Native Variants from the refusal census whose only extra keys are
// documentation their own Harness ignores (minimized: instructions shortened).
var descriptiveVariants = []struct {
	name    string
	harness agent.HarnessID
	path    string
	content string
	notes   []string
}{
	{
		// https://github.com/davidop/fabric-gsd-demo/blob/ddec70004908/.github/agents/gsd-debug-session-manager.agent.md
		"gsd-debug-session-manager", agent.Copilot, ".github/agents/reviewer.agent.md",
		"---\nname: reviewer\ndescription: \"Manages multi-cycle /gsd-debug checkpoint and " +
			"continuation loop in isolated context.\"\ntools: ['read', 'edit', 'execute', " +
			"'search', 'agent', 'ask_user']\ncolor: orange\n---\n\n<role>Manage the loop.</role>\n",
		[]string{"color is not used by Copilot CLI"},
	},
	{
		// https://github.com/QuantumHope/LeanQuill/blob/982feac6a909/.github/agents/gsd-plan-checker.agent.md
		"gsd-plan-checker", agent.Copilot, ".github/agents/reviewer.agent.md",
		"---\nname: reviewer\ndescription: Verifies plans will achieve phase goal before " +
			"execution.\ntools: ['read', 'execute', 'search']\ncolor: green\n---\n\n" +
			"<role>Check the plan.</role>\n",
		[]string{"color is not used by Copilot CLI"},
	},
	{
		// https://github.com/Thox-ai/thox-imagine/blob/b6d279942546/.cursor/agents/swift-concurrency-expert.md
		"swift-concurrency-expert", agent.Cursor, ".cursor/agents/reviewer.md",
		"---\nname: reviewer\ndescription: Expert in Swift concurrency, async/await, actors, " +
			"and structured concurrency\nmodel: inherit\ncategory: ios-development\n" +
			"team: ios-development\ncolor: green\n---\n\nReview the concurrency.\n",
		[]string{
			"category is not used by Cursor", "color is not used by Cursor",
			"team is not used by Cursor",
		},
	},
	{
		// https://github.com/vanillagreencom/vsys/blob/55deac0603c5/.codex/agents/reviewer-quality.toml
		// without its sandbox_mode, which stays refused.
		"reviewer-quality", agent.Codex, ".codex/agents/reviewer.toml",
		"name = \"reviewer\"\nnickname_candidates = [\"Reviewer-Quality-Atlas\", " +
			"\"Reviewer-Quality-Delta\", \"Reviewer-Quality-Echo\"]\ndescription = \"Code " +
			"quality reviewer for maintainability.\"\nmodel_reasoning_effort = \"high\"\n" +
			"developer_instructions = '''\n# Code Quality Review\n'''\n",
		nil,
	},
}

// The Harness loads these files and acts on none of the extra keys (Copilot
// CLI 1.0.105 and Cursor list the agents; Codex 0.164.0 loads valid
// nickname_candidates), so they install as written and convert elsewhere.
func TestDescriptiveKeysInstallAsWritten(t *testing.T) {
	for _, tc := range descriptiveVariants {
		artifact, err := installer(t, tc.harness).Native("reviewer", &agent.VariantFile{
			Harness: tc.harness, Path: tc.path, Raw: []byte(tc.content),
		})
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if string(artifact.Content) != tc.content {
			t.Errorf("%s: want the file as written, got:\n%s", tc.name, artifact.Content)
		}
		for _, note := range tc.notes {
			if !hasNote(artifact.Notes, note) {
				t.Errorf("%s: want %q, got %q", tc.name, note, artifact.Notes)
			}
		}
		_, err = generateFrom(t, tc.harness, agent.Claude, tc.path, tc.content)
		if err != nil {
			t.Errorf("%s: does not convert to Claude Code: %v", tc.name, err)
		}
	}
}

// Keys that read as restrictions stay refused even though the Harness ignores
// them (a policy decision), as do keys Codex and Gemini CLI refuse to load and
// nickname lists Codex rejects.
func TestUndocumentedKeysThatMatterStayInvalid(t *testing.T) {
	cases := []struct {
		name    string
		harness agent.HarnessID
		path    string
		content string
	}{
		{
			// https://github.com/benjamin-t-brown/carcer-wayward-world/blob/4374b1a316cc/.cursor/agents/ralphloop-executor.md
			"cursor tools", agent.Cursor, ".cursor/agents/reviewer.md",
			"---\nname: reviewer\ndescription: \"Autonomously implements all tasks.\"\nskills:\n" +
				"  - autonomous-loop\ncolor: orange\nmaxTurns: 100\ntools: Glob, Grep, Read, " +
				"Shell, Write\n---\n\nRun the loop.\n",
		},
		{
			"copilot readonly", agent.Copilot, ".github/agents/reviewer.agent.md",
			"---\nname: reviewer\ndescription: Diagnoses failures.\nreadonly: true\ncolor: red\n" +
				"---\n\nDiagnose.\n",
		},
		{
			"codex color", agent.Codex, ".codex/agents/reviewer.toml",
			"name = \"reviewer\"\ndescription = \"d\"\ncolor = \"blue\"\n" +
				"developer_instructions = \"x\"\n",
		},
		{
			// https://github.com/SimonZhangM/OPC-agent-skill-pack/blob/0a3a5bb064e8/.codex/agents/no1-founder-orchestrator.toml
			"codex nickname beside sandbox_mode", agent.Codex, ".codex/agents/reviewer.toml",
			"name = \"reviewer\"\ndescription = \"Coordinator for AI-native founder " +
				"workflows.\"\nmodel_reasoning_effort = \"high\"\nsandbox_mode = \"read-only\"\n" +
				"nickname_candidates = [\"North Star\", \"Stage Lead\", \"Founder OS\"]\n\n" +
				"developer_instructions = \"\"\"\nCoordinate.\n\"\"\"\n",
		},
		{
			"codex empty nicknames", agent.Codex, ".codex/agents/reviewer.toml",
			"name = \"reviewer\"\ndescription = \"d\"\nnickname_candidates = []\n" +
				"developer_instructions = \"x\"\n",
		},
		{
			"codex nickname punctuation", agent.Codex, ".codex/agents/reviewer.toml",
			"name = \"reviewer\"\ndescription = \"d\"\nnickname_candidates = [\"Ada!\"]\n" +
				"developer_instructions = \"x\"\n",
		},
		{
			"codex duplicate nicknames", agent.Codex, ".codex/agents/reviewer.toml",
			"name = \"reviewer\"\ndescription = \"d\"\nnickname_candidates = [\"Ada\", \"Ada\"]\n" +
				"developer_instructions = \"x\"\n",
		},
		{
			"gemini color", agent.Gemini, ".gemini/agents/reviewer.md",
			"---\nname: reviewer\ndescription: Reviews code.\ncolor: green\n---\n\nReview.\n",
		},
	}
	for _, tc := range cases {
		_, err := installer(t, tc.harness).Native("reviewer", &agent.VariantFile{
			Harness: tc.harness, Path: tc.path, Raw: []byte(tc.content),
		})
		if err == nil {
			t.Errorf("%s: want an invalid variant", tc.name)
			continue
		}
		if strings.Contains(tc.name, "sandbox_mode") {
			// A read-only sandbox Codex would not enforce (ADR 0012).
			requireIncompatible(t, err)
		} else {
			requireInvalid(t, err)
		}
		if strings.Contains(tc.name, "sandbox_mode") &&
			!strings.Contains(err.Error(), "sandbox_mode") {
			t.Errorf("%s: want the sandbox_mode refusal, got %v", tc.name, err)
		}
	}
}
