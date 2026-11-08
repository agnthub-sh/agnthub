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

// buildOne plans def for one Harness from src, with an optional restore target
// converter.
func buildOne(
	t *testing.T,
	def *agent.Definition,
	h agent.HarnessID,
	src state.LockedSource,
	converter string,
) install.Item {
	t.Helper()
	root := t.TempDir()
	ownership, err := state.LoadOwnership(filepath.Join(root, "state", "installations.json"))
	if err != nil {
		t.Fatal(err)
	}
	planner := &install.Planner{Registry: harness.Default(), Ownership: ownership}
	plan, err := planner.Build(install.Request{
		Source: src,
		Targets: []install.Target{{
			Definition: def,
			Converter:  map[agent.HarnessID]string{h: converter},
		}},
		Harnesses: []agent.HarnessID{h},
		Scope:     harness.ScopeProject,
		Context: harness.Context{
			Env:         env.FromMap(map[string]string{"HOME": filepath.Join(root, "home")}),
			ProjectRoot: filepath.Join(root, "project"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Items) != 1 || plan.Items[0].Action != install.ActionCreate {
		t.Fatalf("plan: %+v", plan.Items)
	}
	return plan.Items[0]
}

func reviewerDefinition(t *testing.T) *agent.Definition {
	t.Helper()
	doc, err := agent.ParseMarkdown("agents/reviewer.md", []byte(
		"---\nname: reviewer\ndescription: Reviews code\nlicense: Apache-2.0\n---\nReview.\n"))
	if err != nil {
		t.Fatal(err)
	}
	return &agent.Definition{
		ID: "reviewer", Portable: doc,
		License: &agent.License{Path: "LICENSE", Copyright: []string{"Copyright 2025 ACME"}},
		Variants: map[agent.HarnessID]*agent.VariantFile{agent.Claude: {
			Harness: agent.Claude, Path: ".claude/agents/reviewer.md",
			Raw: []byte("---\nname: reviewer\ndescription: Reviews code\n---\nReview.\n"),
		}},
	}
}

// Generated artifacts name their GitHub or Git source, the converted file and
// the source's license; authored Variants install byte for byte.
func TestGeneratedArtifactsCarryTheirSourceAttribution(t *testing.T) {
	def := reviewerDefinition(t)
	github := state.LockedSource{Kind: "github", Locator: "acme/agents", Revision: "abc"}
	item := buildOne(t, def, agent.Gemini, github, "")
	text := string(item.Artifact.Content)
	for _, want := range []string{
		"# Adapted from agents/reviewer.md in github.com/acme/agents\n",
		"# Copyright 2025 ACME\n",
		"# License: Apache-2.0; see LICENSE in github.com/acme/agents\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("gemini artifact lacks %q:\n%s", want, text)
		}
	}
	git := state.LockedSource{Kind: "git", Locator: "git.example.com/acme/agents.git"}
	if text := string(buildOne(t, def, agent.Codex, git, "").Artifact.Content); !strings.HasPrefix(
		text, "# Adapted from agents/reviewer.md in git.example.com/acme/agents\n") {
		t.Errorf("codex artifact:\n%s", text)
	}
	local := state.LockedSource{Kind: "local", Locator: "/home/someone/agents"}
	if text := string(buildOne(t, def, agent.OMP, local, "").Artifact.Content); !strings.Contains(
		text, "# Adapted from agents/reviewer.md\n") || strings.Contains(text, "someone") {
		t.Errorf("a local source's path stays out of the artifact:\n%s", text)
	}
	native := buildOne(t, def, agent.Claude, github, "")
	if string(native.Artifact.Content) != string(def.Variants[agent.Claude].Raw) {
		t.Errorf("an authored Variant must install unchanged:\n%s", native.Artifact.Content)
	}
}

// A restore of a lock written before converters attributed reproduces its
// artifacts without the header; any other plan attributes.
func TestRestoringAnOlderLockLeavesTheAttributionOut(t *testing.T) {
	def := reviewerDefinition(t)
	src := state.LockedSource{Kind: "github", Locator: "acme/agents"}
	current := buildOne(t, def, agent.Cursor, src, "cursor/v7")
	older := buildOne(t, def, agent.Cursor, src, "cursor/v4")
	if !strings.Contains(string(current.Artifact.Content), "# Adapted from") ||
		current.Artifact.Converter != "cursor/v7" {
		t.Fatalf("current lock: %s\n%s", current.Artifact.Converter, current.Artifact.Content)
	}
	if strings.Contains(string(older.Artifact.Content), "# ") ||
		older.Artifact.Converter != "cursor/v5" {
		t.Fatalf("older lock: %s\n%s", older.Artifact.Converter, older.Artifact.Content)
	}
}
