package resolve_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/heynemann/agnthub/agent"
	"github.com/heynemann/agnthub/discovery"
	"github.com/heynemann/agnthub/harness"
	"github.com/heynemann/agnthub/resolve"
	"github.com/heynemann/agnthub/source"
)

// repo is an in-memory source: a manifest plus portable files for each agent.
type repo struct {
	manifest string
	extra    map[string]string
}

func portable(id string) string {
	return fmt.Sprintf("---\nname: %s\ndescription: %s agent\n---\nDo %s work.\n", id, id, id)
}

func (r repo) snapshot(t *testing.T, loc source.Locator, revision string) *source.Snapshot {
	t.Helper()
	tree := source.NewMemTree()
	add := func(name, content string) {
		if err := tree.Add(name, source.EntryFile, []byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if r.manifest != "" {
		add(discovery.ManifestFile, r.manifest)
	}
	for name, content := range r.extra {
		add(name, content)
	}
	return source.NewSnapshot(loc, revision, tree, nil)
}

// manifest declares portable agents with the given YAML dependency blocks.
func manifest(agents map[string]string) (string, map[string]string) {
	var b strings.Builder
	b.WriteString("schemaVersion: 1\nagents:\n")
	files := map[string]string{}
	ids := make([]string, 0, len(agents))
	for id := range agents {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		fmt.Fprintf(&b, "  %s:\n    portable: agents/%s.md\n", id, id)
		if spec := agents[id]; spec != "" {
			b.WriteString(spec)
		}
		files["agents/"+id+".md"] = portable(id)
	}
	return b.String(), files
}

func newRepo(agents map[string]string) repo {
	m, files := manifest(agents)
	return repo{manifest: m, extra: files}
}

type remote struct {
	repo     repo
	revision string
}

// opener serves remote snapshots by "owner/repo#ref" and counts opens.
type opener struct {
	t       *testing.T
	remotes map[string]remote
	opens   map[string]int
}

func (o *opener) Open(_ context.Context, loc source.Locator) (*source.Snapshot, error) {
	key := loc.Display()
	o.opens[key]++
	r, ok := o.remotes[key]
	if !ok {
		return nil, fmt.Errorf("unknown ref %s", key)
	}
	return r.repo.snapshot(o.t, loc, r.revision), nil
}

type fixture struct {
	t      *testing.T
	opener *opener
	root   resolve.Root
}

func newFixture(t *testing.T, root repo, remotes map[string]remote) *fixture {
	t.Helper()
	snapshot := root.snapshot(t, source.Locator{Kind: source.KindLocal, Path: "/src/root"}, "")
	inventory, err := discovery.Discover(
		snapshot.Tree,
		harness.Default(),
		discovery.DefaultLimits(),
	)
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{
		t:      t,
		opener: &opener{t: t, remotes: remotes, opens: map[string]int{}},
		root:   resolve.Root{Snapshot: snapshot, Inventory: inventory},
	}
}

func (f *fixture) resolve(
	ids []string,
	harnesses ...agent.HarnessID,
) (*resolve.Closure, error) {
	f.t.Helper()
	selected := make([]*agent.Definition, 0, len(ids))
	for _, id := range ids {
		def := f.root.Inventory.Find(id)
		if def == nil {
			f.t.Fatalf("root has no agent %s", id)
		}
		selected = append(selected, def)
	}
	resolver := &resolve.Resolver{
		Opener:   f.opener,
		Registry: harness.Default(),
		Limits:   discovery.DefaultLimits(),
	}
	return resolver.Resolve(context.Background(), f.root, selected, harnesses)
}

// summary renders members as "agent@locator[harnesses]<requiredBy>" for comparison.
func summary(closure *resolve.Closure) []string {
	out := make([]string, 0, len(closure.Members))
	for _, m := range closure.Members {
		origin := "dep"
		if m.Direct {
			origin = "direct"
		}
		harnesses := make([]string, 0, len(m.Harnesses))
		for _, h := range m.Harnesses {
			harnesses = append(harnesses, string(h))
		}
		out = append(out, fmt.Sprintf("%s %s@%s [%s] <%s>",
			origin, m.Definition.ID, m.Snapshot.Locator.Display(),
			strings.Join(harnesses, ","), strings.Join(m.RequiredBy, ",")))
	}
	return out
}

func TestClosureFollowsSameSnapshotAndPinnedExternalSourcesTransitively(t *testing.T) {
	ext := remote{revision: "1111", repo: newRepo(map[string]string{
		"linter":      "    dependencies:\n      agents:\n        - agent: style-guide\n",
		"style-guide": "",
		"unrelated":   "",
	})}
	f := newFixture(t, newRepo(map[string]string{
		"reviewer": "    dependencies:\n      agents:\n        - agent: planner\n" +
			"        - agent: linter\n          source: acme/tools#v1\n",
		"planner": "    dependencies:\n      agents:\n        - agent: linter\n          source: acme/tools#v1\n",
	}), map[string]remote{"acme/tools#v1": ext})
	closure, err := f.resolve([]string{"reviewer"}, agent.Claude, agent.Codex)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = closure.Close() }()
	want := []string{
		"direct reviewer@/src/root [claude,codex] <>",
		"dep planner@/src/root [claude,codex] <reviewer>",
		"dep linter@acme/tools#v1 [claude,codex] <planner,reviewer>",
		"dep style-guide@acme/tools#v1 [claude,codex] <linter (acme/tools#v1)>",
	}
	if got := summary(closure); !slices.Equal(got, want) {
		t.Fatalf("closure:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if f.opener.opens["acme/tools#v1"] != 1 {
		t.Fatalf("external source must be opened once, got %d", f.opener.opens["acme/tools#v1"])
	}
}

func TestVariantDependenciesAddToCommonOnesForTheirHarnessOnly(t *testing.T) {
	root := newRepo(map[string]string{
		"reviewer": "    variants:\n      codex:\n        path: .codex/agents/reviewer.toml\n" +
			"        dependencies:\n          agents:\n            - agent: sandboxer\n" +
			"    dependencies:\n      agents:\n        - agent: planner\n",
		"planner":   "",
		"sandboxer": "",
	})
	root.extra[".codex/agents/reviewer.toml"] = "name = \"reviewer\"\ndescription = \"Reviews\"\n" +
		"developer_instructions = \"Review.\"\n"
	f := newFixture(t, root, nil)
	closure, err := f.resolve([]string{"reviewer"}, agent.Claude, agent.Codex, agent.OMP)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"direct reviewer@/src/root [claude,codex,omp] <>",
		"dep planner@/src/root [claude,codex,omp] <reviewer>",
		"dep sandboxer@/src/root [codex] <reviewer>",
	}
	if got := summary(closure); !slices.Equal(got, want) {
		t.Fatalf("closure:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestDirectlySelectedCompanionStaysDirect(t *testing.T) {
	f := newFixture(t, newRepo(map[string]string{
		"reviewer": "    dependencies:\n      agents:\n        - agent: planner\n",
		"planner":  "",
	}), nil)
	closure, err := f.resolve([]string{"reviewer", "planner"}, agent.Claude)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"direct reviewer@/src/root [claude] <>",
		"direct planner@/src/root [claude] <>",
	}
	if got := summary(closure); !slices.Equal(got, want) {
		t.Fatalf("closure: %v", got)
	}
}

func TestCyclesAreRejectedWithTheirPath(t *testing.T) {
	ext := remote{revision: "2222", repo: newRepo(map[string]string{
		"linter": "    dependencies:\n      agents:\n        - agent: reviewer\n          source: acme/root#main\n",
	})}
	rootDeps := map[string]string{
		"reviewer": "    dependencies:\n      agents:\n        - agent: linter\n          source: acme/tools#v1\n",
	}
	rootRepo := newRepo(rootDeps)
	f := newFixture(t, newRepo(map[string]string{
		"a": "    dependencies:\n      agents:\n        - agent: b\n",
		"b": "    dependencies:\n      agents:\n        - agent: c\n",
		"c": "    dependencies:\n      agents:\n        - agent: a\n",
	}), nil)
	_, err := f.resolve([]string{"a"}, agent.Claude)
	if !errors.Is(err, resolve.ErrCycle) || !strings.Contains(err.Error(), "a → b → c → a") {
		t.Fatalf("want cycle a → b → c → a, got %v", err)
	}

	// A cycle through an external source back into the selected repository.
	f = newFixture(t, rootRepo, map[string]remote{
		"acme/tools#v1":  ext,
		"acme/root#main": {revision: "3333", repo: rootRepo},
	})
	_, err = f.resolve([]string{"reviewer"}, agent.Claude)
	if !errors.Is(err, resolve.ErrCycle) {
		t.Fatalf("want cross-source cycle, got %v", err)
	}
}

func TestConflictingRevisionsOfOneDependencyIdentityFail(t *testing.T) {
	tools := newRepo(map[string]string{"linter": ""})
	root := newRepo(map[string]string{
		"reviewer": "    dependencies:\n      agents:\n        - agent: linter\n          source: acme/tools#v1\n" +
			"        - agent: auditor\n",
		"auditor": "    dependencies:\n      agents:\n        - agent: linter\n          source: acme/tools#v2\n",
	})
	f := newFixture(t, root, map[string]remote{
		"acme/tools#v1": {revision: "aaaa", repo: tools},
		"acme/tools#v2": {revision: "bbbb", repo: tools},
	})
	_, err := f.resolve([]string{"reviewer"}, agent.Claude)
	if !errors.Is(err, resolve.ErrConflict) {
		t.Fatalf("want conflicting revisions, got %v", err)
	}

	// Different refs that resolve to one immutable revision are the same content.
	f = newFixture(t, root, map[string]remote{
		"acme/tools#v1": {revision: "aaaa", repo: tools},
		"acme/tools#v2": {revision: "aaaa", repo: tools},
	})
	closure, err := f.resolve([]string{"reviewer"}, agent.Claude)
	if err != nil {
		t.Fatal(err)
	}
	if got := summary(closure); len(got) != 3 {
		t.Fatalf("one linter companion expected: %v", got)
	}
}

func TestMissingAmbiguousAndLocalDependenciesFail(t *testing.T) {
	f := newFixture(t, newRepo(map[string]string{
		"reviewer": "    dependencies:\n      agents:\n        - agent: ghost\n",
	}), nil)
	if _, err := f.resolve(
		[]string{"reviewer"},
		agent.Claude,
	); !errors.Is(
		err,
		resolve.ErrMissing,
	) {
		t.Fatalf("want missing dependency, got %v", err)
	}

	// An external source without a manifest that has two definitions for one id.
	ambiguous := repo{manifest: "", extra: map[string]string{
		"agents/linter.md":        portable("linter"),
		"agents/linter/AGENT.md":  portable("linter"),
		".claude/agents/other.md": portable("other"),
	}}
	f = newFixture(t, newRepo(map[string]string{
		"reviewer": "    dependencies:\n      agents:\n        - agent: linter\n          source: acme/tools\n",
	}), map[string]remote{"acme/tools": {revision: "cccc", repo: ambiguous}})
	_, err := f.resolve([]string{"reviewer"}, agent.Claude)
	if !errors.Is(err, resolve.ErrMissing) || !strings.Contains(err.Error(), "linter") {
		t.Fatalf("want ambiguous dependency, got %v", err)
	}

	f = newFixture(t, newRepo(map[string]string{
		"reviewer": "    dependencies:\n      agents:\n        - agent: linter\n          source: ~/secrets\n",
	}), nil)
	if _, err := f.resolve([]string{"reviewer"}, agent.Claude); !errors.Is(err, resolve.ErrSource) {
		t.Fatalf("repository metadata must not name local paths, got %v", err)
	}
	if len(f.opener.opens) != 0 {
		t.Fatal("a local dependency source must never be opened")
	}
}

func TestSkillDependenciesJoinTheClosurePerHarness(t *testing.T) {
	f := newFixture(t, repo{
		manifest: "schemaVersion: 1\nagents:\n" +
			"  reviewer:\n    portable: agents/reviewer.md\n" +
			"    variants:\n      codex:\n        path: .codex/agents/reviewer.toml\n" +
			"        dependencies:\n          skills:\n" +
			"            - source: acme/skills#v1\n              skill: codex-only\n" +
			"    dependencies:\n      agents:\n        - agent: planner\n" +
			"  planner:\n    portable: agents/planner.md\n" +
			"    dependencies:\n      skills:\n        - source: acme/skills#v1\n" +
			"          skill: code-review\n          minVersion: \"1.2.0\"\n" +
			"          maxVersion: \"2.0.0\"\n",
		extra: map[string]string{
			"agents/reviewer.md": portable("reviewer"),
			"agents/planner.md":  portable("planner"),
			".codex/agents/reviewer.toml": "name = \"reviewer\"\ndescription = \"r\"\n" +
				"developer_instructions = \"Review.\"\n",
		},
	}, nil)
	closure, err := f.resolve([]string{"reviewer"}, agent.Claude, agent.Codex)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][]string{}
	for _, requirement := range closure.Skills {
		if requirement.Source.Display() != "acme/skills#v1" {
			t.Fatalf("skill source = %s", requirement.Source.Display())
		}
		for _, need := range requirement.Needs {
			got[requirement.Skill] = append(got[requirement.Skill],
				string(need.Harness)+":"+need.RequiredBy+":"+need.Bounds.String())
		}
	}
	want := map[string][]string{
		"code-review": {"claude:planner:>=1.2.0 <=2.0.0", "codex:planner:>=1.2.0 <=2.0.0"},
		"codex-only":  {"codex:reviewer:any version"},
	}
	for skill, needs := range want {
		if !slices.Equal(got[skill], needs) {
			t.Errorf("%s needs = %v, want %v", skill, got[skill], needs)
		}
	}
	if len(f.opener.opens) != 0 {
		t.Fatal("resolving skill requirements must not fetch skill sources")
	}
}

func TestInvalidOrConflictingSkillDependenciesFail(t *testing.T) {
	skill := func(source, name, bounds string) string {
		return "    dependencies:\n      skills:\n        - source: " + source +
			"\n          skill: " + name + "\n" + bounds
	}
	cases := map[string]struct {
		agents map[string]string
		want   error
	}{
		"non-semver bound": {
			agents: map[string]string{
				"reviewer": skill("acme/skills", "x", "          minVersion: \"1.0\"\n"),
			},
			want: resolve.ErrSkill,
		},
		"inverted bounds": {
			agents: map[string]string{"reviewer": skill("acme/skills", "x",
				"          minVersion: \"2.0.0\"\n          maxVersion: \"1.0.0\"\n")},
			want: resolve.ErrSkill,
		},
		"invalid name": {
			agents: map[string]string{"reviewer": skill("acme/skills", "../x", "")},
			want:   resolve.ErrSkill,
		},
		"local source": {
			agents: map[string]string{"reviewer": skill("./skills", "x", "")},
			want:   resolve.ErrSource,
		},
		"one name from two sources": {
			agents: map[string]string{
				"reviewer": "    dependencies:\n      agents:\n        - agent: planner\n" +
					"      skills:\n        - source: acme/skills#v1\n          skill: x\n",
				"planner": skill("other/skills#v1", "x", ""),
			},
			want: resolve.ErrConflict,
		},
		"one name at two refs": {
			agents: map[string]string{
				"reviewer": "    dependencies:\n      agents:\n        - agent: planner\n" +
					"      skills:\n        - source: acme/skills#v1\n          skill: x\n",
				"planner": skill("acme/skills#v2", "x", ""),
			},
			want: resolve.ErrConflict,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, newRepo(tc.agents), nil)
			if _, err := f.resolve([]string{"reviewer"}, agent.Claude); !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
		})
	}
}
