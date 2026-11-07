package harness_test

import (
	"strings"
	"testing"

	"github.com/agnthub-sh/agnthub/agent"
)

// https://github.com/shanraisshan/claude-code-best-practice/blob/722071c519f7/.claude/agents/time-agent.md
// (minimized). Claude Code reads a rule whose specifier is exactly "*" as the
// whole tool, so WebFetch(*) grants web fetch like WebFetch.
const timeAgent = `---
name: reviewer
description: Use this agent to display the current time in Pakistan Standard Time (PKT, UTC+5).
allowedTools:
  - "Bash(*)"
  - "Read"
  - "Write"
  - "Edit"
  - "WebFetch(*)"
  - "WebSearch(*)"
model: haiku
---
Show the time.
`

func TestWholeToolWildcardIsTheTool(t *testing.T) {
	path := ".claude/agents/reviewer.md"
	for _, target := range []agent.HarnessID{
		agent.OpenCode, agent.Codex, agent.Cursor, agent.OMP,
	} {
		artifact, err := generateFrom(t, agent.Claude, target, path, timeAgent)
		if err != nil {
			t.Errorf("%s: want WebFetch(*) read as WebFetch, got %v", target, err)
			continue
		}
		if len(artifact.Reduced) != 0 {
			t.Errorf("%s: nothing is reduced, got %v", target, artifact.ReducedNames())
		}
	}
	plain := strings.NewReplacer("Bash(*)", "Bash", "WebFetch(*)", "WebFetch",
		"WebSearch(*)", "WebSearch").Replace(timeAgent)
	for _, target := range nonClaude {
		wild, wildErr := generateFrom(t, agent.Claude, target, path, timeAgent)
		bare, bareErr := generateFrom(t, agent.Claude, target, path, plain)
		if (wildErr == nil) != (bareErr == nil) || string(wild.Content) != string(bare.Content) {
			t.Errorf("%s: Tool(*) must convert as Tool\n%v %s\n%v %s", target, wildErr,
				wild.Content, bareErr, bare.Content)
		}
	}
}

// A real scope stays a scope: a WebFetch domain is still refused, and a shell
// command list keeps its handling (OpenCode carries it natively).
func TestScopedRulesKeepTheirHandling(t *testing.T) {
	path := ".claude/agents/reviewer.md"
	domain := "---\nname: reviewer\ndescription: Reads docs.\n" +
		"tools: Read, WebFetch(domain:docs.example.com)\n---\nRead.\n"
	for _, target := range nonClaude {
		_, err := generateFrom(t, agent.Claude, target, path, domain)
		requireIncompatible(t, err)
	}
	git := "---\nname: reviewer\ndescription: Reviews.\ntools: Read, Edit, Bash(git *)\n" +
		"---\nReview.\n"
	artifact, err := generateFrom(t, agent.Claude, agent.OpenCode, path, git)
	if err != nil || !strings.Contains(string(artifact.Content), "git *") {
		t.Errorf("opencode: want the shell limited to git *, got %v\n%s", err, artifact.Content)
	}
	artifact, err = generateFrom(t, agent.Claude, agent.OMP, path, git)
	requireReduced(t, "omp", artifact, err, "shell")
}
