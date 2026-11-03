package harness_test

import (
	"strings"
	"testing"

	"github.com/agnthub-sh/agnthub/agent"
	"github.com/agnthub-sh/agnthub/harness"
)

// claudeWith is claudeAgent with extra frontmatter lines.
func claudeWith(extra string, tools ...string) string {
	return strings.Replace(claudeAgent(tools...), "color: blue\n", "color: blue\n"+extra, 1)
}

// body returns what the generated artifact gives the agent as instructions.
func body(t *testing.T, target agent.HarnessID, artifact harness.Artifact) string {
	t.Helper()
	sem, err := installer(t, target).Semantics("reviewer", &agent.VariantFile{
		Harness: target, Path: artifact.FileName, Raw: artifact.Content,
	})
	if err != nil {
		t.Fatalf("%s: generated file does not parse: %v\n%s", target, err, artifact.Content)
	}
	return sem.Instructions
}

// boundaries are the capabilities that change what an agent can affect.
const boundaries = harness.CapRead | harness.CapSearch | harness.CapEdit |
	harness.CapShell | harness.CapDelegate

// requireNotWidened fails when the generated agent grants a boundary
// capability the source agent withheld.
func requireNotWidened(
	t *testing.T,
	target agent.HarnessID,
	source string,
	artifact harness.Artifact,
) {
	t.Helper()
	want, err := installer(t, agent.Claude).Semantics("reviewer", variant(agent.Claude, source))
	if err != nil {
		t.Fatal(err)
	}
	got, err := installer(t, target).Semantics("reviewer", &agent.VariantFile{
		Harness: target, Path: artifact.FileName, Raw: artifact.Content,
	})
	if err != nil {
		t.Fatal(err)
	}
	if widened := got.Tools.Effective() &^ want.Tools.Effective() & boundaries; widened != 0 &&
		target != agent.Codex && target != agent.Cursor {
		t.Errorf("%s: widened %s:\n%s", target, widened, artifact.Content)
	}
}

func TestPersistentMemoryIsEmulatedWhereTheAgentCanWriteItsNotes(t *testing.T) {
	writer := []string{"Read", "Grep", "Glob", "Edit", "Write", "Bash"}
	reader := []string{"Read", "Grep", "Glob"}
	cases := []struct {
		name  string
		scope string
		tools []string
		// dirs is the memory folder each target's instructions name; a target
		// missing from it installs without memory.
		dirs map[agent.HarnessID]string
	}{
		{
			name: "project scope with edits", scope: "project", tools: writer,
			dirs: map[agent.HarnessID]string{
				agent.OpenCode: ".claude/agent-memory/reviewer/",
				agent.Codex:    ".claude/agent-memory/reviewer/",
				agent.Copilot:  ".claude/agent-memory/reviewer/",
				agent.Cursor:   ".claude/agent-memory/reviewer/",
				agent.Gemini:   ".claude/agent-memory/reviewer/",
				agent.OMP:      ".claude/agent-memory/reviewer/",
			},
		},
		{
			name: "local scope with edits", scope: "local", tools: writer,
			dirs: map[agent.HarnessID]string{
				agent.OpenCode: ".claude/agent-memory-local/reviewer/",
				agent.Codex:    ".claude/agent-memory-local/reviewer/",
				agent.Copilot:  ".claude/agent-memory-local/reviewer/",
				agent.Cursor:   ".claude/agent-memory-local/reviewer/",
				agent.Gemini:   ".claude/agent-memory-local/reviewer/",
				agent.OMP:      ".claude/agent-memory-local/reviewer/",
			},
		},
		{
			// Codex, Copilot CLI and Gemini CLI agents cannot write outside the
			// project without approval.
			name: "user scope with edits", scope: "user", tools: writer,
			dirs: map[agent.HarnessID]string{
				agent.OpenCode: "~/.claude/agent-memory/reviewer/",
				agent.Cursor:   "~/.cursor/agent-memory/reviewer/",
				agent.OMP:      "~/.claude/agent-memory/reviewer/",
			},
		},
		{name: "project scope, read-only", scope: "project", tools: reader},
		{name: "inherited tools", scope: "user", dirs: map[agent.HarnessID]string{
			agent.OpenCode: "~/.claude/agent-memory/reviewer/",
			agent.Cursor:   "~/.cursor/agent-memory/reviewer/",
			agent.OMP:      "~/.claude/agent-memory/reviewer/",
		}},
	}
	for _, tc := range cases {
		source := claudeWith("memory: "+tc.scope+"\n", tc.tools...)
		for _, target := range nonClaude {
			artifact, err := generateFrom(t, agent.Claude, target, ".claude/agents/reviewer.md",
				source)
			if target == agent.Codex && tc.tools != nil && len(tc.tools) == len(reader) {
				requireIncompatible(t, err) // withholds edits, as without memory
				continue
			}
			if err != nil {
				t.Errorf("%s → %s: memory must not refuse conversion: %v", tc.name, target, err)
				continue
			}
			instructions := body(t, target, artifact)
			dir, emulated := tc.dirs[target]
			switch {
			case emulated && !strings.Contains(instructions, dir+"MEMORY.md"):
				t.Errorf("%s → %s: want notes in %s:\n%s", tc.name, target, dir, instructions)
			case !emulated && strings.Contains(instructions, "MEMORY.md"):
				t.Errorf("%s → %s: memory emulated without a writable folder:\n%s",
					tc.name, target, instructions)
			}
			if !strings.HasPrefix(instructions, "You review Laravel code.") {
				t.Errorf("%s → %s: the agent's own instructions must come first:\n%s",
					tc.name, target, instructions)
			}
			if !hasNote(artifact.Notes, "persistent memory") {
				t.Errorf("%s → %s: memory outcome not noted: %v", tc.name, target, artifact.Notes)
			}
			requireNotWidened(t, target, source, artifact)
		}
	}
}

func TestOpenCodeAllowlistsLetTheAgentWriteOnlyItsUserMemory(t *testing.T) {
	source := claudeWith("memory: user\n", "Read", "Grep", "Glob", "Edit", "Write", "Bash")
	artifact, err := generateFrom(t, agent.Claude, agent.OpenCode, ".claude/agents/reviewer.md",
		source)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := agent.ParseMarkdown(artifact.FileName, artifact.Content)
	if err != nil {
		t.Fatal(err)
	}
	permission, _ := doc.Frontmatter["permission"].(map[string]any)
	external, _ := permission["external_directory"].(map[string]any)
	if permission["*"] != "deny" || len(external) != 1 ||
		external["~/.claude/agent-memory/reviewer/*"] != "allow" {
		t.Fatalf("want only the memory folder allowed outside the project:\n%s", artifact.Content)
	}
}

func TestMemoryWithAnUnknownScopeIsIgnoredLikeClaudeCode(t *testing.T) {
	source := claudeWith("memory: team\n", "Read", "Edit", "Write", "Bash")
	for _, target := range nonClaude {
		artifact, err := generateFrom(t, agent.Claude, target, ".claude/agents/reviewer.md",
			source)
		if err != nil {
			t.Fatalf("%s: %v", target, err)
		}
		if strings.Contains(body(t, target, artifact), "MEMORY.md") {
			t.Errorf("%s: emulated memory Claude Code would not give the agent", target)
		}
	}
}

func TestPreloadedSkillsMapNativelyOrBecomeInstructions(t *testing.T) {
	source := claudeWith("skills: [bug-report, release-checklist]\n",
		"Read", "Grep", "Glob", "Edit", "Write", "Bash")
	native := map[agent.HarnessID]string{agent.Copilot: "skills", agent.OMP: "autoload-skills"}
	for _, target := range nonClaude {
		artifact, err := generateFrom(t, agent.Claude, target, ".claude/agents/reviewer.md",
			source)
		if err != nil {
			t.Errorf("%s: preloaded skills must not refuse conversion: %v", target, err)
			continue
		}
		instructions := body(t, target, artifact)
		named := strings.Contains(instructions, "`bug-report`") &&
			strings.Contains(instructions, "`release-checklist`")
		if key, ok := native[target]; ok {
			doc, err := agent.ParseMarkdown(artifact.FileName, artifact.Content)
			if err != nil {
				t.Fatal(err)
			}
			list, _ := doc.Frontmatter[key].([]any)
			if len(list) != 2 || list[0] != "bug-report" || list[1] != "release-checklist" ||
				named {
				t.Errorf("%s: want %s: [bug-report, release-checklist] and unchanged "+
					"instructions:\n%s", target, key, artifact.Content)
			}
		} else if !named {
			t.Errorf("%s: instructions must name the skills to load:\n%s", target, instructions)
		}
		if !hasNote(artifact.Notes, "agnthub does not install skills") {
			t.Errorf("%s: the note must say the skills are not installed: %v", target,
				artifact.Notes)
		}
		requireNotWidened(t, target, source, artifact)
	}
}

func TestPreloadedSkillsGrantOnlyTheSkillTool(t *testing.T) {
	source := claudeWith("skills: [bug-report]\n", "Read", "Grep", "Glob")
	for target, want := range map[agent.HarnessID]string{
		agent.OpenCode: "skill", agent.Gemini: "activate_skill",
	} {
		artifact, err := generateFrom(t, agent.Claude, target, ".claude/agents/reviewer.md",
			source)
		if err != nil {
			t.Fatal(err)
		}
		got, err := installer(t, target).Semantics("reviewer", &agent.VariantFile{
			Harness: target, Path: artifact.FileName, Raw: artifact.Content,
		})
		if err != nil {
			t.Fatal(err)
		}
		if got.Tools.Effective() != harness.CapRead|harness.CapSearch|harness.CapSkill {
			t.Errorf("%s: want read, search and %s only, got %s:\n%s", target, want,
				got.Tools.Effective(), artifact.Content)
		}
	}
}

func TestAutoloadedOmpSkillsPreloadOnClaudeCode(t *testing.T) {
	def := &agent.Definition{
		ID: "reviewer",
		Variants: map[agent.HarnessID]*agent.VariantFile{agent.OMP: {
			Harness: agent.OMP, Path: ".omp/agents/reviewer.md",
			Raw: []byte("---\nname: reviewer\ndescription: Reviews code\n" +
				"autoload-skills: [bug-report]\n---\nReview it.\n"),
		}},
	}
	artifact, _, _, err := harness.Default().Generate(installer(t, agent.Claude), def)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := agent.ParseMarkdown(artifact.FileName, artifact.Content)
	if err != nil {
		t.Fatal(err)
	}
	if list, _ := doc.Frontmatter["skills"].([]any); len(list) != 1 || list[0] != "bug-report" ||
		doc.Body != "Review it.\n" {
		t.Fatalf("want skills: [bug-report] and the body unchanged:\n%s", artifact.Content)
	}
}

func TestKeysClaudeCodeIgnoresAreOmittedWithANote(t *testing.T) {
	source := claudeWith("capabilities: [review]\ncategory: quality\nprompt: Review it.\n",
		"Read", "Grep", "Glob", "Edit", "Write", "Bash")
	for _, target := range nonClaude {
		artifact, err := generateFrom(t, agent.Claude, target, ".claude/agents/reviewer.md",
			source)
		if err != nil {
			t.Errorf("%s: a key Claude Code ignores must not refuse conversion: %v", target, err)
			continue
		}
		for _, key := range []string{"capabilities", "category", "prompt"} {
			if !hasNote(artifact.Notes, "omitted "+key+": Claude Code ignores it") {
				t.Errorf("%s: %s not noted: %v", target, key, artifact.Notes)
			}
		}
		requireNotWidened(t, target, source, artifact)
	}
}

func TestIgnoredKeysThatReadAsRestrictionsStillRefuse(t *testing.T) {
	for _, field := range []string{
		"allowed-tools: Read, Grep\n",
		"allowedTools: [Read]\n",
		"readonly: true\n",
		"read_only: true\n",
		"permissions:\n  edit: deny\n",
		"disallowed-tools: Bash\n",
		"Tools: Read\n",
		"max_turns: 3\n",
	} {
		source := claudeWith(field)
		for _, target := range nonClaude {
			_, err := generateFrom(t, agent.Claude, target, ".claude/agents/reviewer.md", source)
			requireIncompatible(t, err)
		}
	}
	if !harness.ClaudeIgnores("allowed-tools") || harness.ClaudeIgnores("tools") {
		t.Fatal("ClaudeIgnores must follow Claude Code's recognized fields")
	}
}
