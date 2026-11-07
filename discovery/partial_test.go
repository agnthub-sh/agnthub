package discovery_test

import (
	"errors"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"testing"

	"github.com/agnthub-sh/agnthub/discovery"
	"github.com/agnthub-sh/agnthub/harness"
	"github.com/agnthub-sh/agnthub/source"
)

// partialTree serves a MemTree as a source.Partial that holds no file content
// until it is fetched, like a sparse Git snapshot (unknown sizes) or the
// registry's listing (known sizes). Reading content that was not fetched first
// fails the test: every file discovery reads must be planned.
type partialTree struct {
	*source.MemTree
	t          *testing.T
	knownSizes bool
	fetched    map[string]bool
	batches    [][]string
	fetchErr   error
}

func newPartial(t *testing.T, files map[string]string, knownSizes bool) *partialTree {
	t.Helper()
	return &partialTree{
		MemTree: memTree(t, files), t: t, knownSizes: knownSizes, fetched: map[string]bool{},
	}
}

func (p *partialTree) describe(name string, entry source.Entry) source.Entry {
	if entry.Kind == source.EntryFile && !p.knownSizes && !p.fetched[name] {
		entry.Size = source.UnknownSize
	}
	return entry
}

func (p *partialTree) Stat(name string) (source.Entry, error) {
	entry, err := p.MemTree.Stat(name)
	if err != nil {
		return entry, err
	}
	cleaned, _ := source.CleanPath(name)
	return p.describe(cleaned, entry), nil
}

func (p *partialTree) ReadDir(dir string) ([]source.Entry, error) {
	entries, err := p.MemTree.ReadDir(dir)
	cleaned, _ := source.CleanPath(dir)
	for i, entry := range entries {
		name := entry.Name
		if cleaned != "." {
			name = cleaned + "/" + name
		}
		entries[i] = p.describe(name, entry)
	}
	return entries, err
}

func (p *partialTree) Fetched(name string) bool {
	entry, err := p.MemTree.Stat(name)
	if err != nil || entry.Kind != source.EntryFile {
		return true
	}
	cleaned, _ := source.CleanPath(name)
	return p.fetched[cleaned]
}

func (p *partialTree) Fetch(names []string) error {
	if p.fetchErr != nil {
		return p.fetchErr
	}
	p.batches = append(p.batches, names)
	for _, name := range names {
		if entry, err := p.MemTree.Stat(name); err == nil && entry.Kind == source.EntryFile {
			p.fetched[name] = true
		}
	}
	return nil
}

func (p *partialTree) ReadFile(name string, limit int64) ([]byte, error) {
	cleaned, _ := source.CleanPath(name)
	if !p.fetched[cleaned] {
		p.t.Errorf("discovery read %s without fetching it first", name)
	}
	return p.MemTree.ReadFile(name, limit)
}

// recordingTree records the files a scan of a whole tree reads.
type recordingTree struct {
	*source.MemTree
	read map[string]bool
}

func (r *recordingTree) ReadFile(name string, limit int64) ([]byte, error) {
	r.read[name] = true
	return r.MemTree.ReadFile(name, limit)
}

// everyLayout is a repository with a file in every layout the registry
// declares, so a layout added later is covered here without editing the test.
// layoutFiles lists those native files.
func everyLayout(registry *harness.Registry) (files map[string]string, layoutFiles []string) {
	files = map[string]string{
		"AGENT.md":                  portable("root-agent", "Root"),
		"agents/planner.md":         portable("planner", "Plans"),
		"agents/auditor/AGENT.md":   portable("auditor", "Audits"),
		"agents/notes.txt":          "not a definition",
		"agents/assets/diagram.svg": "<svg/>",
		"README.md":                 "# repository\n",
		"docs/guide.md":             portable("guide", "Not in a layout"),
		"src/main.go":               "package main\n",
	}
	for _, adapter := range registry.All() {
		layout := adapter.Layout()
		files[layout.Dir+"/notes.txt"] = "not a definition"
		for i, suffix := range layout.Suffixes {
			id := string(adapter.ID()) + "-" + strconv.Itoa(i)
			name := layout.Dir + "/" + id + suffix
			files[name] = portable(id, "Native")
			layoutFiles = append(layoutFiles, name)
			if layout.Recursive {
				nested := layout.Dir + "/team/" + id + "-nested" + suffix
				files[nested] = portable(id+"-nested", "Nested")
				layoutFiles = append(layoutFiles, nested)
			}
		}
	}
	return files, layoutFiles
}

func pluginRepository() map[string]string {
	return map[string]string{
		".claude-plugin/marketplace.json": `{"name": "team", "plugins": [
			{"name": "listed", "source": "./plugins/listed"},
			{"name": "scanned", "source": "./plugins/scanned"},
			{"name": "remote", "source": {"source": "github", "repo": "acme/x"}}]}`,
		"plugins/listed/.claude-plugin/plugin.json": `{"name": "listed-tools",
			"agents": ["./reviewer.md", "./deep/fixer.md"]}`,
		"plugins/listed/reviewer.md":                  portable("reviewer", "Reviews"),
		"plugins/listed/deep/fixer.md":                portable("fixer", "Fixes"),
		"plugins/listed/README.md":                    "# not listed\n",
		"plugins/listed/agents/ignored.md":            portable("ignored", "Not listed"),
		"plugins/scanned/.claude-plugin/plugin.json":  `{"name": "scanned-tools"}`,
		"plugins/scanned/agents/writer.md":            portable("writer", "Writes"),
		"plugins/scanned/agents/review/editor.md":     portable("editor", "Edits"),
		"plugins/scanned/agents/notes.txt":            "not an agent",
		"plugins/unlisted/.claude-plugin/plugin.json": `{"name": "unlisted"}`,
		"plugins/unlisted/agents/hidden.md":           portable("hidden", "Unlisted"),
		".claude/agents/local.md":                     portable("local", "Local"),
		"docs/guide.md":                               "# unrelated\n",
	}
}

func manifestRepository() map[string]string {
	return map[string]string{
		"agnthub.yaml": `schemaVersion: 1
agents:
  reviewer:
    portable: defs/reviewer.md
    variants:
      claude:
        path: native/claude/reviewer.md
  planner:
    variants:
      opencode:
        path: native/opencode/planner.md
`,
		"defs/reviewer.md":           portable("reviewer", "Reviews"),
		"native/claude/reviewer.md":  portable("reviewer", "Claude reviewer"),
		"native/opencode/planner.md": "---\ndescription: Plans\n---\nPlan.\n",
		".claude/agents/ignored.md":  portable("ignored", "The manifest is authoritative"),
		"defs/unused.md":             portable("unused", "Not declared"),
	}
}

func rootPluginRepository() map[string]string {
	return map[string]string{
		".claude-plugin/plugin.json": `{"name": "solo", "agents": "./team/lead.md"}`,
		"team/lead.md":               portable("lead", "Leads"),
		"team/other.md":              portable("other", "Not listed"),
		"agents/portable.md":         portable("portable", "Portable"),
	}
}

// Discovery over a partial tree fetches exactly the files a scan of the whole
// tree reads, in a few batches, and finds the same inventory.
func TestPartialTreeFetchesExactlyWhatDiscoveryReads(t *testing.T) {
	registry := harness.Default()
	layouts, layoutFiles := everyLayout(registry)
	cases := map[string]struct {
		files      map[string]string
		maxBatches int
	}{
		"every layout": {layouts, 1},
		"plugins":      {pluginRepository(), 3},
		"manifest":     {manifestRepository(), 2},
		"root plugin":  {rootPluginRepository(), 2},
		"catalog":      {catalogRepository(), 2},
	}
	for name, tc := range cases {
		for _, knownSizes := range []bool{false, true} {
			t.Run(name+"/known sizes "+strconv.FormatBool(knownSizes), func(t *testing.T) {
				whole := &recordingTree{MemTree: memTree(t, tc.files), read: map[string]bool{}}
				want := discover(t, whole)
				partial := newPartial(t, tc.files, knownSizes)
				got := discover(t, partial)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("partial tree: %+v\nwhole tree: %+v", got, want)
				}
				fetched := slices.Sorted(maps.Keys(partial.fetched))
				read := slices.Sorted(maps.Keys(whole.read))
				if !slices.Equal(fetched, read) {
					t.Fatalf("fetched %v\nbut discovery reads %v", fetched, read)
				}
				if len(partial.batches) > tc.maxBatches {
					t.Fatalf("%d fetch rounds, want at most %d: %v",
						len(partial.batches), tc.maxBatches, partial.batches)
				}
				if name != "every layout" {
					return
				}
				for _, file := range layoutFiles {
					if !partial.fetched[file] {
						t.Errorf("layout file %s was not fetched", file)
					}
				}
				for _, file := range []string{"README.md", "docs/guide.md", "agents/notes.txt"} {
					if partial.fetched[file] {
						t.Errorf("%s is not a candidate but was fetched", file)
					}
				}
			})
		}
	}
}

// A plan that already exceeds a budget stops before fetching what it cannot
// read, and the scan's verdict is the budget.
func TestPartialTreePlanStopsAtTheBudget(t *testing.T) {
	files := map[string]string{}
	for i := range 50 {
		id := "agent-" + strconv.Itoa(i)
		files["agents/"+id+".md"] = portable(id, "Agent")
	}
	limits := discovery.DefaultLimits()
	limits.MaxCandidates = 10
	partial := newPartial(t, files, false)
	_, err := discovery.Discover(partial, harness.Default(), limits)
	if !errors.Is(err, discovery.ErrBudgetExceeded) {
		t.Fatalf("want ErrBudgetExceeded, got %v", err)
	}
	if len(partial.fetched) != 0 {
		t.Fatalf("fetched %d files of a scan that cannot complete", len(partial.fetched))
	}

	partial = newPartial(t, files, false)
	partial.fetchErr = source.ErrFetchBudget
	if _, err := discovery.Discover(
		partial,
		harness.Default(),
		discovery.DefaultLimits(),
	); !errors.Is(
		err,
		discovery.ErrBudgetExceeded,
	) {
		t.Fatalf("a refused fetch: want ErrBudgetExceeded, got %v", err)
	}
}
