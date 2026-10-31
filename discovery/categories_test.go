package discovery_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/agnthub-sh/agnthub/discovery"
	"github.com/agnthub-sh/agnthub/harness"
)

func TestManifestCategoriesPerAgentOverrideRepository(t *testing.T) {
	inv := discover(t, memTree(t, map[string]string{
		"agnthub.yaml": `schemaVersion: 1
categories: [software-engineering, code-review, software-engineering]
agents:
  reviewer:
    portable: agents/reviewer.md
    categories: [code-review, security]
  planner:
    portable: agents/planner.md
`,
		"agents/reviewer.md": portable("reviewer", "Reviews"),
		"agents/planner.md":  portable("planner", "Plans"),
	}))
	if want := []string{
		"software-engineering",
		"code-review",
	}; !slices.Equal(
		inv.Categories,
		want,
	) {
		t.Fatalf(
			"repository categories = %v, want %v (deduplicated, declaration order)",
			inv.Categories,
			want,
		)
	}
	reviewer, planner := inv.Find("reviewer"), inv.Find("planner")
	if want := []string{
		"code-review",
		"security",
	}; !slices.Equal(
		inv.CategoriesFor(reviewer),
		want,
	) {
		t.Fatalf("reviewer categories = %v, want its own %v", inv.CategoriesFor(reviewer), want)
	}
	if len(planner.Categories) != 0 || !slices.Equal(inv.CategoriesFor(planner), inv.Categories) {
		t.Fatalf("planner should inherit repository categories, got own=%v effective=%v",
			planner.Categories, inv.CategoriesFor(planner))
	}
}

func TestManifestKeepsUnknownButSlugShapedCategories(t *testing.T) {
	inv := discover(t, memTree(t, map[string]string{
		"agnthub.yaml": `schemaVersion: 1
categories: [quantum-computing-2030]
agents:
  reviewer:
    portable: agents/reviewer.md
    categories: [not-yet-in-taxonomy]
`,
		"agents/reviewer.md": portable("reviewer", "Reviews"),
	}))
	if !slices.Equal(inv.Categories, []string{"quantum-computing-2030"}) {
		t.Fatalf("repository categories = %v", inv.Categories)
	}
	if got := inv.CategoriesFor(
		inv.Find("reviewer"),
	); !slices.Equal(
		got,
		[]string{"not-yet-in-taxonomy"},
	) {
		t.Fatalf("agent categories = %v", got)
	}
}

func TestManifestRejectsMalformedCategories(t *testing.T) {
	for name, manifest := range map[string]string{
		"uppercase":         "schemaVersion: 1\ncategories: [Software]\nagents:\n  reviewer:\n    portable: agents/reviewer.md\n",
		"space":             "schemaVersion: 1\nagents:\n  reviewer:\n    portable: agents/reviewer.md\n    categories: [\"code review\"]\n",
		"leading hyphen":    "schemaVersion: 1\ncategories: [-x]\nagents:\n  reviewer:\n    portable: agents/reviewer.md\n",
		"double hyphen":     "schemaVersion: 1\ncategories: [a--b]\nagents:\n  reviewer:\n    portable: agents/reviewer.md\n",
		"empty":             "schemaVersion: 1\ncategories: [\"\"]\nagents:\n  reviewer:\n    portable: agents/reviewer.md\n",
		"not a list":        "schemaVersion: 1\ncategories: security\nagents:\n  reviewer:\n    portable: agents/reviewer.md\n",
		"mapping not slugs": "schemaVersion: 1\ncategories: [{slug: security}]\nagents:\n  reviewer:\n    portable: agents/reviewer.md\n",
	} {
		t.Run(name, func(t *testing.T) {
			tree := memTree(t, map[string]string{
				"agnthub.yaml":       manifest,
				"agents/reviewer.md": portable("reviewer", "Reviews"),
			})
			_, err := discovery.Discover(tree, harness.Default(), discovery.DefaultLimits())
			var manifestErr *discovery.ManifestError
			if !errors.As(err, &manifestErr) {
				t.Fatalf("want ManifestError, got %v", err)
			}
		})
	}
}

func TestLayoutDiscoveryHasNoCategories(t *testing.T) {
	inv := discover(t, memTree(t, map[string]string{
		"agents/reviewer.md": portable("reviewer", "Reviews"),
	}))
	if len(inv.Categories) != 0 || len(inv.CategoriesFor(inv.Find("reviewer"))) != 0 {
		t.Fatalf("no manifest means no declared categories, got %v", inv.Categories)
	}
}
