package discovery_test

import (
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/agnthub-sh/agnthub/discovery"
	"github.com/agnthub-sh/agnthub/harness"
)

func agentIDs(inv *discovery.Inventory) []string {
	ids := make([]string, 0, len(inv.Agents))
	for _, def := range inv.Agents {
		ids = append(ids, def.ID)
	}
	return ids
}

func discoverIncludingIgnored(t *testing.T, files map[string]string) *discovery.Inventory {
	t.Helper()
	inv, err := discovery.Discover(memTree(t, files), harness.Default(), discovery.DefaultLimits(),
		discovery.IncludeIgnored())
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	return inv
}

func TestIgnoreFileThatListsNoPathsIgnoresTheWholeSource(t *testing.T) {
	for name, content := range map[string]string{
		"empty":         "",
		"comments only": "# agnthub.sh, please skip this repository\n\n",
		"root":          "/\n",
	} {
		t.Run(name, func(t *testing.T) {
			files := map[string]string{
				discovery.IgnoreFile: content,
				"agents/reviewer.md": portable("reviewer", "Reviews"),
			}
			inv := discover(t, memTree(t, files))
			if len(inv.Agents) != 0 || len(inv.Problems) != 0 || !inv.Ignore.All {
				t.Fatalf("agents=%v problems=%v ignore=%+v", inv.Agents, inv.Problems, inv.Ignore)
			}
			inv = discoverIncludingIgnored(t, files)
			if got := agentIDs(inv); !slices.Equal(got, []string{"reviewer"}) || !inv.Ignore.All {
				t.Fatalf("IncludeIgnored: agents=%v ignore=%+v", got, inv.Ignore)
			}
		})
	}
}

func TestIgnoredPathsAreNotDiscovered(t *testing.T) {
	files := map[string]string{
		discovery.IgnoreFile: "# internal agents\n/.claude/agents/internal/\n" +
			"  agents/draft.md  \n./agents/scratch\n",
		"agents/reviewer.md":                 portable("reviewer", "Reviews"),
		"agents/draft.md":                    portable("draft", "Not ready"),
		"agents/scratch/AGENT.md":            portable("scratch", "Experiments"),
		"agents/draft.md.bak":                "not covered: a different name",
		".claude/agents/planner.md":          portable("planner", "Plans"),
		".claude/agents/internal/ops.md":     portable("ops", "Ours only"),
		".claude/agents/internal-tools/x.md": portable("tools", "Sibling, not under internal/"),
	}
	inv := discover(t, memTree(t, files))
	if got := agentIDs(inv); !slices.Equal(got, []string{"planner", "reviewer", "tools"}) {
		t.Fatalf("agents %v", got)
	}
	want := []string{".claude/agents/internal", "agents/draft.md", "agents/scratch"}
	if !reflect.DeepEqual(inv.Ignore, discovery.Ignore{Paths: want}) {
		t.Fatalf("ignore %+v", inv.Ignore)
	}
	inv = discoverIncludingIgnored(t, files)
	if got := agentIDs(inv); len(got) != 6 {
		t.Fatalf("IncludeIgnored agents %v", got)
	}
}

// An ignored duplicate is how an owner settles an ambiguous identity without a manifest.
func TestIgnoredDuplicateIsNotAmbiguous(t *testing.T) {
	files := map[string]string{
		"agents/reviewer.md":       portable("reviewer", "Reviews"),
		"agents/reviewer/AGENT.md": portable("reviewer", "Old copy"),
	}
	if inv := discover(t, memTree(t, files)); inv.Find("reviewer") != nil {
		t.Fatal("two portable reviewers must be ambiguous")
	}
	inv := discover(t, memTree(t, withFile(files, discovery.IgnoreFile, "agents/reviewer\n")))
	reviewer := inv.Find("reviewer")
	if len(inv.Problems) != 0 || reviewer == nil || reviewer.Description != "Reviews" {
		t.Fatalf("agents=%v problems=%v", inv.Agents, inv.Problems)
	}
}

func TestManifestIgnoreKey(t *testing.T) {
	layout := map[string]string{
		"agents/reviewer.md":   portable("reviewer", "Reviews"),
		"agents/internal.md":   portable("internal", "Ours only"),
		"agents/sub/AGENT.md":  portable("sub", "Nested"),
		"agents/sub/notes.txt": "x",
	}
	with := func(manifest string) map[string]string {
		files := map[string]string{discovery.ManifestFile: manifest}
		for name, content := range layout {
			files[name] = content
		}
		return files
	}

	t.Run("all needs no schemaVersion", func(t *testing.T) {
		inv := discover(t, memTree(t, with("ignore: all\n")))
		if len(inv.Agents) != 0 || !inv.Ignore.All {
			t.Fatalf("agents=%v ignore=%+v", inv.Agents, inv.Ignore)
		}
	})

	t.Run("paths without agents keep layout discovery", func(t *testing.T) {
		inv := discover(t, memTree(t, with(
			"categories: [security]\nignore:\n  - agents/internal.md\n  - agents/sub/\n")))
		if got := agentIDs(inv); !slices.Equal(got, []string{"reviewer"}) || inv.Manifest {
			t.Fatalf("agents=%v manifest=%v", got, inv.Manifest)
		}
		if !slices.Equal(inv.Categories, []string{"security"}) {
			t.Fatalf("categories %v", inv.Categories)
		}
	})

	t.Run("joins the ignore file", func(t *testing.T) {
		files := with("ignore: [agents/internal.md]\n")
		files[discovery.IgnoreFile] = "agents/sub\n"
		inv := discover(t, memTree(t, files))
		if got := agentIDs(inv); !slices.Equal(got, []string{"reviewer"}) {
			t.Fatalf("agents %v", got)
		}
	})

	t.Run("a declared agent at an ignored path rejects the manifest", func(t *testing.T) {
		files := with("schemaVersion: 1\nignore: [agents/internal.md]\n" +
			"agents:\n  internal:\n    portable: agents/internal.md\n")
		_, err := discovery.Discover(
			memTree(t, files),
			harness.Default(),
			discovery.DefaultLimits(),
		)
		var manifestErr *discovery.ManifestError
		if !errors.As(err, &manifestErr) {
			t.Fatalf("want ManifestError, got %v", err)
		}
		inv := discoverIncludingIgnored(t, files)
		if got := agentIDs(inv); !slices.Equal(got, []string{"internal"}) || !inv.Manifest {
			t.Fatalf("IncludeIgnored: agents=%v", got)
		}
	})

	t.Run("agents still need schemaVersion", func(t *testing.T) {
		files := with(
			"ignore: [agents/sub]\nagents:\n  reviewer:\n    portable: agents/reviewer.md\n",
		)
		_, err := discovery.Discover(
			memTree(t, files),
			harness.Default(),
			discovery.DefaultLimits(),
		)
		var manifestErr *discovery.ManifestError
		if !errors.As(err, &manifestErr) {
			t.Fatalf("want ManifestError, got %v", err)
		}
	})
}

func TestInvalidIgnoreRulesRejectTheSource(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"pattern in file":      {discovery.IgnoreFile: "agents/*.md\n"},
		"escape in file":       {discovery.IgnoreFile: "ok\n../elsewhere\n"},
		"empty list":           {discovery.ManifestFile: "ignore: []\n"},
		"unknown scalar":       {discovery.ManifestFile: "ignore: everything\n"},
		"mapping":              {discovery.ManifestFile: "ignore:\n  all: true\n"},
		"pattern in manifest":  {discovery.ManifestFile: "ignore: ['**/drafts']\n"},
		"no agents, no ignore": {discovery.ManifestFile: "categories: [security]\n"},
	} {
		t.Run(name, func(t *testing.T) {
			files["agents/reviewer.md"] = portable("reviewer", "Reviews")
			_, err := discovery.Discover(memTree(t, files), harness.Default(),
				discovery.DefaultLimits(), discovery.IncludeIgnored())
			var manifestErr *discovery.ManifestError
			var ignoreErr *discovery.IgnoreError
			if !errors.As(err, &manifestErr) && !errors.As(err, &ignoreErr) {
				t.Fatalf("want a ManifestError or IgnoreError, got %v", err)
			}
		})
	}
}

// A plugin that lists an ignored agent is not a broken plugin: no scan notes.
func TestIgnoredPluginPathsLeaveNoProblems(t *testing.T) {
	inv := discover(t, memTree(t, map[string]string{
		discovery.IgnoreFile: "plugins/internal\nplugins/tools/agents/secret.md\n",
		".claude-plugin/marketplace.json": `{"name": "m", "plugins": [` +
			`{"name": "tools", "source": "./plugins/tools"},` +
			`{"name": "internal", "source": "./plugins/internal"}]}`,
		"plugins/tools/.claude-plugin/plugin.json": `{"name": "tools", ` +
			`"agents": ["./agents/helper.md", "./agents/secret.md"]}`,
		"plugins/tools/agents/helper.md":    portable("helper", "Helps"),
		"plugins/tools/agents/secret.md":    portable("secret", "Ours only"),
		"plugins/internal/agents/ops.md":    portable("ops", "Ours only"),
		"plugins/internal/.claude-plugin/x": "{}",
	}))
	if got := agentIDs(inv); !slices.Equal(got, []string{"helper"}) || len(inv.Problems) != 0 {
		t.Fatalf("agents=%v problems=%v", got, inv.Problems)
	}
}

// Planning fetches the configuration first; what it ignores is never fetched.
func TestPlanningNeverFetchesIgnoredFiles(t *testing.T) {
	files := map[string]string{
		"agents/reviewer.md":    portable("reviewer", "Reviews"),
		"agents/internal.md":    portable("internal", "Ours only"),
		".claude/agents/ops.md": portable("ops", "Ours only"),
	}
	t.Run("all", func(t *testing.T) {
		files := withFile(files, discovery.IgnoreFile, "")
		partial := newPartial(t, files, false)
		inv, err := discovery.Discover(partial, harness.Default(), discovery.DefaultLimits())
		if err != nil || len(inv.Agents) != 0 {
			t.Fatalf("inv=%v err=%v", inv, err)
		}
		if want := [][]string{{discovery.IgnoreFile}}; !reflect.DeepEqual(partial.batches, want) {
			t.Fatalf("batches %v", partial.batches)
		}
	})
	t.Run("paths", func(t *testing.T) {
		files := withFile(files, discovery.IgnoreFile, "agents/internal.md\n.claude\n")
		partial := newPartial(t, files, false)
		inv, err := discovery.Discover(partial, harness.Default(), discovery.DefaultLimits())
		if err != nil || !slices.Equal(agentIDs(inv), []string{"reviewer"}) {
			t.Fatalf("inv=%v err=%v", inv, err)
		}
		for _, batch := range partial.batches {
			for _, name := range batch {
				if name == "agents/internal.md" || name == ".claude/agents/ops.md" {
					t.Fatalf("fetched ignored %s: %v", name, partial.batches)
				}
			}
		}
	})
}

func withFile(base map[string]string, name, content string) map[string]string {
	out := map[string]string{name: content}
	for k, v := range base {
		out[k] = v
	}
	return out
}
