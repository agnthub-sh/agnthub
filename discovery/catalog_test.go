package discovery_test

import (
	"slices"
	"testing"

	"github.com/agnthub-sh/agnthub/agent"
	"github.com/agnthub-sh/agnthub/discovery"
)

// division writes an agency-agents style agent: a display name, cosmetic
// keys, and a body.
func division(name, description string) string {
	return "---\nname: " + name + "\ndescription: " + description +
		"\ncolor: \"#00BFB3\"\nemoji: 🔎\nvibe: Finds it, ranks it.\n---\n# " + name + "\n\nDo the work.\n"
}

// catalogRepository is shaped like msitarzewski/agency-agents: division
// folders of agents (one nested a level down), a one-agent division, and
// folders of documents, scripts and examples.
func catalogRepository() map[string]string {
	return map[string]string{
		"README.md": "# The Agency\n",
		"engineering/engineering-search-relevance-engineer.md": division(
			"Search Relevance Engineer", "Expert search engineer for OpenSearch"),
		"engineering/engineering-frontend-developer.md": division(
			"Frontend Developer",
			"Builds UIs",
		),
		"engineering/engineering-backend-architect.md": division(
			"Backend Architect",
			"Designs APIs",
		),
		"engineering/README.md":                 "# Engineering division\n",
		"marketing/marketing-seo-specialist.md": division("SEO Specialist", "Ranks pages"),
		"marketing/marketing-content-creator.md": division(
			"Content Creator",
			"Writes posts",
		),
		"marketing/marketing-growth-hacker.md": division("Growth Hacker", "Grows users"),
		"marketing/marketing-tiktok-strategist.md": division(
			"TikTok Strategist",
			"Plans videos",
		),
		"game-development/game-designer.md": division("Game Designer", "Designs games"),
		"game-development/unity/unity-architect.md": division(
			"Unity Architect",
			"Structures Unity",
		),
		"game-development/godot/godot-shader-developer.md": division(
			"Godot Shader Developer", "Writes shaders"),
		"research/research-synthesist.md":         division("Research Synthesist", "Synthesizes"),
		"strategy/nexus-strategy.md":              "# NEXUS\n\nThe playbook.\n",
		"strategy/QUICKSTART.md":                  "# Quickstart\n",
		"strategy/playbooks/phase-0-discovery.md": "# Phase 0\n",
		"examples/workflow-landing-page.md":       "# Workflow\n",
		"scripts/convert.sh":                      "#!/bin/sh\n",
		"integrations/claude-code/agent.md":       division("Converted Copy", "An output"),
		".github/PULL_REQUEST_TEMPLATE.md":        "# PR\n",
	}
}

func TestDiscoverCatalogLayoutReadsDivisionFolders(t *testing.T) {
	inv := discover(t, memTree(t, catalogRepository()))
	var ids []string
	for _, def := range inv.Agents {
		ids = append(ids, def.ID)
	}
	want := []string{
		"engineering-backend-architect", "engineering-frontend-developer",
		"engineering-search-relevance-engineer", "game-designer", "godot-shader-developer",
		"marketing-content-creator", "marketing-growth-hacker", "marketing-seo-specialist",
		"marketing-tiktok-strategist", "research-synthesist", "unity-architect",
	}
	if !slices.Equal(ids, want) || len(inv.Problems) != 0 {
		t.Fatalf("agents %v, want %v; problems %v", ids, want, inv.Problems)
	}
	engineer := inv.Find("engineering-search-relevance-engineer")
	if engineer.Division != "engineering" || engineer.Portable == nil ||
		engineer.Portable.Path != "engineering/engineering-search-relevance-engineer.md" ||
		engineer.Description != "Expert search engineer for OpenSearch" {
		t.Fatalf("unexpected catalog agent: %+v", engineer)
	}
	if inv.Find("unity-architect").Division != "game-development" {
		t.Fatal("a division's subfolder belongs to the division")
	}
}

func TestDiscoverCatalogLayoutRefusesWhatIsNotACatalog(t *testing.T) {
	page := func(title string) string {
		return "---\ntitle: " + title + "\ndescription: A page\n---\n# " + title + "\n"
	}
	cases := map[string]map[string]string{
		"documentation pages with frontmatter": {
			"handbook/install.md":   page("Install"),
			"handbook/configure.md": page("Configure"),
			"handbook/deploy.md":    page("Deploy"),
			"handbook/upgrade.md":   page("Upgrade"),
			"handbook/faq.md":       page("FAQ"),
			"handbook/agent-a.md":   division("Agent A", "Mentioned in the docs"),
			"handbook/agent-b.md":   division("Agent B", "Mentioned in the docs"),
		},
		"a folder that is mostly documents": {
			"team/lead.md":     division("Lead", "Leads"),
			"team/reviewer.md": division("Reviewer", "Reviews"),
			"team/planner.md":  division("Planner", "Plans"),
			"team/a.md":        "# Notes\n",
			"team/b.md":        "# Notes\n",
			"team/c.md":        "# Notes\n",
			"team/d.md":        "# Notes\n",
			"team/e.md":        "# Notes\n",
		},
		"only two agents": {
			"team/lead.md":     division("Lead", "Leads"),
			"team/reviewer.md": division("Reviewer", "Reviews"),
		},
		"agents in excluded folders": {
			"docs/a.md":     division("A", "A"),
			"docs/b.md":     division("B", "B"),
			"docs/c.md":     division("C", "C"),
			"examples/d.md": division("D", "D"),
			"examples/e.md": division("E", "E"),
			"examples/f.md": division("F", "F"),
		},
		"frontmatter without instructions": {
			"team/a.md": "---\nname: A\ndescription: A\n---\n",
			"team/b.md": "---\nname: B\ndescription: B\n---\n",
			"team/c.md": "---\nname: C\ndescription: C\n---\n",
			"team/d.md": "---\nname: D\ndescription: D\n---\n",
			"team/e.md": "---\nname: E\ndescription: E\n---\n",
		},
		"another product's records": {
			"personas/architect.md": "---\nid: architect\nname: Architect\nrole: Architect\ndomain: core\n" +
				"description: Designs\n---\nYou are the architect.\n",
			"personas/reviewer.md": "---\nid: reviewer\nname: Reviewer\nrole: Reviewer\ndomain: core\n" +
				"description: Reviews\n---\nYou are the reviewer.\n",
			"personas/planner.md": "---\nid: planner\nname: Planner\nrole: Planner\ndomain: core\n" +
				"description: Plans\n---\nYou are the planner.\n",
			"public_agents/base/writer.md": "---\nname: Writer\nslug: writer\n" +
				"description: Writes\nmodel: ''\ntags: [Base]\n---\nWrite.\n",
			"public_agents/base/translator.md": "---\nname: Translator\nslug: translator\n" +
				"description: Translates\nmodel: ''\ntags: [Base]\n---\nTranslate.\n",
			"public_agents/base/researcher.md": "---\nname: Researcher\nslug: researcher\n" +
				"description: Researches\nmodel: ''\ntags: [Base]\n---\nResearch.\n",
		},
	}
	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			inv := discover(t, memTree(t, files))
			if len(inv.Agents) != 0 {
				t.Fatalf("not a catalog, but discovered %v", inv.Agents)
			}
		})
	}
}

func TestDiscoverCatalogLayoutOnlyWithoutOtherAgents(t *testing.T) {
	files := catalogRepository()
	files["agents/reviewer.md"] = portable("reviewer", "Reviews")
	inv := discover(t, memTree(t, files))
	if len(inv.Agents) != 1 || inv.Find("reviewer") == nil {
		t.Fatalf("an agents/ repository keeps its inventory, got %v", inv.Agents)
	}
	files = catalogRepository()
	files["agnthub.yaml"] = "schemaVersion: 1\nagents:\n  reviewer:\n    portable: " +
		"engineering/engineering-backend-architect.md\n"
	if inv := discover(t, memTree(t, files)); len(inv.Agents) != 1 || !inv.Manifest {
		t.Fatalf("agnthub.yaml stays authoritative, got %v", inv.Agents)
	}
}

func TestDiscoverCatalogLayoutNormalizesFileNames(t *testing.T) {
	files := map[string]string{
		"team/Search Relevance Engineer.md": division("Search Relevance Engineer", "Searches"),
		"team/Café Planner (v2).md":         division("Café Planner", "Plans"),
		"team/ŁÓDŹ_ops.md":                  division("Ops", "Operates"),
		"team/reviewer.md":                  division("Reviewer", "Reviews"),
		"team/Reviewer.md":                  division("Reviewer Two", "Also reviews"),
		"team/计划.md":                        division("Planner", "Plans in Chinese"),
	}
	inv := discover(t, memTree(t, files))
	for _, id := range []string{"search-relevance-engineer", "cafe-planner-v2", "lodz_ops"} {
		if inv.Find(id) == nil {
			t.Errorf("missing %s in %v (problems %v)", id, inv.Agents, inv.Problems)
		}
	}
	if inv.Find("reviewer") != nil || len(inv.ProblemsFor("reviewer")) == 0 {
		t.Errorf("two files with one identifier must be reported, not merged: %v", inv.Problems)
	}
	if !slices.ContainsFunc(inv.Problems, func(p discovery.Problem) bool {
		return p.Path == "team/计划.md"
	}) {
		t.Errorf("a file name without identifier characters must be reported: %v", inv.Problems)
	}
}

func TestDiscoverAcceptsDisplayNamesInEveryLayout(t *testing.T) {
	display := func(name string) string {
		return "---\nname: " + name + "\ndescription: Does it\n---\nDo it.\n"
	}
	inv := discover(t, memTree(t, map[string]string{
		"agents/code-reviewer.md":                 display("Code Reviewer"),
		"agents/Fabric Admin.agent.md":            display("FabricAdmin"),
		".claude/agents/test-writer.md":           display("Test Writer"),
		".omp/agents/Release Manager.md":          display("Release Manager"),
		".cursor/agents/doc-writer.md":            display("Doc Writer"),
		".codex/agents/triager.toml":              "name = \"Issue Triager\"\ndescription = \"Triages\"\ndeveloper_instructions = \"Triage.\"\n",
		".github/agents/Pair Programmer.agent.md": display("Pair Programmer"),
	}))
	for _, id := range []string{
		"code-reviewer", "fabric-admin", "test-writer", "release-manager", "doc-writer",
		"triager", "pair-programmer",
	} {
		if inv.Find(id) == nil {
			t.Errorf(
				"display name refused for %s: agents %v, problems %v",
				id,
				inv.Agents,
				inv.Problems,
			)
		}
	}
	for _, harnessID := range []agent.HarnessID{agent.Claude, agent.OMP, agent.Cursor, agent.Codex} {
		found := false
		for _, def := range inv.Agents {
			found = found || def.Variant(harnessID) != nil
		}
		if !found {
			t.Errorf("no %s variant discovered", harnessID)
		}
	}
	if n := len(inv.Problems); n != 0 {
		t.Errorf("%d problems: %v", n, inv.Problems)
	}
}
