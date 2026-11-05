package harness_test

import (
	"strings"
	"testing"

	"github.com/agnthub-sh/agnthub/agent"
	"github.com/agnthub-sh/agnthub/harness"
)

// Agents from the refusal census, minimized (instructions shortened): a widely
// copied template declares pre and post shell snippets as hooks, events Claude
// Code does not have.
var ignoredHookAgents = map[string]string{
	// https://github.com/DevOpsMadDog/Aldeci.AI/blob/f913dffe9abc/.claude/agents/consensus/quorum-manager.md
	"quorum-manager": `---
name: quorum-manager
type: coordinator
color: "#673AB7"
description: Implements dynamic quorum adjustment and intelligent membership management
capabilities:
  - dynamic_quorum_calculation
  - membership_management
priority: high
hooks:
  pre: |
    echo "🎯 Quorum Manager adjusting: $TASK"
    if [[ "$TASK" == *"quorum"* ]]; then
      echo "📡 Analyzing network topology and node health"
    fi
  post: |
    echo "⚖️  Quorum adjustment complete"
---

# Quorum Manager

Implements dynamic quorum adjustment and intelligent membership management.
`,
	// https://github.com/summarybotng/summarybot-ng/blob/fd332161d4e7/.claude/agents/templates/memory-coordinator.md
	"memory-coordinator": `---
name: memory-coordinator
type: coordination
color: green
description: Manage persistent memory across sessions and facilitate cross-agent memory sharing
capabilities:
  - memory-management
priority: high
hooks:
  pre: |
    echo "🧠 Memory Coordination Specialist initializing"
  post: |
    echo "✅ Memory operations completed successfully"
---

# Memory Coordination Specialist Agent

Manage the distributed memory system.
`,
	// https://github.com/softmg/product-tracker/blob/86e4fb3fd60c/.claude/agents/github/github-modes.md
	"github-modes": `---
name: github-modes
description: Comprehensive GitHub integration modes for workflow orchestration, PR management, and repository coordination with batch optimization
tools: mcp__claude-flow__swarm_init, mcp__claude-flow__agent_spawn, Bash, TodoWrite, Read, Write
color: purple
type: development
priority: medium
hooks:
  pre: |
    echo "Starting github-modes..."
    gh auth status || (echo "GitHub CLI authentication required" && exit 1)
  post: |
    echo "Completed github-modes"
---

# GitHub Integration Modes

Coordinate GitHub workflows.
`,
}

// Claude Code 2.1.309 logs "hooks.pre: unknown hook event; entry ignored" and
// loads these agents without hooks, so every other Harness converts them as if
// hooks were absent, with a note; whatever else refuses them still does.
func TestClaudeHooksWithOnlyIgnoredEventsConvert(t *testing.T) {
	for id, source := range ignoredHookAgents {
		for _, target := range nonClaude {
			artifact, err := generateFrom(t, agent.Claude, target, ".claude/agents/reviewer.md",
				strings.Replace(source, "name: "+id, "name: reviewer", 1))
			if err != nil {
				if strings.Contains(err.Error(), "hooks") {
					t.Errorf("%s → %s: refused for its ignored hooks: %v", id, target, err)
				}
				continue
			}
			if !hasNote(
				artifact.Notes,
				"omitted hooks: Claude Code ignores these events (post, pre)",
			) {
				t.Errorf("%s → %s: want the omission noted, got %q", id, target, artifact.Notes)
			}
		}
	}
}

// Claude Code matches event names exactly, so a misspelled or lowercased event
// is ignored too, unless its value holds PreToolUse or PermissionRequest hooks:
// then Claude Code refuses to load the agent at all.
func TestClaudeHookEventsAreReadAsClaudeCodeReadsThem(t *testing.T) {
	ignored := []string{
		"hooks:\n  pre: echo\n  post:\n",
		"hooks:\n  preToolUse:\n    - matcher: Bash\n      hooks:\n        - type: command\n" +
			"          command: echo\n",
		"hooks:\n  notification:\n    - type: command\n      command: echo\n",
		"hooks:\n  post_edit:\n    command: echo\n    timeout: 5\n",
	}
	refused := []string{
		// Real events, alone or beside ignored ones.
		"hooks:\n  Stop:\n    - hooks:\n        - type: command\n          command: echo\n",
		"hooks:\n  pre: echo\n  PreToolUse:\n    - matcher: Bash\n      hooks:\n" +
			"        - type: command\n          command: echo\n",
		// Guard hooks under an unknown key keep Claude Code from loading the agent.
		"hooks:\n  pre:\n    PreToolUse:\n      - matcher: Bash\n        hooks:\n" +
			"          - type: command\n            command: echo\n",
		"hooks:\n  pre:\n    hooks:\n      - type: command\n        command: echo\n",
		// A matcher where the event map belongs, or no map at all.
		"hooks:\n  matcher: Bash\n  hooks:\n    - type: command\n      command: echo\n",
		"hooks: echo\n",
		"hooks:\n  - echo\n",
	}
	for _, field := range ignored {
		for _, target := range nonClaude {
			artifact, err := generateFrom(t, agent.Claude, target, ".claude/agents/reviewer.md",
				withField(field))
			if err != nil {
				t.Errorf("%q → %s: %v", field, target, err)
				continue
			}
			if !hasNote(artifact.Notes, "omitted hooks: Claude Code ignores these events") {
				t.Errorf("%q → %s: want the omission noted, got %q", field, target, artifact.Notes)
			}
		}
	}
	for _, field := range refused {
		for _, target := range nonClaude {
			_, err := generateFrom(t, agent.Claude, target, ".claude/agents/reviewer.md",
				withField(field))
			if err == nil {
				t.Errorf("%q → %s: want a refusal", field, target)
				continue
			}
			requireIncompatible(t, err)
		}
	}
}

// A real hook still refuses conversion, whatever else the agent declares.
// https://github.com/benedito-dev-full/Scrumbam-Backend-V2/blob/ec41fb138578/.claude/agents/documenter.md
// (minimized).
func TestClaudeRealHooksStayRefused(t *testing.T) {
	const documenter = `---
name: reviewer
description: |
  Technical writer and documentation guardian for Scrumban-Backend-V2.
model: haiku
tools:
  - Read
  - Write
  - Edit
  - Bash
  - Glob
  - Grep
disallowedTools:
  - Task
  - WebFetch
  - WebSearch
permissionMode: acceptEdits
hooks:
  Stop:
    - type: command
      command: ./.claude/scripts/validate-documentation.sh
      timeout: 60
color: purple
---

# DOCUMENTER AGENT

Keep the documentation current.
`
	for _, target := range nonClaude {
		_, err := generateFrom(t, agent.Claude, target, ".claude/agents/reviewer.md", documenter)
		if err == nil || !strings.Contains(err.Error(), "hooks") {
			t.Errorf("%s: want a refusal naming hooks, got %v", target, err)
		}
	}
}

// Claude Code installs its own file as written; hooks it ignores run nothing,
// so the plan does not say they run.
func TestClaudeNativeKeepsIgnoredHooksVerbatim(t *testing.T) {
	for id, source := range ignoredHookAgents {
		artifact, err := installer(t, agent.Claude).Native(id, &agent.VariantFile{
			Harness: agent.Claude, Path: ".claude/agents/" + id + ".md", Raw: []byte(source),
		})
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if string(artifact.Content) != source {
			t.Errorf("%s: want the file as written", id)
		}
		if hasNote(artifact.Notes, "declares hooks") {
			t.Errorf("%s: ignored hooks run nothing, got %q", id, artifact.Notes)
		}
	}
	real := withField("hooks:\n  Stop:\n    - hooks:\n        - type: command\n" +
		"          command: echo\n")
	artifact, err := installer(t, agent.Claude).Native("reviewer", variant(agent.Claude, real))
	if err != nil {
		t.Fatal(err)
	}
	if !hasNote(artifact.Notes, "declares hooks, which runs when the agent is used") {
		t.Errorf("a real hook must be flagged, got %q", artifact.Notes)
	}
}

// The ignored events are what the e2e suite looks for in Claude Code's log.
func TestClaudeIgnoredHookEventsAreListed(t *testing.T) {
	events, ok := harness.ClaudeIgnoredHookEvents(map[string]any{"pre": "echo", "post": nil})
	if !ok || strings.Join(events, ",") != "post,pre" {
		t.Errorf("got %v %t", events, ok)
	}
	if _, ok := harness.ClaudeIgnoredHookEvents(map[string]any{"Stop": []any{}}); ok {
		t.Error("Stop is a Claude Code event")
	}
}
