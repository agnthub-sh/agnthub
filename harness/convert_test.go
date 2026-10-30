package harness_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/heynemann/agnthub/agent"
	"github.com/heynemann/agnthub/harness"
)

// convert generates an artifact from a portable core, as the planner does.
func convert(
	installer harness.Installer,
	id string,
	doc *agent.Document,
) (harness.Artifact, error) {
	artifact, _, _, err := harness.Default().Generate(
		installer,
		&agent.Definition{ID: id, Portable: doc},
	)
	return artifact, err
}

// generateFrom converts one authored Variant of source for target.
func generateFrom(
	t *testing.T,
	source, target agent.HarnessID,
	path, content string,
) (harness.Artifact, error) {
	t.Helper()
	def := &agent.Definition{
		ID: "reviewer",
		Variants: map[agent.HarnessID]*agent.VariantFile{
			source: {Harness: source, Path: path, Raw: []byte(content)},
		},
	}
	artifact, sourcePath, _, err := harness.Default().Generate(installer(t, target), def)
	if err == nil && sourcePath != path {
		t.Fatalf("%s: source path %q, want %q", target, sourcePath, path)
	}
	return artifact, err
}

// claudeAgent is a claude-laravel style Claude Code agent with the given tools.
func claudeAgent(tools ...string) string {
	var b strings.Builder
	b.WriteString("---\nname: reviewer\n")
	b.WriteString("description: Reviews Laravel code. Use after changes.\n")
	b.WriteString("model: sonnet\ncolor: blue\n")
	if len(tools) > 0 {
		b.WriteString("tools:\n")
		for _, tool := range tools {
			b.WriteString("  - " + tool + "\n")
		}
	}
	b.WriteString("---\nYou review Laravel code.\n")
	return b.String()
}

var nonClaude = []agent.HarnessID{
	agent.OpenCode, agent.Codex, agent.Copilot, agent.Cursor, agent.Gemini, agent.OMP,
}

func TestClaudeAgentsConvertToEveryHarnessTheirToolsAllow(t *testing.T) {
	const (
		ok     = ""
		refuse = "refuse"
	)
	cases := []struct {
		name  string
		tools []string
		want  map[agent.HarnessID]string
	}{
		{
			name:  "reviewer with shell but no edits",
			tools: []string{"Read", "Grep", "Glob", "Bash"},
			want: map[agent.HarnessID]string{
				agent.OpenCode: ok, agent.Codex: refuse, agent.Copilot: ok,
				agent.Cursor: refuse, agent.Gemini: ok, agent.OMP: ok,
			},
		},
		{
			name:  "developer with team messaging",
			tools: []string{"Read", "Glob", "Grep", "Edit", "Write", "Bash", "SendMessage"},
			want: map[agent.HarnessID]string{
				agent.OpenCode: ok, agent.Codex: ok, agent.Copilot: ok,
				agent.Cursor: ok, agent.Gemini: ok, agent.OMP: ok,
			},
		},
		{
			name:  "read-only researcher",
			tools: []string{"Read", "Grep", "Glob", "WebFetch"},
			want: map[agent.HarnessID]string{
				agent.OpenCode: ok, agent.Codex: refuse, agent.Copilot: ok,
				agent.Cursor: ok, agent.Gemini: ok, agent.OMP: ok,
			},
		},
		{
			name:  "web search",
			tools: []string{"Read", "Edit", "Write", "Bash", "WebSearch"},
			want: map[agent.HarnessID]string{
				agent.OpenCode: ok, agent.Codex: ok, agent.Copilot: refuse,
				agent.Cursor: ok, agent.Gemini: ok, agent.OMP: ok,
			},
		},
		{
			name:  "delegation",
			tools: []string{"Read", "Edit", "Write", "Bash", "Agent"},
			want: map[agent.HarnessID]string{
				agent.OpenCode: ok, agent.Codex: ok, agent.Copilot: ok,
				agent.Cursor: ok, agent.Gemini: refuse, agent.OMP: ok,
			},
		},
		{
			name:  "MCP tool",
			tools: []string{"Read", "Edit", "Write", "Bash", "mcp__context7__query-docs"},
			want: map[agent.HarnessID]string{
				agent.OpenCode: refuse, agent.Codex: refuse, agent.Copilot: refuse,
				agent.Cursor: refuse, agent.Gemini: refuse, agent.OMP: refuse,
			},
		},
		{
			name: "every tool inherited",
			want: map[agent.HarnessID]string{
				agent.OpenCode: ok, agent.Codex: ok, agent.Copilot: ok,
				agent.Cursor: ok, agent.Gemini: ok, agent.OMP: ok,
			},
		},
	}
	for _, tc := range cases {
		for _, target := range nonClaude {
			artifact, err := generateFrom(t, agent.Claude, target, ".claude/agents/reviewer.md",
				claudeAgent(tc.tools...))
			switch tc.want[target] {
			case refuse:
				var incompatible *harness.IncompatibleError
				if !errors.As(err, &incompatible) || incompatible.Reason == "" {
					t.Errorf("%s → %s: want a refusal with a reason, got %v", tc.name, target, err)
				}
			default:
				if err != nil {
					t.Errorf("%s → %s: %v", tc.name, target, err)
					continue
				}
				if !artifact.Generated || !strings.HasSuffix(artifact.Converter, "/v2") {
					t.Errorf("%s → %s: not a v2 generated artifact: %+v", tc.name, target, artifact)
				}
				if !hasNote(artifact.Notes, "harness default model") {
					t.Errorf("%s → %s: the source model must be noted, got %q",
						tc.name, target, artifact.Notes)
				}
				if strings.Contains(string(artifact.Content), "sonnet") ||
					strings.Contains(string(artifact.Content), "color") {
					t.Errorf("%s → %s: model and color must not be carried:\n%s",
						tc.name, target, artifact.Content)
				}
			}
		}
	}
}

// A generated artifact is a valid native file whose own semantics grant
// exactly what the source allowed, for every target with a tool allowlist.
func TestGeneratedToolPoliciesRoundTrip(t *testing.T) {
	cases := [][]string{
		{"Read", "Grep", "Glob", "Bash"},
		{"Read", "Glob", "Grep", "Edit", "Write", "Bash", "SendMessage"},
		{"Read", "Grep", "Glob", "WebFetch", "WebSearch"},
		nil,
	}
	exact := []agent.HarnessID{agent.OpenCode, agent.Gemini}
	for _, tools := range cases {
		source := claudeAgent(tools...)
		want, err := installer(t, agent.Claude).Semantics("reviewer",
			variant(agent.Claude, source))
		if err != nil {
			t.Fatal(err)
		}
		for _, target := range nonClaude {
			artifact, err := generateFrom(t, agent.Claude, target, ".claude/agents/reviewer.md",
				source)
			if err != nil {
				continue
			}
			back := &agent.VariantFile{
				Harness: target,
				Path:    artifact.FileName,
				Raw:     artifact.Content,
			}
			if _, err := installer(t, target).Native("reviewer", back); err != nil {
				t.Fatalf("%v → %s: generated file is invalid: %v\n%s", tools, target, err,
					artifact.Content)
			}
			got, err := installer(t, target).Semantics("reviewer", back)
			if err != nil {
				t.Fatalf("%v → %s: %v", tools, target, err)
			}
			if got.Tools.Restricted != want.Tools.Restricted && target != agent.Codex &&
				target != agent.Cursor {
				t.Errorf("%v → %s: restricted %t, want %t", tools, target,
					got.Tools.Restricted, want.Tools.Restricted)
			}
			// Generated files never grant a boundary capability the source withheld.
			const boundaries = harness.CapRead | harness.CapSearch | harness.CapEdit |
				harness.CapShell | harness.CapDelegate
			if widened := got.Tools.Effective() &^ want.Tools.Effective() & boundaries; widened != 0 &&
				target != agent.Codex &&
				target != agent.Cursor {
				t.Errorf("%v → %s: widened %s:\n%s", tools, target, widened, artifact.Content)
			}
			for _, h := range exact {
				if target == h && got.Tools.Effective() != want.Tools.Effective() {
					t.Errorf("%v → %s: grants %s, want %s", tools, target,
						got.Tools.Effective(), want.Tools.Effective())
				}
			}
		}
	}
}

func TestBehavioralFieldsRefuseConversion(t *testing.T) {
	for _, field := range []string{
		"hooks:\n  Stop:\n    - command: echo\n",
		"permissionMode: acceptEdits\n",
		"mcpServers:\n  - github\n",
		"isolation: worktree\n",
		"memory: project\n",
	} {
		source := strings.Replace(claudeAgent("Read", "Edit", "Write", "Bash"),
			"color: blue\n", "color: blue\n"+field, 1)
		for _, target := range nonClaude {
			_, err := generateFrom(t, agent.Claude, target, ".claude/agents/reviewer.md", source)
			requireIncompatible(t, err)
		}
	}
}

func TestTurnLimitsMapWhereTheHarnessHasOne(t *testing.T) {
	source := strings.Replace(claudeAgent("Read", "Edit", "Write", "Bash"),
		"color: blue\n", "maxTurns: 7\n", 1)
	for target, key := range map[agent.HarnessID]string{
		agent.OpenCode: "steps", agent.Gemini: "max_turns",
	} {
		artifact, err := generateFrom(t, agent.Claude, target, ".claude/agents/reviewer.md", source)
		if err != nil {
			t.Fatal(err)
		}
		doc, err := agent.ParseMarkdown(artifact.FileName, artifact.Content)
		if err != nil || doc.Frontmatter[key] != 7 {
			t.Fatalf("%s: want %s: 7, got %v\n%s", target, key, err, artifact.Content)
		}
	}
	for _, target := range []agent.HarnessID{agent.Codex, agent.Copilot, agent.Cursor, agent.OMP} {
		_, err := generateFrom(t, agent.Claude, target, ".claude/agents/reviewer.md", source)
		requireIncompatible(t, err)
	}
}

func TestCodexWithheldShellBecomesAFeatureSwitch(t *testing.T) {
	source := "---\nname: reviewer\ndescription: Edits without a shell\n" +
		"tools: Read, Grep, Glob, Edit, Write\n---\nEdit only.\n"
	artifact, err := generateFrom(
		t,
		agent.Claude,
		agent.Codex,
		".claude/agents/reviewer.md",
		source,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(artifact.Content), "[features]\nshell_tool = false") {
		t.Fatalf("want features.shell_tool = false:\n%s", artifact.Content)
	}
	// Cursor can only withhold shell by withholding edits too.
	_, err = generateFrom(t, agent.Claude, agent.Cursor, ".claude/agents/reviewer.md", source)
	requireIncompatible(t, err)
}

func TestReadOnlyAgentsUseCursorReadonly(t *testing.T) {
	artifact, err := generateFrom(t, agent.Claude, agent.Cursor, ".claude/agents/reviewer.md",
		claudeAgent("Read", "Grep", "Glob"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := agent.ParseMarkdown(artifact.FileName, artifact.Content)
	if err != nil || doc.Frontmatter["readonly"] != true {
		t.Fatalf("want readonly: true, got %v\n%s", err, artifact.Content)
	}
}

func TestOpenCodeAllowlistsDenyEverythingElseFirst(t *testing.T) {
	artifact, err := generateFrom(t, agent.Claude, agent.OpenCode, ".claude/agents/reviewer.md",
		claudeAgent("Read", "Grep", "Glob"))
	if err != nil {
		t.Fatal(err)
	}
	keys := agent.MappingKeys(artifact.Content, "permission")
	if len(keys) == 0 || keys[0] != "*" {
		t.Fatalf("OpenCode applies the last matching rule; \"*\" must come first:\n%s",
			artifact.Content)
	}
	doc, _ := agent.ParseMarkdown(artifact.FileName, artifact.Content)
	if doc.String("mode") != "subagent" {
		t.Fatalf("generated OpenCode agents must stay subagents:\n%s", artifact.Content)
	}
}

func TestOtherHarnessVariantsConvertToClaude(t *testing.T) {
	sources := []struct {
		harness agent.HarnessID
		path    string
		content string
		tools   string
	}{
		{
			agent.OpenCode, ".opencode/agents/reviewer.md",
			"---\ndescription: Reviews\nmode: subagent\nmodel: anthropic/claude-sonnet-4\n" +
				"permission:\n  \"*\": deny\n  read: allow\n  grep: allow\n  glob: allow\n---\nReview.\n",
			"Read, Grep, Glob",
		},
		{
			agent.Copilot, ".github/agents/reviewer.agent.md",
			"---\ndescription: Reviews\ntools: [read, search, execute]\n---\nReview.\n",
			"Read, Grep, Glob, Bash, Skill",
		},
		{
			agent.Gemini,
			".gemini/agents/reviewer.md",
			"---\nname: reviewer\ndescription: Reviews\ntools: [read_file, grep_search]\n---\nReview.\n",
			"Read, Grep, Glob",
		},
		{
			agent.OMP, ".omp/agents/reviewer.md",
			"---\nname: reviewer\ndescription: Reviews\ntools: [read, grep, bash]\n---\nReview.\n",
			"Read, Grep, Glob, Bash, WebFetch",
		},
		{
			agent.Codex,
			".codex/agents/reviewer.toml",
			"name = \"reviewer\"\ndescription = \"Reviews\"\ndeveloper_instructions = \"Review.\"\n" +
				"[features]\nshell_tool = false\n",
			"",
		},
		{
			agent.Cursor, ".cursor/agents/reviewer.md",
			"---\nname: reviewer\ndescription: Reviews\nreadonly: true\n---\nReview.\n",
			"Read, Grep, Glob, WebFetch, WebSearch, TodoWrite, TaskCreate, TaskGet, TaskList, " +
				"TaskUpdate, Skill",
		},
	}
	for _, source := range sources {
		artifact, err := generateFrom(t, source.harness, agent.Claude, source.path, source.content)
		if err != nil {
			t.Fatalf("%s: %v", source.harness, err)
		}
		doc, err := agent.ParseMarkdown(artifact.FileName, artifact.Content)
		if err != nil {
			t.Fatal(err)
		}
		if got := doc.String("tools"); got != source.tools {
			t.Errorf("%s: tools %q, want %q\n%s", source.harness, got, source.tools,
				artifact.Content)
		}
		if source.harness == agent.Codex && doc.String("disallowedTools") != "Bash" {
			t.Errorf("codex: withheld shell must become disallowedTools: Bash\n%s",
				artifact.Content)
		}
	}
}

func TestApprovalPromptsHaveNoEquivalent(t *testing.T) {
	source := "---\ndescription: Reviews\nmode: subagent\npermission:\n  edit: deny\n  bash:\n" +
		"    \"*\": ask\n    \"git diff\": allow\n---\nReview.\n"
	for _, target := range []agent.HarnessID{agent.Claude, agent.Codex, agent.Gemini} {
		_, err := generateFrom(t, agent.OpenCode, target, ".opencode/agents/reviewer.md", source)
		requireIncompatible(t, err)
	}
}

func TestPortableCoreIsPreferredOverOtherVariants(t *testing.T) {
	def := &agent.Definition{
		ID:       "reviewer",
		Portable: portable(t, "---\nname: reviewer\ndescription: Reviews\n---\nReview.\n"),
		Variants: map[agent.HarnessID]*agent.VariantFile{
			agent.Claude: {
				Harness: agent.Claude, Path: ".claude/agents/reviewer.md",
				Raw: []byte(claudeAgent("Read", "Grep", "Glob")),
			},
		},
	}
	_, sourcePath, _, err := harness.Default().Generate(installer(t, agent.Gemini), def)
	if err != nil || sourcePath != "agents/reviewer/AGENT.md" {
		t.Fatalf("want the portable core, got %q, %v", sourcePath, err)
	}
}

func TestV1LocksRestoreUnderTheV2Converter(t *testing.T) {
	for _, h := range agent.AllHarnesses {
		if !harness.ConverterReproduces(string(h)+"/v1", string(h)+"/v2") {
			t.Errorf("%s: v2 must restore v1 locks", h)
		}
	}
	if harness.ConverterReproduces("claude/v1", "codex/v2") ||
		harness.ConverterReproduces("claude/v3", "claude/v2") {
		t.Fatal("only the same harness's v1 is reproducible")
	}
}

func hasNote(notes []string, fragment string) bool {
	for _, note := range notes {
		if strings.Contains(note, fragment) {
			return true
		}
	}
	return false
}
