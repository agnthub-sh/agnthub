package install_test

import (
	"path/filepath"
	"testing"

	"github.com/heynemann/agnthub/internal/agent"
	"github.com/heynemann/agnthub/internal/harness"
	"github.com/heynemann/agnthub/internal/install"
	"github.com/heynemann/agnthub/internal/platform/env"
	"github.com/heynemann/agnthub/internal/state"
)

func TestPlanCarriesHarnessDependencyNotes(t *testing.T) {
	root := t.TempDir()
	ownership, err := state.LoadOwnership(filepath.Join(root, "state", "installations.json"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := agent.ParseMarkdown(
		"agents/lead/AGENT.md",
		[]byte("---\nname: lead\ndescription: Leads reviews\n---\nCoordinate.\n"),
	)
	if err != nil {
		t.Fatal(err)
	}
	def := &agent.Definition{
		ID:           "lead",
		Portable:     doc,
		Dependencies: agent.Dependencies{Agents: []agent.AgentDependency{{Agent: "helper"}}},
	}
	planner := &install.Planner{Registry: harness.Default(), Ownership: ownership}
	plan, err := planner.Build(install.Request{
		Targets:   []install.Target{{Definition: def}},
		Harnesses: []agent.HarnessID{agent.Claude, agent.Gemini},
		Scope:     harness.ScopeProject,
		Context: harness.Context{
			Env:         env.FromMap(map[string]string{"HOME": filepath.Join(root, "home")}),
			ProjectRoot: filepath.Join(root, "project"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	notes := map[agent.HarnessID]int{}
	for _, item := range plan.Items {
		if item.Action != install.ActionCreate {
			t.Fatalf("%s: %s %s", item.Harness, item.Action, item.Reason)
		}
		notes[item.Harness] = len(item.Artifact.Notes)
	}
	if notes[agent.Gemini] == 0 || notes[agent.Claude] != 0 {
		t.Fatalf("only Gemini must qualify agent dependencies: %v", notes)
	}
}
