package cli_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bytedance/sonic"

	"github.com/agnthub-sh/agnthub/adoption"
	"github.com/agnthub-sh/agnthub/internal/cli"
	"github.com/agnthub-sh/agnthub/platform/env"
	"github.com/agnthub-sh/agnthub/source"
	"github.com/agnthub-sh/agnthub/state"
)

// remoteRepo is one pinned Git snapshot served by fakeGit.
type remoteRepo struct {
	revision string
	files    map[string]string
}

// fakeGit serves in-memory snapshots keyed by "owner/repo#ref".
type fakeGit map[string]remoteRepo

func (g fakeGit) Fetch(_ context.Context, loc source.Locator) (*source.Snapshot, error) {
	repo, ok := g[loc.Display()]
	if !ok {
		return nil, fmt.Errorf("unknown ref %s", loc.Display())
	}
	tree := source.NewMemTree()
	for name, content := range repo.files {
		if err := tree.Add(name, source.EntryFile, []byte(content)); err != nil {
			return nil, err
		}
	}
	return source.NewSnapshot(loc, repo.revision, tree, nil), nil
}

func (h *harnessT) runWith(git fakeGit, extraEnv map[string]string, args ...string) (int, string) {
	h.t.Helper()
	values := map[string]string{"HOME": h.home}
	for key, value := range extraEnv {
		values[key] = value
	}
	var out bytes.Buffer
	app := cli.NewApp(cli.App{
		Env:      env.FromMap(values),
		WorkDir:  h.project,
		Stdin:    strings.NewReader(""),
		Stdout:   &out,
		Stderr:   &out,
		Now:      func() time.Time { return time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC) },
		LookPath: func(string) (string, error) { return "", os.ErrNotExist },
		Opener:   source.Opener{Git: git},
		Adoption: offline,
	})
	return app.Run(context.Background(), args), out.String()
}

// offline reports nothing: fake Git repositories aren't real GitHub repositories, so
// tests never contact GitHub or the registry unless they configure their own servers.
var offline = &adoption.Reporter{}

func agentFile(id string) string {
	return fmt.Sprintf("---\nname: %s\ndescription: The %s agent\n---\nDo %s work.\n", id, id, id)
}

// toolsRepo is an external manifest whose linter needs a same-snapshot style-guide.
func toolsRepo(revision string) remoteRepo {
	return remoteRepo{revision: revision, files: map[string]string{
		"agnthub.yaml": "schemaVersion: 1\nagents:\n" +
			"  linter:\n    portable: agents/linter.md\n" +
			"    dependencies:\n      agents:\n        - agent: style-guide\n" +
			"  style-guide:\n    portable: agents/style-guide.md\n",
		"agents/linter.md":      agentFile("linter"),
		"agents/style-guide.md": agentFile("style-guide") + "Revision " + revision + ".\n",
	}}
}

const reviewerManifest = `schemaVersion: 1
agents:
  reviewer:
    portable: agents/reviewer.md
    dependencies:
      agents:
        - agent: planner
        - agent: linter
          source: acme/tools#v1
  planner:
    portable: agents/planner.md
`

func (h *harnessT) writeReviewerSource(manifest string) {
	h.write("agnthub.yaml", manifest)
	h.write("agents/reviewer.md", agentFile("reviewer"))
	h.write("agents/planner.md", agentFile("planner"))
}

func (h *harnessT) lock() *state.Lock {
	h.t.Helper()
	lock, err := state.LoadLock(filepath.Join(h.project, state.LockFileName))
	if err != nil {
		h.t.Fatal(err)
	}
	return lock
}

func TestAddInstallsTheDependencyClosureUnderEachCompanionsOwnSource(t *testing.T) {
	h := newHarness(t)
	h.writeReviewerSource(reviewerManifest)
	git := fakeGit{"acme/tools#v1": toolsRepo("1111111111111111")}

	code, out := h.runWith(git, nil, "add", h.source, "--agent", "reviewer",
		"--harness", "claude", "--harness", "codex", "--harness", "omp", "--yes", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	var plan struct {
		Items []struct {
			Agent      string   `json:"agent"`
			Harness    string   `json:"harness"`
			Origin     string   `json:"origin"`
			RequiredBy []string `json:"requiredBy"`
			Source     struct {
				Locator  string `json:"locator"`
				Ref      string `json:"ref"`
				Revision string `json:"revision"`
			} `json:"source"`
		} `json:"items"`
	}
	if err := sonic.UnmarshalString(out, &plan); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if len(plan.Items) != 12 {
		t.Fatalf("want 4 agents x 3 harnesses, got %d: %s", len(plan.Items), out)
	}
	for _, item := range plan.Items {
		if item.Agent == "style-guide" &&
			(item.Origin != "dependency" || item.Source.Locator != "acme/tools" ||
				item.Source.Ref != "v1" || item.Source.Revision != "1111111111111111" ||
				len(item.RequiredBy) != 1 || !strings.HasPrefix(item.RequiredBy[0], "linter")) {
			t.Fatalf("style-guide must show its pinned source and dependent: %+v", item)
		}
		if item.Agent == "reviewer" && item.Origin != "direct" {
			t.Fatalf("selected agent must stay direct: %+v", item)
		}
	}
	for _, rel := range []string{
		".claude/agents/planner.md", ".claude/agents/linter.md", ".claude/agents/style-guide.md",
		".codex/agents/linter.toml", ".codex/agents/style-guide.toml",
		".omp/agents/linter.md", ".omp/agents/style-guide.md",
	} {
		h.installed(rel)
	}
	origins := map[string]string{}
	for _, locked := range h.lock().Agents {
		origins[locked.Source.Locator+"::"+locked.Agent] = string(locked.Origin) +
			"@" + locked.Source.Revision
		if len(locked.Artifacts) != 3 {
			t.Fatalf("%s must be locked for all three harnesses: %+v", locked.Agent, locked)
		}
	}
	for _, key := range []string{"acme/tools::linter", "acme/tools::style-guide"} {
		if origins[key] != "dependency@1111111111111111" {
			t.Fatalf("companions must be locked under their own source: %v", origins)
		}
	}
	for key, origin := range origins {
		if strings.HasSuffix(key, "::reviewer") && origin != "direct@" {
			t.Fatalf("reviewer must be locked as direct: %v", origins)
		}
		if strings.HasSuffix(key, "::planner") && origin != "dependency@" {
			t.Fatalf("planner must be locked as dependency of the local source: %v", origins)
		}
	}
}

func TestDirectOriginIsNeverDowngradedByALaterDependencyInstall(t *testing.T) {
	h := newHarness(t)
	h.writeReviewerSource(reviewerManifest)
	git := fakeGit{"acme/tools#v1": toolsRepo("1111")}
	if code, out := h.runWith(git, nil, "add", h.source, "--agent", "planner",
		"--harness", "claude", "--yes"); code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if code, out := h.runWith(git, nil, "add", h.source, "--agent", "reviewer",
		"--harness", "claude", "--yes"); code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	for _, locked := range h.lock().Agents {
		if locked.Agent == "planner" && locked.Origin != state.OriginDirect {
			t.Fatalf("planner was installed directly first: %+v", locked)
		}
	}
	ownership, err := state.LoadOwnership(
		state.OwnershipPath(filepath.Join(h.home, ".local", "state")),
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range ownership.Records {
		if record.Agent == "planner" && record.Origin != state.OriginDirect {
			t.Fatalf("ownership must keep the direct origin: %+v", record)
		}
	}
}

func TestCompanionsInheritTheSelectedOMPProfile(t *testing.T) {
	h := newHarness(t)
	h.writeReviewerSource(reviewerManifest)
	profileRoot := filepath.Join(h.home, ".omp", "profiles", "work")
	if err := os.MkdirAll(profileRoot, 0o750); err != nil {
		t.Fatal(err)
	}
	git := fakeGit{"acme/tools#v1": toolsRepo("1111")}
	// A detected PI_PROFILE must not redirect companions of an explicit selection.
	code, out := h.runWith(git, map[string]string{"PI_PROFILE": "other"},
		"add", h.source, "--agent", "reviewer", "--harness", "omp", "--global",
		"--extra", "omp.profile=work", "--yes")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	agents := filepath.Join(profileRoot, "agent", "agents")
	for _, id := range []string{"reviewer", "planner", "linter", "style-guide"} {
		if _, err := os.Stat(filepath.Join(agents, id+".md")); err != nil {
			t.Fatalf("%s must install into the selected profile: %v\n%s", id, err, out)
		}
	}
	if _, err := os.Stat(filepath.Join(h.home, ".omp", "agent")); !os.IsNotExist(err) {
		t.Fatal("companions must not be routed to the default omp profile")
	}
	ownership, err := state.LoadOwnership(
		state.OwnershipPath(filepath.Join(h.home, ".local", "state")),
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range ownership.Records {
		if record.Settings["profile"] != "work" {
			t.Fatalf("every receipt must bind the selected profile: %+v", record)
		}
	}
}

func TestDependencyFailuresLeaveTheProjectUntouched(t *testing.T) {
	cases := map[string]struct {
		manifest string
		files    map[string]string
		git      fakeGit
		want     string
	}{
		"cycle": {
			manifest: "schemaVersion: 1\nagents:\n" +
				"  reviewer:\n    portable: agents/reviewer.md\n" +
				"    dependencies:\n      agents:\n        - agent: planner\n" +
				"  planner:\n    portable: agents/planner.md\n" +
				"    dependencies:\n      agents:\n        - agent: reviewer\n",
			want: "reviewer → planner → reviewer",
		},
		"conflicting revisions": {
			manifest: "schemaVersion: 1\nagents:\n" +
				"  reviewer:\n    portable: agents/reviewer.md\n" +
				"    dependencies:\n      agents:\n        - agent: planner\n" +
				"        - agent: linter\n          source: acme/tools#v1\n" +
				"  planner:\n    portable: agents/planner.md\n" +
				"    dependencies:\n      agents:\n        - agent: linter\n          source: acme/tools#v2\n",
			git:  fakeGit{"acme/tools#v1": toolsRepo("1111"), "acme/tools#v2": toolsRepo("2222")},
			want: "conflicting revisions",
		},
		"missing": {
			manifest: "schemaVersion: 1\nagents:\n" +
				"  reviewer:\n    portable: agents/reviewer.md\n" +
				"    dependencies:\n      agents:\n        - agent: ghost\n",
			want: "ghost",
		},
		"incompatible companion": {
			manifest: reviewerManifest,
			files: map[string]string{
				"agents/planner.md": "---\nname: planner\ndescription: x\ntools: Read\n---\nPlan.\n",
			},
			git:  fakeGit{"acme/tools#v1": toolsRepo("1111")},
			want: "incompatible",
		},
		"unfetchable skill source": {
			manifest: "schemaVersion: 1\nagents:\n" +
				"  reviewer:\n    portable: agents/reviewer.md\n" +
				"    dependencies:\n      skills:\n        - source: acme/skills#v1\n          skill: code-review\n",
			want: "acme/skills#v1",
		},
		"invalid skill bounds": {
			manifest: "schemaVersion: 1\nagents:\n" +
				"  reviewer:\n    portable: agents/reviewer.md\n" +
				"    dependencies:\n      skills:\n        - source: acme/skills#v1\n" +
				"          skill: code-review\n          minVersion: \"1.2\"\n",
			want: "minVersion",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.writeReviewerSource(tc.manifest)
			for rel, content := range tc.files {
				h.write(rel, content)
			}
			code, out := h.runWith(tc.git, nil, "add", h.source, "--agent", "reviewer",
				"--harness", "claude", "--harness", "codex", "--yes")
			if code == 0 || !strings.Contains(out, tc.want) {
				t.Fatalf("want refusal mentioning %q, exit %d: %s", tc.want, code, out)
			}
			entries, err := os.ReadDir(h.project)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("nothing may be written, found %v", entries)
			}
		})
	}
}
