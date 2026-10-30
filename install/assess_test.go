package install_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/agnthub-sh/agnthub/agent"
	"github.com/agnthub-sh/agnthub/harness"
	"github.com/agnthub-sh/agnthub/install"
	"github.com/agnthub-sh/agnthub/platform/env"
	"github.com/agnthub-sh/agnthub/state"
)

func portableDoc(t *testing.T, frontmatter string) *agent.Document {
	t.Helper()
	doc, err := agent.ParseMarkdown(
		"agents/reviewer.md",
		[]byte(
			"---\nname: reviewer\ndescription: Reviews code\n"+frontmatter+"---\nReview carefully.\n",
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func assessAll(def *agent.Definition) map[agent.HarnessID]install.Assessment {
	out := map[agent.HarnessID]install.Assessment{}
	for _, h := range agent.AllHarnesses {
		out[h] = install.Assess(harness.Default(), def, h)
	}
	return out
}

func TestAssessGeneratesEveryHarnessFromAPortableCore(t *testing.T) {
	def := &agent.Definition{ID: "reviewer", Portable: portableDoc(t, "")}
	for h, a := range assessAll(def) {
		if a.Status != install.StatusGenerated || a.SourcePath != "agents/reviewer.md" ||
			a.Reason != "" {
			t.Errorf("%s: %+v", h, a)
		}
	}
}

// The first refusal is reported when neither the portable core nor another
// Variant converts.
func TestAssessPrefersValidVariantsAndRefusesUnsupportedPortableFields(t *testing.T) {
	def := &agent.Definition{
		ID:       "reviewer",
		Portable: portableDoc(t, "tools: Read\n"),
		Variants: map[agent.HarnessID]*agent.VariantFile{
			agent.Claude: {
				Harness: agent.Claude,
				Path:    ".claude/agents/reviewer.md",
				Raw: []byte(
					"---\nname: reviewer\ndescription: Reviews\ntools: Read\n" +
						"permissionMode: acceptEdits\n---\nReview.\n",
				),
			},
		},
	}
	got := assessAll(def)
	if a := got[agent.Claude]; a.Status != install.StatusNative ||
		a.SourcePath != ".claude/agents/reviewer.md" {
		t.Fatalf("claude: %+v", a)
	}
	for _, h := range agent.AllHarnesses[1:] {
		a := got[h]
		if a.Status != install.StatusIncompatible || !strings.Contains(a.Reason, `"tools"`) {
			t.Errorf("%s: want incompatible naming the field, got %+v", h, a)
		}
	}
}

func TestAssessNeverFallsBackFromAnInvalidVariant(t *testing.T) {
	def := &agent.Definition{
		ID:       "reviewer",
		Portable: portableDoc(t, ""),
		Variants: map[agent.HarnessID]*agent.VariantFile{
			agent.Codex: {
				Harness: agent.Codex,
				Path:    ".codex/agents/reviewer.toml",
				Raw: []byte(
					"name = \"other\"\ndescription = \"d\"\ndeveloper_instructions = \"x\"\n",
				),
			},
		},
	}
	a := install.Assess(harness.Default(), def, agent.Codex)
	if a.Status != install.StatusInvalid ||
		!strings.Contains(a.Reason, ".codex/agents/reviewer.toml") {
		t.Fatalf("invalid variant must not fall back to conversion: %+v", a)
	}
}

func TestAssessConvertsNativeOnlyAgentsFromTheirVariant(t *testing.T) {
	native := func(frontmatter string) *agent.Definition {
		return &agent.Definition{
			ID: "reviewer",
			Variants: map[agent.HarnessID]*agent.VariantFile{
				agent.Claude: {
					Harness: agent.Claude,
					Path:    ".claude/agents/reviewer.md",
					Raw: []byte("---\nname: reviewer\ndescription: Reviews\n" + frontmatter +
						"---\nReview.\n"),
				},
			},
		}
	}
	got := assessAll(native("model: sonnet\ntools: Read, Grep, Glob, Edit, Write, Bash\n"))
	if got[agent.Claude].Status != install.StatusNative {
		t.Fatalf("claude: %+v", got[agent.Claude])
	}
	for _, h := range agent.AllHarnesses[1:] {
		if a := got[h]; a.Status != install.StatusGenerated ||
			a.SourcePath != ".claude/agents/reviewer.md" {
			t.Errorf("%s: want generated from the Claude variant, got %+v", h, a)
		}
	}
	hooks := native("hooks:\n  Stop:\n    - command: echo\n")
	if a := install.Assess(
		harness.Default(),
		hooks,
		agent.OMP,
	); a.Status != install.StatusIncompatible ||
		!strings.Contains(a.Reason, "hooks") {
		t.Fatalf("omp: hooks must refuse conversion, got %+v", a)
	}
}

func TestAssessAddsVariantDependencies(t *testing.T) {
	def := &agent.Definition{
		ID:           "reviewer",
		Portable:     portableDoc(t, ""),
		Dependencies: agent.Dependencies{Agents: []agent.AgentDependency{{Agent: "planner"}}},
		Variants: map[agent.HarnessID]*agent.VariantFile{
			agent.Claude: {
				Harness: agent.Claude,
				Path:    ".claude/agents/reviewer.md",
				Raw:     []byte("---\nname: reviewer\ndescription: Reviews\n---\nReview.\n"),
				Dependencies: agent.Dependencies{
					Skills: []agent.SkillDependency{
						{Source: "o/s", Skill: "lint", MinVersion: "1.0.0"},
					},
				},
			},
		},
	}
	got := assessAll(def)
	if d := got[agent.Claude].Dependencies; len(d.Agents) != 1 || len(d.Skills) != 1 {
		t.Fatalf("claude deps: %+v", d)
	}
	if d := got[agent.OMP].Dependencies; len(d.Agents) != 1 || len(d.Skills) != 0 {
		t.Fatalf("omp deps: %+v", d)
	}
	if len(got[agent.Gemini].Notes) == 0 {
		t.Fatal("gemini must carry its dependency advisory like a plan does")
	}
}

// The registry shows what the CLI would do: assessment and plan agree on every Harness.
func TestAssessAgreesWithThePlanner(t *testing.T) {
	root := t.TempDir()
	ownership, err := state.LoadOwnership(filepath.Join(root, "state", "installations.json"))
	if err != nil {
		t.Fatal(err)
	}
	defs := []*agent.Definition{
		{ID: "reviewer", Portable: portableDoc(t, "")},
		{ID: "reviewer", Portable: portableDoc(t, "model: opus\n")},
		{
			ID:       "reviewer",
			Portable: portableDoc(t, ""),
			Variants: map[agent.HarnessID]*agent.VariantFile{
				agent.Gemini: {
					Harness: agent.Gemini,
					Path:    ".gemini/agents/reviewer.md",
					Raw:     []byte("not frontmatter"),
				},
			},
		},
	}
	for i, def := range defs {
		planner := &install.Planner{Registry: harness.Default(), Ownership: ownership}
		plan, err := planner.Build(install.Request{
			Targets:   []install.Target{{Definition: def}},
			Harnesses: agent.AllHarnesses,
			Scope:     harness.ScopeProject,
			Context: harness.Context{
				Env:         env.FromMap(map[string]string{"HOME": filepath.Join(root, "home")}),
				ProjectRoot: filepath.Join(root, "project"),
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range plan.Items {
			a := install.Assess(harness.Default(), def, item.Harness)
			want := map[install.Status]install.Action{
				install.StatusIncompatible: install.ActionIncompatible,
				install.StatusInvalid:      install.ActionInvalid,
				install.StatusNative:       install.ActionCreate,
				install.StatusGenerated:    install.ActionCreate,
			}[a.Status]
			if item.Action != want || (item.Reason != "" && item.Reason != a.Reason) {
				t.Errorf("def %d %s: plan %s %q, assessment %s %q",
					i, item.Harness, item.Action, item.Reason, a.Status, a.Reason)
			}
		}
	}
}
