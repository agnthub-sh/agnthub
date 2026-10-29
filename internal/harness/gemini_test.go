package harness_test

import (
	"path/filepath"
	"testing"

	"github.com/heynemann/agnthub/internal/agent"
	"github.com/heynemann/agnthub/internal/harness"
	"github.com/heynemann/agnthub/internal/platform/env"
)

func TestGeminiNativeLocalAndRemoteAgentsInstallVerbatim(t *testing.T) {
	cases := map[string]struct {
		content string
		notes   bool
	}{
		"local": {
			content: "---\nname: reviewer\ndescription: Reviews code\nkind: local\n" +
				"tools: [read_file, grep_search, 'mcp_github_*']\nmodel: inherit\n" +
				"temperature: 0.2\nmax_turns: 10\ntimeout_mins: 5\n---\nReview.\n",
		},
		"local with mcp servers": {
			content: "---\nname: reviewer\ndescription: d\nmcp_servers:\n  gh:\n    command: gh-mcp\n---\nReview.\n",
			notes:   true,
		},
		"remote": {
			content: "---\nname: reviewer\ndescription: d\nkind: remote\n" +
				"agent_card_url: https://example.com/card.json\n---\n",
			notes: true,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			artifact, err := installer(t, agent.Gemini).
				Native("reviewer", variant(agent.Gemini, tc.content))
			if err != nil {
				t.Fatal(err)
			}
			if string(artifact.Content) != tc.content || artifact.FileName != "reviewer.md" {
				t.Fatalf("native variant must be installed byte-for-byte: %+v", artifact)
			}
			if (len(artifact.Notes) > 0) != tc.notes {
				t.Fatalf("notes: %v", artifact.Notes)
			}
		})
	}
}

func TestGeminiNativeRefusesWhatGeminiRejectsOrCannotHonor(t *testing.T) {
	cases := map[string]struct{ id, content string }{
		"name mismatch":   {"reviewer", "---\nname: other\ndescription: d\n---\nBody\n"},
		"name not a slug": {"reviewer", "---\nname: Reviewer\ndescription: d\n---\nBody\n"},
		"missing name":    {"reviewer", "---\ndescription: d\n---\nBody\n"},
		"unknown key": {
			"reviewer",
			"---\nname: reviewer\ndescription: d\nreadonly: true\n---\nBody\n",
		},
		"unknown kind": {
			"reviewer",
			"---\nname: reviewer\ndescription: d\nkind: team\n---\nBody\n",
		},
		"delegation tool": {
			"reviewer",
			"---\nname: reviewer\ndescription: d\ntools: [read_file, invoke_agent]\n---\nBody\n",
		},
		"tools string": {
			"reviewer",
			"---\nname: reviewer\ndescription: d\ntools: read_file\n---\nBody\n",
		},
		"tool mapping": {
			"reviewer",
			"---\nname: reviewer\ndescription: d\ntools: [{read_file: true}]\n---\nBody\n",
		},
		"zero turns": {
			"reviewer",
			"---\nname: reviewer\ndescription: d\nmax_turns: 0\n---\nBody\n",
		},
		"string temp": {
			"reviewer",
			"---\nname: reviewer\ndescription: d\ntemperature: hot\n---\nBody\n",
		},
		"empty prompt":   {"reviewer", "---\nname: reviewer\ndescription: d\n---\n"},
		"remote no card": {"reviewer", "---\nname: reviewer\ndescription: d\nkind: remote\n---\n"},
		"remote with tools": {
			"reviewer",
			"---\nname: reviewer\ndescription: d\nkind: remote\nagent_card_url: https://e.x/c\ntools: ['*']\n---\n",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := installer(t, agent.Gemini).Native(tc.id, variant(agent.Gemini, tc.content))
			requireInvalid(t, err)
		})
	}
}

func TestGeminiConversionRefusesDelegationAndRestrictions(t *testing.T) {
	for _, field := range []string{"spawns: [helper]", "agents: [helper]", "tools: [read]", "readonly: true"} {
		doc := portable(t, "---\nname: reviewer\ndescription: d\n"+field+"\n---\nBody\n")
		_, err := installer(t, agent.Gemini).Convert("reviewer", doc)
		requireIncompatible(t, err)
	}
}

func TestGeminiConversionIsAValidGeminiAgent(t *testing.T) {
	gemini := installer(t, agent.Gemini)
	doc := portable(
		t,
		"---\nname: reviewer\ndescription: |-\n  Reviews code\n  in depth\ntags: [x]\n---\nReview.\n",
	)
	artifact, err := gemini.Convert("reviewer", doc)
	if err != nil {
		t.Fatal(err)
	}
	if !artifact.Generated || artifact.Converter != "gemini/v1" || len(artifact.Notes) == 0 {
		t.Fatalf("generated artifact must record converter and omissions: %+v", artifact)
	}
	if _, err := gemini.Native(
		"reviewer",
		variant(agent.Gemini, string(artifact.Content)),
	); err != nil {
		t.Fatalf("generated content is not a valid Gemini agent: %v", err)
	}
}

func TestGeminiAgentDependenciesAreNotRuntimeDelegation(t *testing.T) {
	advisor, ok := installer(t, agent.Gemini).(harness.DependencyAdvisor)
	if !ok {
		t.Fatal("Gemini must explain what companion agents mean")
	}
	if notes := advisor.DependencyNotes(agent.Dependencies{}); len(notes) != 0 {
		t.Fatalf("no dependencies, no notes: %v", notes)
	}
	skills := agent.Dependencies{Skills: []agent.SkillDependency{{Skill: "s"}}}
	if notes := advisor.DependencyNotes(skills); len(notes) != 0 {
		t.Fatalf("skills are not delegation: %v", notes)
	}
	agents := agent.Dependencies{Agents: []agent.AgentDependency{{Agent: "helper"}}}
	if notes := advisor.DependencyNotes(agents); len(notes) == 0 {
		t.Fatal("agent dependencies must carry a nondelegation note")
	}
}

func TestGeminiGlobalDestinationHonorsGeminiCLIHome(t *testing.T) {
	gemini := installer(t, agent.Gemini)
	cases := map[string]struct {
		vars map[string]string
		want string
	}{
		"home": {map[string]string{"HOME": "/home/u"}, "/home/u/.gemini/agents"},
		"cli home": {
			map[string]string{"HOME": "/home/u", "GEMINI_CLI_HOME": "/srv/g"},
			"/srv/g/.gemini/agents",
		},
		"relative home": {
			map[string]string{"HOME": "/home/u", "GEMINI_CLI_HOME": "rel"},
			"/home/u/.gemini/agents",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			ctx := harness.Context{Env: env.FromMap(tc.vars), ProjectRoot: "/p"}
			global, err := gemini.Destination(ctx, harness.ScopeGlobal, nil)
			if err != nil || global.Dir != filepath.FromSlash(tc.want) {
				t.Fatalf("global destination: %+v %v", global, err)
			}
		})
	}
	project, err := gemini.Destination(
		harness.Context{Env: env.FromMap(nil), ProjectRoot: "/p"}, harness.ScopeProject, nil,
	)
	if err != nil || project.Dir != filepath.Join("/p", ".gemini", "agents") {
		t.Fatalf("project destination: %+v %v", project, err)
	}
}
