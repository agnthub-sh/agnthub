package install_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agnthub-sh/agnthub/agent"
	"github.com/agnthub-sh/agnthub/harness"
	"github.com/agnthub-sh/agnthub/install"
	"github.com/agnthub-sh/agnthub/platform/env"
	"github.com/agnthub-sh/agnthub/state"
)

// An authored file installs byte-identical with its display name, so two
// agents can carry one name in a directory; a harness that invokes agents by
// name would load only one of them, so the plan reports a conflict.
func TestPlanRefusesAgentsAHarnessWouldLoadUnderOneName(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "project")
	agents := filepath.Join(project, ".claude", "agents")
	if err := os.MkdirAll(agents, 0o755); err != nil {
		t.Fatal(err)
	}
	existing := "---\nname: Search Relevance Engineer\ndescription: Someone else's\n---\nOther.\n"
	if err := os.WriteFile(
		filepath.Join(agents, "search.md"),
		[]byte(existing),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	ownership, err := state.LoadOwnership(filepath.Join(root, "state", "installations.json"))
	if err != nil {
		t.Fatal(err)
	}
	native := func(id, name string) *agent.Definition {
		raw := "---\nname: " + name + "\ndescription: Tunes search\n---\nTune.\n"
		return &agent.Definition{ID: id, Variants: map[agent.HarnessID]*agent.VariantFile{
			agent.Claude: {
				Harness: agent.Claude,
				Path:    ".claude/agents/" + id + ".md",
				Raw:     []byte(raw),
			},
		}}
	}
	build := func(defs ...*agent.Definition) *install.Plan {
		targets := make([]install.Target, len(defs))
		for i, def := range defs {
			targets[i] = install.Target{Definition: def}
		}
		planner := &install.Planner{Registry: harness.Default(), Ownership: ownership}
		plan, err := planner.Build(install.Request{
			Targets: targets, Harnesses: []agent.HarnessID{agent.Claude, agent.Copilot},
			Scope: harness.ScopeProject,
			Context: harness.Context{
				Env:         env.FromMap(map[string]string{"HOME": filepath.Join(root, "home")}),
				ProjectRoot: project,
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		return plan
	}
	actions := func(plan *install.Plan) map[string]install.Item {
		out := map[string]install.Item{}
		for _, item := range plan.Items {
			out[item.Agent+"/"+string(item.Harness)] = item
		}
		return out
	}

	got := actions(build(native("search-relevance-engineer", "Search Relevance Engineer")))
	claude := got["search-relevance-engineer/claude"]
	if claude.Action != install.ActionConflict || !strings.Contains(claude.Reason, "search.md") {
		t.Fatalf(
			"claude: want a conflict naming search.md, got %s %q",
			claude.Action,
			claude.Reason,
		)
	}
	if copilot := got["search-relevance-engineer/copilot"]; copilot.Action != install.ActionCreate {
		t.Fatalf(
			"Copilot CLI identifies agents by file name: got %s %q",
			copilot.Action,
			copilot.Reason,
		)
	}

	got = actions(build(native("tuner", "Tuner"), native("ranker", "Tuner")))
	conflicts := 0
	for _, item := range got {
		if item.Harness == agent.Claude && item.Action == install.ActionConflict {
			conflicts++
		}
	}
	if conflicts != 1 {
		t.Fatalf("two planned agents named Tuner: want one conflict, got %v", got)
	}

	if item := actions(build(native("tuner", "Tuner")))["tuner/claude"]; item.Action != install.ActionCreate {
		t.Fatalf("a unique display name installs: got %s %q", item.Action, item.Reason)
	}
}
