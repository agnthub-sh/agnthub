package harness_test

import (
	"strings"
	"testing"

	"github.com/agnthub-sh/agnthub/agent"
	"github.com/agnthub-sh/agnthub/harness"
)

// Allowlists from the refusal census that name tools no harness maps
// (minimized: descriptions and instructions shortened).
var unknownToolAgents = map[string]string{
	// Other harnesses' names, lowercase, which Claude Code matches exactly and so
	// grants nothing for:
	// https://github.com/microsoft/power-platform-skills/blob/8821feed9140/plugins/model-apps/agents/genpage-edit-planner.md
	"genpage-edit-planner": `---
name: reviewer
description: >-
  Plans edits to an existing generative page.
color: cyan
tools:
  - Read
  - Write
  - Glob
  - TaskCreate
  - TaskUpdate
  - TaskList
  - read
  - edit
  - search
  - todo
---

Plan the edit.
`,
	// "All tools", which Claude Code reads as the two unknown names All and tools:
	// https://github.com/taiyousan15/nansentop50/blob/d3ba53582e6f/.claude/agents/ait42-cicd-manager.md
	"ait42-cicd-manager": `---
name: reviewer
description: "Elite CI/CD pipeline architect"
tools: All tools
model: sonnet
---

Design the pipeline.
`,
	// Claude Code's MCP resource tools, which no other harness has:
	// https://github.com/lengo0951/pms/blob/5b1f1c3b095c/.claude/agents/project-manager.md
	"project-manager": `---
name: reviewer
description: 'Use this agent when you need comprehensive project oversight and coordination.'
tools: Glob, Grep, LS, Read, Edit, MultiEdit, Write, NotebookEdit, WebFetch, TaskCreate, TaskGet, TaskUpdate, TaskList, WebSearch, BashOutput, KillBash, ListMcpResourcesTool, ReadMcpResourceTool, SendMessage
model: haiku
---

Track the project.
`,
	// MCP server and program names instead of tool names:
	// https://github.com/lucky7slw/construction-erp/blob/7b66a1873459/.claude/agents/fullstack-developer.md
	"fullstack-developer": `---
name: reviewer
description: End-to-end feature owner with expertise across the entire stack.
tools: Read, Write, MultiEdit, Bash, Docker, database, redis, postgresql, magic, context7, playwright
---

Deliver the feature.
`,
}

// Leaving an allowlisted name out withholds it, which never widens what the
// agent may do: names with no equivalent are dropped with a note, and the
// converted agent grants no capability the source did not.
func TestUnknownAllowlistNamesAreDropped(t *testing.T) {
	claude := installer(t, agent.Claude)
	for name, source := range unknownToolAgents {
		want, err := claude.Semantics("reviewer", variant(agent.Claude, source))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(want.Tools.Unknown) == 0 {
			t.Fatalf("%s: the fixture must name unknown tools", name)
		}
		converted := 0
		for _, target := range nonClaude {
			artifact, err := generateFrom(t, agent.Claude, target, ".claude/agents/reviewer.md",
				source)
			if err != nil {
				if strings.Contains(err.Error(), "has no "+string(target)+" equivalent") {
					t.Errorf("%s → %s: refused for an unknown name: %v", name, target, err)
				}
				continue
			}
			converted++
			note := "omitted tools entries " + strings.Join(want.Tools.Unknown, ", ") + ": no " +
				string(target) + " equivalent"
			if !hasNote(artifact.Notes, note) {
				t.Errorf("%s → %s: want %q, got %q", name, target, note, artifact.Notes)
			}
			got, err := installer(t, target).Semantics("reviewer", &agent.VariantFile{
				Harness: target, Path: artifact.FileName, Raw: artifact.Content,
			})
			if err != nil {
				t.Fatalf("%s → %s: %v", name, target, err)
			}
			boundaries := harness.CapRead | harness.CapSearch | harness.CapEdit |
				harness.CapShell | harness.CapWeb | harness.CapDelegate
			// Codex and Cursor cannot withhold reads, searches, web tools or
			// delegation from an agent with a shell and note what they cannot
			// withhold (see TestClaudeAgentsConvertToEveryHarnessTheirToolsAllow);
			// omp's read tool also fetches URLs.
			widened := got.Tools.Effective() &^ want.Tools.Effective() & boundaries
			switch target {
			case agent.Codex, agent.Cursor:
				widened &= harness.CapEdit | harness.CapShell
			case agent.OMP:
				widened &^= harness.CapWeb
			}
			if widened != 0 {
				t.Errorf("%s → %s: grants %s the source does not:\n%s", name, target, widened,
					artifact.Content)
			}
		}
		if converted == 0 {
			t.Errorf("%s: converts nowhere", name)
		}
	}
}

// Claude Code 2.1.309 grants nothing for "All tools" (its model request lists
// no tools), so the converted agent has no tools either.
func TestAllToolsGrantsNothing(t *testing.T) {
	sem, err := installer(t, agent.Claude).Semantics("reviewer",
		variant(agent.Claude, unknownToolAgents["ait42-cicd-manager"]))
	if err != nil {
		t.Fatal(err)
	}
	if !sem.Tools.Restricted || sem.Tools.Effective() != 0 {
		t.Errorf("want an allowlist granting nothing, got %+v", sem.Tools)
	}
}

// A denylist entry with no equivalent still refuses: other harnesses could not
// withhold it.
func TestUnknownDenylistNamesStayRefused(t *testing.T) {
	source := strings.Replace(claudeAgent("Read", "Bash"), "color: blue\n",
		"disallowedTools: [Read, mcp__*__fetch, ListMcpResourcesTool]\n", 1)
	for _, target := range nonClaude {
		_, err := generateFrom(t, agent.Claude, target, ".claude/agents/reviewer.md", source)
		if err == nil {
			t.Errorf("%s: want a refusal", target)
			continue
		}
		requireIncompatible(t, err)
	}
}
