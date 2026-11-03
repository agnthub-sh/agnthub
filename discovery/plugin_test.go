package discovery_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agnthub-sh/agnthub/agent"
	"github.com/agnthub-sh/agnthub/discovery"
	"github.com/agnthub-sh/agnthub/harness"
	"github.com/agnthub-sh/agnthub/source"
)

const marketplace = `{
  "name": "team",
  "owner": {"name": "Team"},
  "plugins": [
    {"name": "listed", "source": "./plugins/listed"},
    {"name": "scanned", "source": "./plugins/scanned"},
    {"name": "remote", "source": {"source": "github", "repo": "acme/remote-agents"}},
    {"name": "escape", "source": "./../outside"},
    {"name": "absolute", "source": "/etc"},
    {"name": "unprefixed", "source": "plugins/listed"},
    {"name": "missing", "source": "./plugins/missing"}
  ]
}`

func hasProblem(inv *discovery.Inventory, substr string) bool {
	for _, problem := range inv.Problems {
		if strings.Contains(problem.String(), substr) {
			return true
		}
	}
	return false
}

func pluginOf(t *testing.T, inv *discovery.Inventory, id string) string {
	t.Helper()
	def := inv.Find(id)
	if def == nil {
		t.Fatalf("%s not discovered: agents=%v problems=%v", id, inv.Agents, inv.Problems)
	}
	if def.Portable != nil || len(def.Variants) != 1 {
		t.Fatalf("%s: a plugin agent is a Claude Code Variant only: %+v", id, def)
	}
	return def.Variant(agent.Claude).Plugin
}

func TestMarketplacePluginsWithLocalSourcesAddClaudeVariants(t *testing.T) {
	inv := discover(t, memTree(t, map[string]string{
		".claude-plugin/marketplace.json": marketplace,
		"plugins/listed/.claude-plugin/plugin.json": `{"name": "listed-tools",
			"agents": ["./reviewer.md", "./deep/fixer.md"]}`,
		"plugins/listed/reviewer.md":   portable("reviewer", "Reviews"),
		"plugins/listed/deep/fixer.md": portable("fixer", "Fixes"),
		"plugins/listed/README.md":     "# not listed, not read\n",
		"plugins/listed/agents/ignored.md": portable(
			"ignored",
			"agents replaces the default dir",
		),
		"plugins/scanned/agents/planner.md":        portable("planner", "Plans"),
		"plugins/scanned/agents/review/auditor.md": portable("auditor", "Audits"),
		"outside/agents/evil.md":                   portable("evil", "Escaped"),
		"agents/helper.md":                         portable("helper", "Portable still works"),
	}))
	for id, plugin := range map[string]string{
		"reviewer": "listed-tools", "fixer": "listed-tools",
		"planner": "scanned", "auditor": "scanned",
	} {
		if got := pluginOf(t, inv, id); got != plugin {
			t.Errorf("%s: plugin %q, want %q", id, got, plugin)
		}
	}
	if inv.Find("helper") == nil || inv.Find("helper").Portable == nil {
		t.Error("portable layouts keep working beside plugins")
	}
	for _, id := range []string{"ignored", "evil"} {
		if inv.Find(id) != nil {
			t.Errorf("%s must not be discovered", id)
		}
	}
	if len(inv.Agents) != 5 {
		t.Errorf("agents = %d, want 5", len(inv.Agents))
	}
	for _, want := range []string{
		`plugin "remote": github source acme/remote-agents is outside this repository`,
		`plugin "escape": source "./../outside" escapes`,
		`plugin "absolute": source "/etc" must start with ./`,
		`plugin "unprefixed": source "plugins/listed" must start with ./`,
		`plugin "missing": source "./plugins/missing" not found`,
	} {
		if !hasProblem(inv, want) {
			t.Errorf("missing problem %q in %v", want, inv.Problems)
		}
	}
}

func TestRootPluginManifestMakesTheRepositoryAPlugin(t *testing.T) {
	inv := discover(t, memTree(t, map[string]string{
		".claude-plugin/plugin.json": `{"name": "solo"}`,
		"agents/reviewer.md": "---\nname: reviewer\ndescription: Reviews\n" +
			"tools: Read, Grep\n---\nReview.\n",
		"agents/nested/linter.md": portable("linter", "Lints"),
	}))
	// agents/ holds the plugin's Claude Code agents, not portable definitions.
	for _, id := range []string{"reviewer", "linter"} {
		if got := pluginOf(t, inv, id); got != "solo" {
			t.Errorf("%s: plugin %q, want solo", id, got)
		}
	}
}

func TestPluginAgentPathsStayInsideThePlugin(t *testing.T) {
	inv := discover(t, memTree(t, map[string]string{
		".claude-plugin/plugin.json": `{"name": "root", "agents": [
			"./ok.md", "./../escape.md", "/abs.md", "bare.md", "./dir", "./gone.md", "./notes.txt"
		]}`,
		"ok.md":          portable("ok", "Fine"),
		"bare.md":        portable("bare", "Needs ./"),
		"dir/inside.md":  portable("inside", "Directories are not accepted"),
		"notes.txt":      "text",
		"other/stray.md": portable("stray", "Not listed"),
	}))
	if pluginOf(t, inv, "ok") != "root" || len(inv.Agents) != 1 {
		t.Fatalf("only ok is a listed agent: %v", inv.Agents)
	}
	for _, want := range []string{
		`agents path "./../escape.md" escapes`,
		`agents path "/abs.md" must start with ./`,
		`agents path "bare.md" must start with ./`,
		`agents path "./dir" is not a .md file`,
		`agents path "./notes.txt" is not a .md file`,
		`gone.md: listed by plugin "root" but not found`,
	} {
		if !hasProblem(inv, want) {
			t.Errorf("missing problem %q in %v", want, inv.Problems)
		}
	}
}

func TestInvalidPluginManifestsAreProblemsNotFailures(t *testing.T) {
	inv := discover(t, memTree(t, map[string]string{
		".claude-plugin/marketplace.json": `{"plugins": [`,
		".claude/agents/reviewer.md":      portable("reviewer", "Native still works"),
	}))
	if inv.Find("reviewer") == nil || !hasProblem(inv, "marketplace.json: invalid JSON") {
		t.Fatalf("agents=%v problems=%v", inv.Agents, inv.Problems)
	}

	inv = discover(t, memTree(t, map[string]string{
		".claude-plugin/marketplace.json": `{"plugins": [
			{"source": "./a"},
			{"name": "nameless", "source": "./b"},
			{"name": "typed", "source": "./c"},
			{"name": "conflict", "source": "./d", "strict": false, "agents": "./x.md"},
			{"name": "entry-only", "source": "./e", "agents": ["./x.md"]},
			{"name": "number", "source": 7}
		]}`,
		"a/agents/a.md":                portable("a1", "A"),
		"b/.claude-plugin/plugin.json": `{"description": "no name"}`,
		"b/agents/b.md":                portable("b1", "B"),
		"c/.claude-plugin/plugin.json": `{"name": "typed", "agents": 3}`,
		"c/agents/c.md":                portable("c1", "C"),
		"d/.claude-plugin/plugin.json": `{"name": "conflict"}`,
		"d/x.md":                       portable("d1", "D"),
		"e/x.md":                       portable("e1", "E"),
	}))
	if len(inv.Agents) != 1 || pluginOf(t, inv, "e1") != "entry-only" {
		t.Fatalf("only the entry-only plugin loads: %v %v", inv.Agents, inv.Problems)
	}
	for _, want := range []string{
		"plugins[0] has no name",
		"b/.claude-plugin/plugin.json: requires a name",
		"c/.claude-plugin/plugin.json: agents must be a path",
		`plugin "conflict": both plugin.json and its strict: false`,
		`plugin "number": source must be a relative path`,
	} {
		if !hasProblem(inv, want) {
			t.Errorf("missing problem %q in %v", want, inv.Problems)
		}
	}
}

func TestPluginRootResolvesBareSourceNames(t *testing.T) {
	inv := discover(t, memTree(t, map[string]string{
		".claude-plugin/marketplace.json": `{"metadata": {"pluginRoot": "./plugins"},
			"plugins": [{"name": "fmt", "source": "formatter"}]}`,
		"plugins/formatter/agents/fmt.md": portable("fmt", "Formats"),
	}))
	if pluginOf(t, inv, "fmt") != "fmt" {
		t.Fatalf("problems=%v", inv.Problems)
	}
}

// Two plugins defining one id is ambiguous like any two Claude Code files,
// unless the files are identical: plugins often ship one agent in several
// plugins, or also in .claude/agents.
func TestPluginDuplicatesAreAmbiguousUnlessIdentical(t *testing.T) {
	same := portable("shared", "Same everywhere")
	inv := discover(t, memTree(t, map[string]string{
		".claude-plugin/marketplace.json": `{"plugins": [
			{"name": "one", "source": "./one"}, {"name": "two", "source": "./two"},
			{"name": "again", "source": "./one"}
		]}`,
		"one/agents/shared.md":        same,
		"two/agents/shared.md":        same,
		".claude/agents/shared.md":    same,
		"one/agents/clash.md":         portable("clash", "One"),
		"two/agents/clash.md":         portable("clash", "Two"),
		"two/agents/copy.md":          portable("copied", "In a plugin"),
		".claude/agents/copied.md":    portable("copied", "Edited copy"),
		".claude/agents/elsewhere.md": portable("elsewhere", "Native"),
	}))
	shared := inv.Find("shared")
	if shared == nil || shared.Variant(agent.Claude).Path != ".claude/agents/shared.md" {
		t.Fatalf("identical copies are one agent, the native file first: %+v %v", shared,
			inv.Problems)
	}
	for _, id := range []string{"clash", "copied"} {
		if inv.Find(id) != nil || len(inv.ProblemsFor(id)) == 0 {
			t.Errorf("%s: differing definitions must be ambiguous", id)
		}
	}
	if inv.Find("elsewhere") == nil {
		t.Error("an unrelated native agent stays available")
	}
}

func TestManifestStaysAuthoritativeOverPlugins(t *testing.T) {
	inv := discover(t, memTree(t, map[string]string{
		"agnthub.yaml":               "schemaVersion: 1\nagents:\n  planner:\n    portable: agents/planner.md\n",
		"agents/planner.md":          portable("planner", "Plans"),
		".claude-plugin/plugin.json": `{"name": "root"}`,
		"agents/plugin-only.md":      portable("plugin-only", "Not declared"),
	}))
	if len(inv.Agents) != 1 || inv.Find("planner").Portable == nil {
		t.Fatalf("agnthub.yaml decides the inventory: %v", inv.Agents)
	}
}

func TestPluginLinksAreSkipped(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	write := func(name, content string) {
		t.Helper()
		full := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	link := func(target, name string) {
		t.Helper()
		if err := os.Symlink(target, filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.md"),
		[]byte(portable("secret", "Outside")), 0o644); err != nil {
		t.Fatal(err)
	}
	write(".claude-plugin/marketplace.json", `{"plugins": [
		{"name": "linked", "source": "./linked"}, {"name": "real", "source": "./real"}]}`)
	write("real/.claude-plugin/plugin.json", `{"name": "real",
		"agents": ["./ok.md", "./secret.md", "./via/secret.md"]}`)
	write("real/ok.md", portable("ok", "Fine"))
	link(outside, "linked")
	link(filepath.Join(outside, "secret.md"), "real/secret.md")
	link(outside, "real/via")
	tree, err := source.OpenLocal(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tree.Close() }()
	inv := discover(t, tree)
	if len(inv.Agents) != 1 || pluginOf(t, inv, "ok") != "real" {
		t.Fatalf("agents=%v problems=%v", inv.Agents, inv.Problems)
	}
	for _, want := range []string{
		`source "./linked" is a symlink`, "real/secret.md: skipped symlink",
		"real/via/secret.md: skipped symlink",
	} {
		if !hasProblem(inv, want) {
			t.Errorf("missing problem %q in %v", want, inv.Problems)
		}
	}
}

func TestPluginAgentsCountAgainstTheBudget(t *testing.T) {
	files := map[string]string{".claude-plugin/plugin.json": `{"name": "big"}`}
	for _, id := range []string{"a", "b", "c"} {
		files["agents/"+id+".md"] = portable(id, "Agent")
	}
	limits := discovery.DefaultLimits()
	limits.MaxCandidates = 2
	_, err := discovery.Discover(memTree(t, files), harness.Default(), limits)
	if !errors.Is(err, discovery.ErrBudgetExceeded) {
		t.Fatalf("want ErrBudgetExceeded, got %v", err)
	}
}
