package discovery_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/heynemann/agnthub/internal/agent"
	"github.com/heynemann/agnthub/internal/discovery"
	"github.com/heynemann/agnthub/internal/harness"
	"github.com/heynemann/agnthub/internal/source"
)

func memTree(t *testing.T, files map[string]string) *source.MemTree {
	t.Helper()
	tree := source.NewMemTree()
	for name, content := range files {
		if err := tree.Add(name, source.EntryFile, []byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	return tree
}

func portable(name, description string) string {
	return "---\nname: " + name + "\ndescription: " + description + "\n---\nDo the work.\n"
}

func discover(t *testing.T, tree source.Tree) *discovery.Inventory {
	t.Helper()
	inv, err := discovery.Discover(tree, harness.Default(), discovery.DefaultLimits())
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	return inv
}

func TestDiscoverGroupsPortableAndNativeByIdentity(t *testing.T) {
	inv := discover(t, memTree(t, map[string]string{
		"agents/reviewer.md":         portable("reviewer", "Reviews"),
		"agents/planner/AGENT.md":    portable("planner", "Plans"),
		".claude/agents/reviewer.md": portable("reviewer", "Native reviewer"),
		"AGENTS.md":                  "# project instructions, not an agent\n",
	}))
	if len(inv.Agents) != 2 || len(inv.Problems) != 0 {
		t.Fatalf("agents=%v problems=%v", inv.Agents, inv.Problems)
	}
	reviewer := inv.Find("reviewer")
	if reviewer.Portable == nil || reviewer.Variant(agent.Claude) == nil {
		t.Fatalf("reviewer should have portable core and claude variant: %+v", reviewer)
	}
	if inv.Find("planner").Portable.Path != "agents/planner/AGENT.md" {
		t.Fatal("nested portable layout not discovered")
	}
}

func TestDiscoverRejectsAmbiguousIdentitiesInsteadOfPickingFirst(t *testing.T) {
	inv := discover(t, memTree(t, map[string]string{
		"agents/reviewer.md":          portable("reviewer", "One"),
		"agents/reviewer/AGENT.md":    portable("reviewer", "Two"),
		".claude/agents/a.md":         portable("planner", "A"),
		".claude/agents/nested/b.md":  portable("planner", "B"),
		".claude/agents/solo.md":      portable("solo", "Fine"),
		"agents/mismatch.md":          portable("other-name", "Mismatch"),
		".claude/agents/Bad Name.md":  portable("Bad Name", "Invalid id"),
		".claude/agents/no-header.md": "just text\n",
	}))
	if inv.Find("reviewer") != nil || inv.Find("planner") != nil {
		t.Fatal("ambiguous agents must not be installable")
	}
	if inv.Find("solo") == nil {
		t.Fatal("unambiguous agent should remain available")
	}
	for _, id := range []string{"reviewer", "planner", "mismatch", "Bad Name"} {
		if len(inv.ProblemsFor(id)) == 0 {
			t.Errorf("expected a problem for %s, got %v", id, inv.Problems)
		}
	}
}

func TestManifestIsAuthoritative(t *testing.T) {
	inv := discover(t, memTree(t, map[string]string{
		"agnthub.yaml": `schemaVersion: 1
agents:
  reviewer:
    portable: agents/reviewer.md
    variants:
      claude:
        path: native/claude-reviewer.md
    dependencies:
      agents:
        - agent: planner
`,
		"agents/reviewer.md":         portable("reviewer", "Reviews"),
		"native/claude-reviewer.md":  portable("reviewer", "Claude reviewer"),
		"agents/ignored.md":          portable("ignored", "Not declared"),
		".claude/agents/ignored2.md": portable("ignored2", "Not declared"),
	}))
	if !inv.Manifest || len(inv.Agents) != 1 || inv.Find("reviewer") == nil {
		t.Fatalf("manifest must define the whole inventory: %+v", inv.Agents)
	}
	reviewer := inv.Find("reviewer")
	if reviewer.Variant(agent.Claude).Path != "native/claude-reviewer.md" {
		t.Fatal("manifest variant path not used")
	}
	if len(reviewer.Dependencies.Agents) != 1 {
		t.Fatal("manifest dependencies not parsed")
	}
}

func TestManifestRejectsUnsafeOrUnknownContent(t *testing.T) {
	cases := map[string]map[string]string{
		"traversal": {
			"agnthub.yaml": "schemaVersion: 1\nagents:\n  a:\n    portable: ../outside.md\n",
		},
		"absolute": {
			"agnthub.yaml": "schemaVersion: 1\nagents:\n  a:\n    portable: /etc/passwd\n",
		},
		"unknown field such as hooks": {
			"agnthub.yaml": "schemaVersion: 1\nagents:\n  a:\n    portable: a.md\n    hooks: [\"curl x | sh\"]\n",
			"a.md":         portable("a", "A"),
		},
		"remote include": {
			"agnthub.yaml": "schemaVersion: 1\ninclude: https://example.com/x.yaml\nagents: {}\n",
		},
		"unsupported schema": {
			"agnthub.yaml": "schemaVersion: 2\nagents:\n  a:\n    portable: a.md\n",
			"a.md":         portable("a", "A"),
		},
		"unknown harness": {
			"agnthub.yaml": "schemaVersion: 1\nagents:\n  a:\n    variants:\n      emacs:\n        path: a.md\n",
			"a.md":         portable("a", "A"),
		},
	}
	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := discovery.Discover(
				memTree(t, files),
				harness.Default(),
				discovery.DefaultLimits(),
			)
			var manifestErr *discovery.ManifestError
			if !errors.As(err, &manifestErr) {
				t.Fatalf("want manifest error, got %v", err)
			}
		})
	}
}

func TestDiscoverEnforcesBudgets(t *testing.T) {
	limits := discovery.DefaultLimits()
	limits.MaxFileBytes = 64
	big := portable("big", strings.Repeat("x", 100))
	_, err := discovery.Discover(
		memTree(t, map[string]string{"agents/big.md": big}),
		harness.Default(),
		limits,
	)
	if !errors.Is(err, discovery.ErrBudgetExceeded) {
		t.Fatalf("want budget error for oversized file, got %v", err)
	}
	limits = discovery.DefaultLimits()
	limits.MaxCandidates = 1
	_, err = discovery.Discover(memTree(t, map[string]string{
		"agents/a.md": portable("a", "A"),
		"agents/b.md": portable("b", "B"),
	}), harness.Default(), limits)
	if !errors.Is(err, discovery.ErrBudgetExceeded) {
		t.Fatalf("want budget error for too many candidates, got %v", err)
	}
}

func TestLocalTreeNeverFollowsSymlinks(t *testing.T) {
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.md")
	if err := os.WriteFile(secret, []byte(portable("secret", "Leaked")), 0o600); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "agents"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(root, "agents", "secret.md")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, ".claude")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(root, "agnthub.yaml"),
		[]byte("schemaVersion: 1\nagents:\n  secret:\n    portable: agents/secret.md\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	tree, err := source.OpenLocal(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tree.Close() }()
	_, err = discovery.Discover(tree, harness.Default(), discovery.DefaultLimits())
	var manifestErr *discovery.ManifestError
	if !errors.As(err, &manifestErr) || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("manifest path through a symlink must be rejected, got %v", err)
	}
	if err := os.Remove(filepath.Join(root, "agnthub.yaml")); err != nil {
		t.Fatal(err)
	}
	inv, err := discovery.Discover(tree, harness.Default(), discovery.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if inv.Find("secret") != nil {
		t.Fatal("symlinked definition must not be read")
	}
}
