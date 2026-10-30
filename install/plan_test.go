package install_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/heynemann/agnthub/agent"
	"github.com/heynemann/agnthub/harness"
	"github.com/heynemann/agnthub/install"
	"github.com/heynemann/agnthub/platform/env"
	"github.com/heynemann/agnthub/state"
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

func TestReduceKeepsDependencyClosurePerHarness(t *testing.T) {
	item := func(id string, h agent.HarnessID, action install.Action, requiredBy ...string) install.Item {
		return install.Item{Agent: id, Harness: h, Action: action, RequiredBy: requiredBy}
	}
	plan := &install.Plan{Items: []install.Item{
		item("reviewer", agent.Claude, install.ActionCreate),
		item("reviewer", agent.Codex, install.ActionConflict),
		item("solo", agent.Claude, install.ActionCreate),
		item("planner", agent.Claude, install.ActionIncompatible, "reviewer"),
		item("planner", agent.Codex, install.ActionCreate, "reviewer"),
		item("linter", agent.Claude, install.ActionCreate, "planner (github.com/acme/tools#v1)"),
	}}
	kept, dropped, _ := plan.Reduce()
	var keptKeys, droppedKeys []string
	for _, it := range kept.Items {
		keptKeys = append(keptKeys, it.Agent+"/"+string(it.Harness))
	}
	for _, it := range dropped {
		droppedKeys = append(droppedKeys, it.Agent+"/"+string(it.Harness))
		if !it.Action.Blocking() && it.Reason == "" {
			t.Fatalf("cascaded %s/%s without a reason", it.Agent, it.Harness)
		}
	}
	if got := strings.Join(keptKeys, " "); got != "solo/claude" {
		t.Fatalf(
			"kept %q; a dependent of a blocked companion or an orphaned companion survived",
			got,
		)
	}
	if len(droppedKeys) != 5 {
		t.Fatalf("dropped %v", droppedKeys)
	}
}
