package harness_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/agnthub-sh/agnthub/agent"
	"github.com/agnthub-sh/agnthub/harness"
)

// interviewPrep is github/awesome-copilot's interview-prep agent, shortened: a
// portable core in Copilot's <id>.agent.md form whose tools are Copilot
// aliases and a VS Code namespaced name.
// https://github.com/github/awesome-copilot/blob/797337f33f73/agents/interview-prep.agent.md
const interviewPrep = `---
description: "Technical interview coach for software engineers."
name: interview-prep
tools: ["read", "search", "web/fetch"]
---

# Technical Interview Coach

Run mock interviews.
`

func copilotCore(t *testing.T, path, content string) *agent.Document {
	t.Helper()
	doc, err := agent.ParseMarkdown(path, []byte(content))
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

// frontmatterText is a generated Markdown agent's frontmatter, as written.
func frontmatterText(t *testing.T, artifact harness.Artifact) string {
	t.Helper()
	text := string(artifact.Content)
	end := strings.Index(text, "\n---\n")
	if !strings.HasPrefix(text, "---\n") || end < 0 {
		t.Fatalf("no frontmatter:\n%s", text)
	}
	return text[:end]
}

// An <id>.agent.md core grants on every harness what Copilot CLI grants it:
// read, search and web fetch, and nothing else.
func TestCopilotAgentFileCoreGrantsItsToolsEverywhere(t *testing.T) {
	want := map[agent.HarnessID]struct{ has, lacks []string }{
		agent.Claude: {
			has:   []string{"tools: Read, Grep, Glob, WebFetch"},
			lacks: []string{"Edit", "Bash", "WebSearch"},
		},
		agent.OpenCode: {
			has:   []string{"'*': deny", "glob: allow", "grep: allow", "webfetch: allow"},
			lacks: []string{"edit:", "bash:", "websearch:"},
		},
		agent.Copilot: {
			has:   []string{"  - read\n", "  - search\n", "  - web\n"},
			lacks: []string{"edit", "execute"},
		},
		agent.Cursor: {has: []string{"readonly: true"}},
		agent.Gemini: {
			has:   []string{"read_file", "grep_search", "web_fetch"},
			lacks: []string{"write_file", "replace", "run_shell_command", "google_web_search"},
		},
		agent.OMP: {
			has:   []string{"  - read\n", "  - grep\n"},
			lacks: []string{"edit", "bash"},
		},
	}
	for _, target := range agent.AllHarnesses {
		artifact, err := convert(installer(t, target), "interview-prep",
			copilotCore(t, "agents/interview-prep.agent.md", interviewPrep))
		if target == agent.Codex {
			// Codex agents cannot withhold edits (ADR 0012).
			requireIncompatible(t, err)
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", target, err)
		}
		head := frontmatterText(t, artifact)
		for _, fragment := range want[target].has {
			if !strings.Contains(head+"\n", fragment) {
				t.Errorf("%s: want %q in\n%s", target, fragment, head)
			}
		}
		for _, fragment := range want[target].lacks {
			if strings.Contains(head, fragment) {
				t.Errorf("%s: must not grant %q:\n%s", target, fragment, head)
			}
		}
		if hasNote(artifact.Notes, "omitted tools entr") {
			t.Errorf("%s: every tools entry has an equivalent, got %q", target, artifact.Notes)
		}
	}
}

// Only Copilot's own file form changes the reading: the same frontmatter in
// agents/<id>.md is a Claude Code agent, whose lowercase names grant nothing.
func TestOnlyAgentFileCoresReadCopilotToolNames(t *testing.T) {
	_, err := convert(installer(t, agent.Claude), "interview-prep",
		copilotCore(t, "agents/interview-prep.md", interviewPrep))
	requireIncompatible(t, err)
}

// An <id>.agent.md file declaring a field only Claude Code defines was
// written for Claude Code and keeps that reading.
func TestClaudeShapedAgentFileCoreKeepsTheClaudeReading(t *testing.T) {
	core := copilotCore(t, "agents/x.agent.md", "---\ndescription: d\n"+
		"tools: Read, Grep\npermissionMode: plan\nmodel: sonnet\n---\nbody\n")
	artifact, err := convert(installer(t, agent.Claude), "x", core)
	if err != nil || !strings.Contains(string(artifact.Content), "permissionMode: plan\n") ||
		!strings.Contains(string(artifact.Content), "model: sonnet\n") {
		t.Fatalf("Claude Code keeps its own fields: %v\n%s", err, artifact.Content)
	}
}

// An allowlist that grants nothing stays an allowlist, never all tools: only
// the skill tool Copilot CLI always offers, and an unreadable one refuses.
func TestCopilotAgentFileCoreGrantingNothingStaysRestricted(t *testing.T) {
	for _, tools := range []string{"tools: []", "tools: [problems, githubRepo]"} {
		core := copilotCore(t, "agents/x.agent.md",
			"---\ndescription: d\n"+tools+"\n---\nbody\n")
		artifact, err := convert(installer(t, agent.Claude), "x", core)
		if err != nil || !slices.Equal(claudeToolList(t, artifact), []string{"Skill"}) {
			t.Errorf("%s: Claude Code gets only Skill: %v\n%s", tools, err, artifact.Content)
		}
		for _, target := range []agent.HarnessID{agent.Codex, agent.Cursor} {
			_, err := convert(installer(t, target), "x", core)
			requireIncompatible(t, err)
		}
		artifact, err = convert(installer(t, agent.OMP), "x", core)
		if err != nil || strings.Contains(string(artifact.Content), "  - ") {
			t.Errorf("%s: omp gets no tools: %v\n%s", tools, err, artifact.Content)
		}
	}
	null := copilotCore(t, "agents/x.agent.md", "---\ndescription: d\ntools:\n---\nbody\n")
	for _, target := range agent.AllHarnesses {
		_, err := convert(installer(t, target), "x", null)
		requireIncompatible(t, err)
	}
}

// Entries Copilot CLI grants nothing for are reported, one note each.
func TestCopilotToolEntriesWithoutEquivalentAreReported(t *testing.T) {
	core := copilotCore(t, "agents/x.agent.md", "---\ndescription: d\n"+
		"tools: [read, problems, read/problems, github/*, WebSearch, my-server-tool]\n---\nbody\n")
	artifact, err := convert(installer(t, agent.Claude), "x", core)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range []string{
		"problems:", "read/problems:", "github/*:", "WebSearch:",
		"my-server-tool",
	} {
		if !hasNote(artifact.Notes, entry) {
			t.Errorf("want a note for %s, got %q", entry, artifact.Notes)
		}
	}
	if !strings.Contains(string(artifact.Content), "tools: Read, Skill\n") {
		t.Errorf("only read is granted:\n%s", artifact.Content)
	}
}

// Copilot CLI reads a namespaced entry by its last segment (runtime-verified
// with Copilot CLI 1.0.105), in authored .github/agents files too.
func TestCopilotNamespacedToolsGrantTheirLastSegment(t *testing.T) {
	const raw = "---\ndescription: d\ntools: [edit/editFiles, execute/runInTerminal, " +
		"read/readFile, search/codebase, agent/runSubagent, web/fetch, todo]\n---\nbody\n"
	artifact, err := generateFrom(t, agent.Copilot, agent.Claude,
		".github/agents/reviewer.agent.md", raw)
	if err != nil {
		t.Fatal(err)
	}
	tools := claudeToolList(t, artifact)
	for _, tool := range []string{
		"Read", "Grep", "Glob", "Edit", "Write", "Bash", "WebFetch",
		"Agent",
	} {
		if !slices.Contains(tools, tool) {
			t.Errorf("want %s in %q", tool, tools)
		}
	}
	if slices.Contains(tools, "WebSearch") {
		t.Errorf("web/fetch is no web search: %q", tools)
	}
}

// claudeToolList is a generated Claude Code agent's tools line.
func claudeToolList(t *testing.T, artifact harness.Artifact) []string {
	t.Helper()
	for line := range strings.SplitSeq(string(artifact.Content), "\n") {
		if list, ok := strings.CutPrefix(line, "tools: "); ok {
			return strings.Split(list, ", ")
		}
	}
	t.Fatalf("no tools line:\n%s", artifact.Content)
	return nil
}

// VS Code's model list is not a restriction: noted, never a refusal. A
// string model stays on Copilot CLI, the core's own harness.
func TestCopilotAgentFileCoreModels(t *testing.T) {
	list := copilotCore(t, "agents/x.agent.md",
		"---\ndescription: d\nmodel: [GPT-4.1, Claude Sonnet 4.6]\n---\nbody\n")
	if _, err := convert(installer(t, agent.Claude), "x", list); err != nil {
		t.Fatalf("a model list must not refuse: %v", err)
	}
	single := copilotCore(t, "agents/x.agent.md",
		"---\ndescription: d\nmodel: GPT-4.1\n---\nbody\n")
	artifact, err := convert(installer(t, agent.Copilot), "x", single)
	if err != nil || !strings.Contains(string(artifact.Content), "model: GPT-4.1\n") {
		t.Fatalf("Copilot CLI keeps the model: %v\n%s", err, artifact.Content)
	}
	artifact, err = convert(installer(t, agent.Claude), "x", single)
	if err != nil || strings.Contains(string(artifact.Content), "model:") ||
		!hasNote(artifact.Notes, "specific to copilot") {
		t.Fatalf("other harnesses note the model: %v %q\n%s", err, artifact.Notes,
			artifact.Content)
	}
}
