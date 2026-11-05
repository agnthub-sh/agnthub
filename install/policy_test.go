package install_test

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/agnthub-sh/agnthub/agent"
	"github.com/agnthub-sh/agnthub/harness"
	"github.com/agnthub-sh/agnthub/install"
	"github.com/agnthub-sh/agnthub/platform/env"
	"github.com/agnthub-sh/agnthub/state"
)

// conventionReviewer is a minimized
// https://github.com/abyssparanoia/rapid-go/blob/56b39de68aa1/.claude/agents/convention-reviewer.md:
// a reviewer that runs shell commands but never edits, which Cursor can only
// install read-only, without its shell.
func conventionReviewer() *agent.Definition {
	return &agent.Definition{
		ID: "convention-reviewer",
		Variants: map[agent.HarnessID]*agent.VariantFile{
			agent.Claude: {
				Harness: agent.Claude, Path: ".claude/agents/convention-reviewer.md",
				Raw: []byte("---\nname: convention-reviewer\ndescription: Checks compliance " +
					"with project conventions.\nmodel: sonnet\ntools: [Read, Glob, Grep, Bash, " +
					"WebSearch]\n---\nCheck the conventions.\n"),
			},
		},
	}
}

func planFor(
	t *testing.T,
	def *agent.Definition,
	allow bool,
	harnesses ...agent.HarnessID,
) *install.Plan {
	t.Helper()
	root := t.TempDir()
	ownership, err := state.LoadOwnership(filepath.Join(root, "state", "installations.json"))
	if err != nil {
		t.Fatal(err)
	}
	planner := &install.Planner{Registry: harness.Default(), Ownership: ownership}
	plan, err := planner.Build(install.Request{
		Targets:   []install.Target{{Definition: def}},
		Harnesses: harnesses,
		Scope:     harness.ScopeProject,
		Context: harness.Context{
			Env:         env.FromMap(map[string]string{"HOME": filepath.Join(root, "home")}),
			ProjectRoot: filepath.Join(root, "project"),
		},
		AllowWithoutShell: allow,
	})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestLosingTheShellNeedsTheOptIn(t *testing.T) {
	refused := planFor(t, conventionReviewer(), false, agent.Cursor, agent.Copilot)
	for _, item := range refused.Items {
		switch item.Harness {
		case agent.Cursor:
			if item.Action != install.ActionIncompatible ||
				!strings.Contains(item.Reason, "--allow-without-shell") ||
				!item.Artifact.WithoutShell() {
				t.Errorf("cursor: want a refusal naming the flag, got %s %q", item.Action,
					item.Reason)
			}
		case agent.Copilot:
			// Losing web search needs no opt-in.
			if item.Action != install.ActionCreate ||
				!slices.Equal(item.Artifact.ReducedNames(), []string{"web search"}) {
				t.Errorf("copilot: want a create without web search, got %s %v", item.Action,
					item.Artifact.ReducedNames())
			}
		}
	}
	allowed := planFor(t, conventionReviewer(), true, agent.Cursor)
	if item := allowed.Items[0]; item.Action != install.ActionCreate ||
		!slices.Equal(item.Artifact.ReducedNames(), []string{"shell"}) {
		t.Errorf("cursor with the opt-in: want a create without shell, got %s %q", item.Action,
			item.Reason)
	}
}

func TestAssessReportsReductionsAndTheOptIn(t *testing.T) {
	got := assessAll(conventionReviewer())
	cursor := got[agent.Cursor]
	if cursor.Status != install.StatusGenerated || cursor.OptIn != install.OptInWithoutShell ||
		!slices.Equal(cursor.Reduced, []string{"shell"}) {
		t.Errorf("cursor: want an installable target needing the opt-in, got %+v", cursor)
	}
	copilot := got[agent.Copilot]
	if copilot.Status != install.StatusGenerated || copilot.OptIn != "" ||
		!slices.Equal(copilot.Reduced, []string{"web search"}) {
		t.Errorf("copilot: want a reduced install without opt-in, got %+v", copilot)
	}
	if claude := got[agent.Claude]; claude.Status != install.StatusNative || claude.Reduced != nil {
		t.Errorf("claude: %+v", claude)
	}
}
