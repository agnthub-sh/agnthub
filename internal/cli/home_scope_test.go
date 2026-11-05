package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agnthub-sh/agnthub/internal/cli"
	"github.com/agnthub-sh/agnthub/platform/env"
	"github.com/agnthub-sh/agnthub/state"
)

// homeRun runs agnthub in dir with the given environment.
func homeRun(t *testing.T, vars map[string]string, dir string, args ...string) (int, string) {
	t.Helper()
	var out bytes.Buffer
	app := cli.NewApp(cli.App{
		Env:      env.FromMap(vars),
		WorkDir:  dir,
		Stdin:    strings.NewReader(""),
		Stdout:   &out,
		Stderr:   &out,
		Now:      func() time.Time { return time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC) },
		LookPath: func(string) (string, error) { return "", os.ErrNotExist },
	})
	return app.Run(context.Background(), args), out.String()
}

func requireAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("%s exists (err %v)", path, err)
	}
}

func requireFile(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

// homeDestinations are, per harness, where a user-scope install lands and the
// project directory a home-as-project install would have created.
var homeDestinations = []struct {
	harness, user, project string
}{
	{"claude", ".claude/agents/reviewer.md", ""},
	{"opencode", ".config/opencode/agents/reviewer.md", ".opencode"},
	{"codex", ".codex/agents/reviewer.toml", ""},
	{"copilot", ".copilot/agents/reviewer.agent.md", ".github"},
	{"cursor", ".cursor/agents/reviewer.md", ""},
	{"gemini", ".gemini/agents/reviewer.md", ""},
	{"omp", ".omp/agent/agents/reviewer.md", ".omp/agents"},
}

func TestAddInHomeInstallsIntoUserScope(t *testing.T) {
	for _, tc := range homeDestinations {
		t.Run(tc.harness, func(t *testing.T) {
			h := newHarness(t)
			h.write("agents/reviewer.md", reviewer)
			vars := map[string]string{"HOME": h.home}
			code, out := homeRun(t, vars, h.home, "add", h.source, "--harness", tc.harness, "--yes")
			if code != 0 {
				t.Fatalf("exit %d: %s", code, out)
			}
			requireFile(t, filepath.Join(h.home, tc.user))
			requireFile(t, filepath.Join(h.home, ".config", "agnthub", "global-lock.json"))
			requireAbsent(t, filepath.Join(h.home, state.LockFileName))
			if tc.project != "" {
				requireAbsent(t, filepath.Join(h.home, tc.project))
			}
			// list, check and remove in the home directory find the user-scope install.
			code, out = homeRun(t, vars, h.home, "list", "--json")
			if code != 0 || !strings.Contains(out, `"reviewer"`) {
				t.Fatalf("list in home: exit %d: %s", code, out)
			}
			if code, out = homeRun(t, vars, h.home, "check"); code != 0 {
				t.Fatalf("check in home: exit %d: %s", code, out)
			}
			code, out = homeRun(t, vars, h.home, "remove", "reviewer", "--yes")
			if code != 0 {
				t.Fatalf("remove in home: exit %d: %s", code, out)
			}
			requireAbsent(t, filepath.Join(h.home, tc.user))
		})
	}
}

func TestAddInHomeRefusesExplicitProjectScope(t *testing.T) {
	h := newHarness(t)
	h.write("agents/reviewer.md", reviewer)
	vars := map[string]string{"HOME": h.home}
	code, out := homeRun(t, vars, h.home,
		"add", h.source, "--harness", "omp", "--yes", "--global=false")
	if code == 0 {
		t.Fatalf("explicit project scope in home succeeded: %s", out)
	}
	entries, err := os.ReadDir(h.home)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("a refused install wrote into home: %v", entries)
	}
}

func TestAddAboveOrThroughLinkToHomeUsesUserScope(t *testing.T) {
	h := newHarness(t)
	h.write("agents/reviewer.md", reviewer)
	link := filepath.Join(filepath.Dir(h.home), "home-link")
	if err := os.Symlink(h.home, link); err != nil {
		t.Skip("symbolic links unavailable:", err)
	}
	vars := map[string]string{"HOME": h.home}
	for _, dir := range []string{filepath.Dir(h.home), link} {
		code, out := homeRun(t, vars, dir, "add", h.source, "--harness", "omp", "--yes")
		if code != 0 {
			t.Fatalf("in %s: exit %d: %s", dir, code, out)
		}
		requireAbsent(t, filepath.Join(dir, ".omp", "agents"))
		requireAbsent(t, filepath.Join(dir, state.LockFileName))
		requireFile(t, filepath.Join(h.home, ".omp", "agent", "agents", "reviewer.md"))
	}
}

func TestAddInDirectoryBelowHomeStaysProjectScope(t *testing.T) {
	h := newHarness(t)
	h.write("agents/reviewer.md", reviewer)
	dir := filepath.Join(h.home, "notes")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	code, out := homeRun(t, map[string]string{"HOME": h.home}, dir,
		"add", h.source, "--harness", "omp", "--yes")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	requireFile(t, filepath.Join(dir, ".omp", "agents", "reviewer.md"))
	requireFile(t, filepath.Join(dir, state.LockFileName))
	requireAbsent(t, filepath.Join(h.home, ".omp", "agent"))
}

// A project-scope install agnthub 0.4.7 made with the home directory as project
// stays listable and removable in the home directory, and is never rewritten there.
func TestHomeProjectInstallOfEarlierVersionsIsRemovable(t *testing.T) {
	h := newHarness(t)
	h.write("agents/reviewer.md", reviewer)
	base := filepath.Dir(h.home)
	shared := map[string]string{
		"XDG_CONFIG_HOME": filepath.Join(base, "config"),
		"XDG_STATE_HOME":  filepath.Join(base, "state"),
	}
	with := func(home string) map[string]string {
		vars := map[string]string{"HOME": home}
		for k, v := range shared {
			vars[k] = v
		}
		return vars
	}
	// Seen from another HOME, the home directory is a plain project: this is what
	// 0.4.7 wrote when run in the home directory.
	code, out := homeRun(t, with(filepath.Join(base, "other")), h.home,
		"add", h.source, "--harness", "omp", "--yes")
	if code != 0 {
		t.Fatalf("seeding: exit %d: %s", code, out)
	}
	stray := filepath.Join(h.home, ".omp", "agents", "reviewer.md")
	requireFile(t, stray)

	vars := with(h.home)
	code, out = homeRun(t, vars, h.home, "list", "--json")
	if code != 0 || !strings.Contains(out, stray) {
		t.Fatalf("list does not show the home project install: exit %d: %s", code, out)
	}
	if code, out = homeRun(t, vars, h.home, "update", "--yes"); code == 0 {
		t.Fatalf("update rewrote the home project install: %s", out)
	}
	if code, out = homeRun(t, vars, h.home, "restore", "--yes"); code == 0 {
		t.Fatalf("restore rewrote the home project install: %s", out)
	}
	code, out = homeRun(t, vars, h.home, "remove", "reviewer", "--yes")
	if code != 0 {
		t.Fatalf("remove: exit %d: %s", code, out)
	}
	requireAbsent(t, stray)
	requireAbsent(t, filepath.Join(h.home, state.LockFileName))
	ownership, err := state.LoadOwnership(state.OwnershipPath(shared["XDG_STATE_HOME"]))
	if err != nil {
		t.Fatal(err)
	}
	if len(ownership.Records) != 0 {
		t.Fatalf("ownership records remain: %+v", ownership.Records)
	}
	// With the stray lock gone, the home directory acts on user scope again.
	code, out = homeRun(t, vars, h.home, "add", h.source, "--harness", "omp", "--yes")
	if code != 0 {
		t.Fatalf("reinstall: exit %d: %s", code, out)
	}
	requireFile(t, filepath.Join(h.home, ".omp", "agent", "agents", "reviewer.md"))
	requireAbsent(t, stray)
}
