package harness_test

import (
	"path/filepath"
	"testing"

	"github.com/agnthub-sh/agnthub/agent"
	"github.com/agnthub-sh/agnthub/harness"
	"github.com/agnthub-sh/agnthub/platform/env"
)

// Every Harness installs Skills into a location it discovers, in the requested
// scope, and never through an invented omp backend target.
func TestEverySkillPlacementIsDiscoveredInItsScope(t *testing.T) {
	base := t.TempDir()
	ctx := harness.Context{
		Env: env.FromMap(map[string]string{
			"HOME": filepath.Join(base, "home"), "GEMINI_CLI_TRUST_WORKSPACE": "true",
		}),
		ProjectRoot: filepath.Join(base, "project"),
	}
	for _, installer := range harness.Default().Installers() {
		skillHarness, ok := installer.(harness.SkillHarness)
		if !ok {
			t.Fatalf("%s does not declare skill support", installer.ID())
		}
		for _, scope := range []harness.Scope{harness.ScopeProject, harness.ScopeGlobal} {
			placement, err := skillHarness.SkillPlacement(ctx, scope, nil)
			if err != nil {
				t.Fatalf("%s %s: %v", installer.ID(), scope, err)
			}
			if placement.Target == "" || placement.Target == string(agent.OMP) {
				t.Fatalf("%s needs a supported backend target, got %q", installer.ID(),
					placement.Target)
			}
			visibility, err := skillHarness.SkillVisibility(ctx, scope, nil)
			if err != nil {
				t.Fatalf("%s %s: %v", installer.ID(), scope, err)
			}
			found := false
			for _, location := range visibility.Locations {
				if location.Dir == placement.Dir {
					found = location.Scope == scope
				}
				if scope == harness.ScopeGlobal && location.Scope != harness.ScopeGlobal {
					t.Fatalf("%s: a global agent cannot rely on project skills (%s)",
						installer.ID(), location.Dir)
				}
			}
			if !found {
				t.Fatalf("%s %s: %s installs into %s, which it does not discover in scope",
					installer.ID(), scope, placement.Target, placement.Dir)
			}
		}
	}
}

func TestClaudeUserSkillsOutrankProjectSkills(t *testing.T) {
	base := t.TempDir()
	ctx := harness.Context{
		Env:         env.FromMap(map[string]string{"HOME": filepath.Join(base, "home")}),
		ProjectRoot: filepath.Join(base, "project"),
	}
	installer, err := harness.Default().Installer(agent.Claude)
	if err != nil {
		t.Fatal(err)
	}
	visibility, err := installer.(harness.SkillHarness).SkillVisibility(
		ctx,
		harness.ScopeProject,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	order := map[string]int{}
	for i, location := range visibility.Locations {
		order[location.Dir] = i
	}
	user := filepath.Join(base, "home", ".claude", "skills")
	project := filepath.Join(base, "project", ".claude", "skills")
	if order[user] >= order[project] {
		t.Fatalf("Claude loads personal skills before project skills: %+v", visibility.Locations)
	}
}
