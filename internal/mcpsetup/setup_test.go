package mcpsetup

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agnthub-sh/agnthub/agent"
	"github.com/agnthub-sh/agnthub/harness"
	"github.com/agnthub-sh/agnthub/platform/env"
)

var update = flag.Bool("update", false, "rewrite testdata/golden from this run")

// notFound is a LookPath that finds no Harness command, so every Harness is
// configured by editing its file.
func notFound(string) (string, error) { return "", os.ErrNotExist }

func newSetup(t *testing.T, vars map[string]string) (*Setup, string) {
	t.Helper()
	base := t.TempDir()
	home := filepath.Join(base, "home")
	project := filepath.Join(base, "project")
	for _, dir := range []string{home, project} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	values := map[string]string{"HOME": home}
	for k, v := range vars {
		values[k] = strings.ReplaceAll(v, "$HOME", home)
	}
	return &Setup{
		Ctx: harness.Context{Env: env.FromMap(values), ProjectRoot: project, LookPath: notFound},
		Now: func() time.Time { return time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC) },
		Run: func(context.Context, string, []string, []string) ([]byte, error) {
			return nil, errors.New("unexpected command")
		},
	}, home
}

func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func readOrNone(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "(no file)\n"
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func scanOne(
	t *testing.T,
	s *Setup,
	id agent.HarnessID,
	scope harness.Scope,
	apiKey string,
) *Target {
	t.Helper()
	targets, err := s.Scan(context.Background(), []agent.HarnessID{id}, scope, nil, apiKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 {
		t.Fatalf("want one target, got %d", len(targets))
	}
	return targets[0]
}

// userFile is where each Harness keeps user-scope MCP servers under HOME.
var userFile = map[agent.HarnessID]string{
	agent.Claude:   ".claude.json",
	agent.OpenCode: ".config/opencode/opencode.json",
	agent.Codex:    ".codex/config.toml",
	agent.Copilot:  ".copilot/mcp-config.json",
	agent.Cursor:   ".cursor/mcp.json",
	agent.Gemini:   ".gemini/settings.json",
	agent.OMP:      ".omp/agent/mcp.json",
}

// jsonFixtures are the starting configurations of every JSON Harness; servers
// is replaced by each Harness's servers object path.
var jsonFixtures = map[string]string{
	"empty": "",
	"others": `{
  "theme": "dark",
  "mcpServers": {
    "other": {
      "url": "https://example.com/mcp"
    }
  }
}
`,
	"legacy-api-url": `{
  "mcpServers": {
    "agnthub": {
      "type": "http",
      "url": "https://api.agnthub.sh/mcp"
    },
    "other": {
      "url": "https://example.com/mcp"
    }
  }
}
`,
	"legacy-mcp-url": `{
  "mcpServers": {
    "other": {
      "url": "https://example.com/mcp"
    },
    "agnthub": {
      "url": "https://mcp.agnthub.sh/mcp"
    }
  }
}
`,
	"legacy-stdio": `{
  "mcpServers": {
    "agnthub": {
      "command": "npx",
      "args": ["-y", "agnthub-cli@latest", "mcp"]
    }
  }
}
`,
	"foreign": `{
  "mcpServers": {
    "agnthub": {
      "url": "https://example.com/not-agnthub"
    }
  }
}
`,
	"comments": `{
	// servers this machine uses
	"mcpServers": {
		/* the hosted registry */
		"agnthub": { "command": "npx", "args": ["agnthub-cli", "mcp"] }, // old
		"other": { "url": "https://example.com/mcp" }, // keep
	},
}
`,
	"unparseable": `{ "mcpServers": { "other": `,
}

// codexFixtures are Codex's.
var codexFixtures = map[string]string{
	"empty": "",
	"others": `# my settings
model = "o3"

[mcp_servers.other]
command = "x" # keep this comment
`,
	"legacy-api-url": `model = "o3"

[mcp_servers.agnthub]
url = "https://api.agnthub.sh/mcp"

# the other server
[mcp_servers.other]
command = "x"
`,
	"legacy-mcp-url": `[mcp_servers.other]
command = "x"

[mcp_servers.agnthub]
url = "https://mcp.agnthub.sh/mcp"
`,
	"legacy-stdio": `[mcp_servers.agnthub]
command = "npx"
args = [
  "-y",
  "agnthub-cli",
  "mcp",
]

[mcp_servers.agnthub.env]
FOO = "bar"

[profiles.fast]
model = "o4-mini"
`,
	"foreign": `[mcp_servers.agnthub]
command = "my-own-agnthub"
`,
	"comments": `# top
[mcp_servers.other]
command = "x"
args = ["""
[mcp_servers.agnthub]
not a table, a string
"""]
`,
	"unparseable": `[mcp_servers.agnthub
url = `,
}

// TestFileEdits runs install, install --force and uninstall on each fixture of
// each Harness and compares the actions and the resulting files with
// testdata/golden/<harness>/<fixture>.
func TestFileEdits(t *testing.T) {
	for _, id := range agent.AllHarnesses {
		fixtures := jsonFixtures
		if id == agent.Codex {
			fixtures = codexFixtures
		}
		names := append([]string{"missing", "installed"}, sortedKeys(fixtures)...)
		for _, name := range names {
			t.Run(string(id)+"/"+name, func(t *testing.T) {
				var got strings.Builder
				for _, op := range []string{"install", "install --force", "install --api-key", "uninstall"} {
					s, home := newSetup(t, nil)
					path := filepath.Join(home, userFile[id])
					switch name {
					case "missing":
					case "installed":
						tgt := scanOne(t, s, id, harness.ScopeGlobal, "")
						if res := s.Install(
							context.Background(),
							tgt,
							false,
						); res.Action != ActionInstall {
							t.Fatalf("setup install: %+v", res)
						}
					default:
						writeFile(t, path, fixture(id, fixtures[name]), 0o640)
					}
					before := readOrNone(t, path)
					apiKey := ""
					if op == "install --api-key" {
						apiKey = "agh_test0123"
					}
					tgt := scanOne(t, s, id, harness.ScopeGlobal, apiKey)
					var res Result
					if op == "uninstall" {
						res = s.Uninstall(context.Background(), tgt)
					} else {
						res = s.Install(context.Background(), tgt, op == "install --force")
					}
					after := readOrNone(t, path)
					fmt.Fprintf(&got, "=== %s: %s -> %s\n", op, tgt.State, res.Action)
					if res.Reason != "" {
						fmt.Fprintf(
							&got,
							"reason: %s\n",
							strings.ReplaceAll(res.Reason, home, "$HOME"),
						)
					}
					changed := after != before
					if changed && before != "(no file)\n" && res.Backup == "" {
						t.Errorf("%s: changed the file without a backup", op)
					}
					switch {
					case !changed:
						got.WriteString("(unchanged)\n")
					default:
						got.WriteString(after)
					}
					if changed && res.Backup != "" && readOrNone(t, res.Backup) != before {
						t.Errorf("%s: the backup is not the previous file", op)
					}
					if (res.Action == ActionSkip || res.Action == ActionFail) && changed {
						t.Errorf("%s: %s changed the file", op, res.Action)
					}
				}
				compareGolden(
					t,
					filepath.Join("testdata", "golden", string(id), name),
					got.String(),
				)
			})
		}
	}
}

// fixture adapts a JSON fixture to a Harness: OpenCode 1 keeps servers under
// "mcp" (OpenCode 2 under mcp.servers, see TestOpenCodeShapes).
func fixture(id agent.HarnessID, content string) string {
	if id == agent.OpenCode {
		return strings.ReplaceAll(content, `"mcpServers"`, `"mcp"`)
	}
	return content
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func compareGolden(t *testing.T, path, got string) {
	t.Helper()
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test ./internal/mcpsetup -update)", err)
	}
	if string(want) != got {
		t.Errorf("%s differs:\n--- want\n%s\n--- got\n%s", path, want, got)
	}
}

// An install keeps the file mode, writes through a symbolic link and backs the
// previous file up with its mode; a new file is private.
func TestWritesAreAtomicWithBackupAndMode(t *testing.T) {
	s, home := newSetup(t, nil)
	real := filepath.Join(home, "dotfiles", "mcp.json")
	writeFile(t, real, jsonFixtures["others"], 0o640)
	link := filepath.Join(home, ".cursor", "mcp.json")
	if err := os.MkdirAll(filepath.Dir(link), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	res := s.Install(
		context.Background(),
		scanOne(t, s, agent.Cursor, harness.ScopeGlobal, ""),
		false,
	)
	if res.Action != ActionInstall {
		t.Fatalf("%+v", res)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the symbolic link was replaced: %v", err)
	}
	info, err := os.Stat(real)
	if err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("mode %v, %v", info.Mode(), err)
	}
	backup, err := os.Stat(res.Backup)
	if err != nil || backup.Mode().Perm() != 0o640 ||
		readOrNone(t, res.Backup) != jsonFixtures["others"] {
		t.Fatalf("backup %s: %v", res.Backup, err)
	}
	if !strings.HasPrefix(
		res.Backup,
		filepath.Join(home, ".local", "state", "agnthub", "mcp-backups"),
	) {
		t.Fatalf("backup outside the state directory: %s", res.Backup)
	}
	entries, err := os.ReadDir(filepath.Dir(real))
	if err != nil || len(entries) != 1 {
		t.Fatalf("temporary files left behind: %v %v", entries, err)
	}

	res = s.Install(context.Background(), scanOne(t, s, agent.OMP, harness.ScopeGlobal, ""), false)
	if res.Action != ActionInstall || res.Backup != "" {
		t.Fatalf("%+v", res)
	}
	created, err := os.Stat(filepath.Join(home, ".omp", "agent", "mcp.json"))
	if err != nil || created.Mode().Perm() != 0o600 {
		t.Fatalf("new file mode %v, %v", created.Mode(), err)
	}
}

// A second install changes nothing.
func TestInstallIsIdempotent(t *testing.T) {
	for _, id := range agent.AllHarnesses {
		s, home := newSetup(t, nil)
		ctx := context.Background()
		if res := s.Install(
			ctx,
			scanOne(t, s, id, harness.ScopeGlobal, ""),
			false,
		); res.Action != ActionInstall {
			t.Fatalf("%s: %+v", id, res)
		}
		path := filepath.Join(home, userFile[id])
		first := readOrNone(t, path)
		again := scanOne(t, s, id, harness.ScopeGlobal, "")
		if again.State != Installed {
			t.Fatalf("%s: state after install %s", id, again.State)
		}
		if res := s.Install(
			ctx,
			again,
			false,
		); res.Action != ActionUnchanged ||
			readOrNone(t, path) != first {
			t.Fatalf("%s: second install %+v", id, res)
		}
	}
}

// Each Harness's configuration directory variable moves its user file.
func TestEnvironmentOverrides(t *testing.T) {
	cases := []struct {
		id   agent.HarnessID
		vars map[string]string
		want string
	}{
		{agent.Claude, map[string]string{"CLAUDE_CONFIG_DIR": "$HOME/cc"}, "cc/.claude.json"},
		{agent.OpenCode, map[string]string{"OPENCODE_CONFIG_DIR": "$HOME/oc"}, "oc/opencode.json"},
		{
			agent.OpenCode,
			map[string]string{"XDG_CONFIG_HOME": "$HOME/xdg"},
			"xdg/opencode/opencode.json",
		},
		{agent.Codex, map[string]string{"CODEX_HOME": "$HOME/cx"}, "cx/config.toml"},
		{agent.Copilot, map[string]string{"COPILOT_HOME": "$HOME/cp"}, "cp/mcp-config.json"},
		{
			agent.Gemini,
			map[string]string{"GEMINI_CLI_HOME": "$HOME/gm"},
			"gm/.gemini/settings.json",
		},
		{agent.OMP, map[string]string{"PI_CONFIG_DIR": ".alt"}, ".alt/agent/mcp.json"},
		{
			agent.OMP,
			map[string]string{"PI_CODING_AGENT_DIR": "$HOME/relocated"},
			"relocated/mcp.json",
		},
		// omp sets PI_CODING_AGENT_DIR for a named profile's session; the default
		// profile does not read it.
		{agent.OMP, map[string]string{
			"PI_CODING_AGENT_DIR": "$HOME/.omp/profiles/work/agent", "OMP_PROFILE": "work",
		}, ".omp/agent/mcp.json"},
	}
	for _, tc := range cases {
		s, home := newSetup(t, tc.vars)
		var targets []*Target
		var err error
		if tc.id == agent.OMP {
			targets, err = s.Scan(
				context.Background(),
				[]agent.HarnessID{agent.OMP},
				harness.ScopeGlobal,
				[]string{"default"},
				"",
			)
		} else {
			targets, err = s.Scan(
				context.Background(),
				[]agent.HarnessID{tc.id},
				harness.ScopeGlobal,
				nil,
				"",
			)
		}
		if err != nil {
			t.Fatal(err)
		}
		res := s.Install(context.Background(), targets[0], false)
		if want := filepath.Join(
			home,
			tc.want,
		); targets[0].Path != want ||
			res.Action != ActionInstall {
			t.Errorf("%s %v: %s %+v, want %s", tc.id, tc.vars, targets[0].Path, res, want)
		}
		if _, err := os.Stat(filepath.Join(home, tc.want)); err != nil {
			t.Errorf("%s %v: %v", tc.id, tc.vars, err)
		}
	}
	s, _ := newSetup(t, map[string]string{"CODEX_HOME": "relative"})
	if tgt := scanOne(t, s, agent.Codex, harness.ScopeGlobal, ""); tgt.Err == nil {
		t.Error("a relative CODEX_HOME has no single user file")
	}
}

// omp gets a target per profile; a named profile must exist.
func TestOMPProfiles(t *testing.T) {
	s, home := newSetup(t, nil)
	for _, name := range []string{"work", "hep", "Bad Name"} {
		if err := os.MkdirAll(filepath.Join(home, ".omp", "profiles", name), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	targets, err := s.Scan(
		context.Background(),
		[]agent.HarnessID{agent.OMP},
		harness.ScopeGlobal,
		nil,
		"",
	)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, tgt := range targets {
		rel, _ := filepath.Rel(home, tgt.Path)
		got = append(got, tgt.Profile+"="+rel)
	}
	want := []string{
		"default=" + filepath.FromSlash(".omp/agent/mcp.json"),
		"hep=" + filepath.FromSlash(".omp/profiles/hep/agent/mcp.json"),
		"work=" + filepath.FromSlash(".omp/profiles/work/agent/mcp.json"),
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if _, err := s.Scan(context.Background(), []agent.HarnessID{agent.OMP}, harness.ScopeGlobal,
		[]string{"missing"}, ""); err == nil {
		t.Fatal("a profile that does not exist must be refused")
	}
}

// Project scope writes each Harness's project file, one for every omp profile.
func TestProjectScope(t *testing.T) {
	want := map[agent.HarnessID]string{
		agent.Claude:   ".mcp.json",
		agent.OpenCode: "opencode.json",
		agent.Codex:    ".codex/config.toml",
		agent.Copilot:  ".github/mcp.json",
		agent.Cursor:   ".cursor/mcp.json",
		agent.Gemini:   ".gemini/settings.json",
		agent.OMP:      ".omp/mcp.json",
	}
	for id, rel := range want {
		s, home := newSetup(t, nil)
		if err := os.MkdirAll(filepath.Join(home, ".omp", "profiles", "work"), 0o750); err != nil {
			t.Fatal(err)
		}
		targets, err := s.Scan(
			context.Background(),
			[]agent.HarnessID{id},
			harness.ScopeProject,
			nil,
			"",
		)
		if err != nil || len(targets) != 1 || targets[0].Profile != "" {
			t.Fatalf("%s: %v %v", id, targets, err)
		}
		if res := s.Install(context.Background(), targets[0], false); res.Action != ActionInstall {
			t.Fatalf("%s: %+v", id, res)
		}
		path := filepath.Join(s.Ctx.ProjectRoot, filepath.FromSlash(rel))
		if !strings.Contains(readOrNone(t, path), URL) {
			t.Errorf("%s: %s has no agnthub entry", id, rel)
		}
		if entries, _ := os.ReadDir(home); len(entries) > 1 {
			t.Errorf("%s: project scope wrote into the home directory: %v", id, entries)
		}
	}
	// An existing opencode.jsonc is used rather than creating opencode.json.
	s, _ := newSetup(t, nil)
	jsonc := filepath.Join(s.Ctx.ProjectRoot, "opencode.jsonc")
	writeFile(t, jsonc, "{\n  // mine\n  \"mcp\": {}\n}\n", 0o644)
	tgt := scanOne(t, s, agent.OpenCode, harness.ScopeProject, "")
	if res := s.Install(
		context.Background(),
		tgt,
		false,
	); tgt.Path != jsonc ||
		res.Action != ActionInstall {
		t.Fatalf("%s %+v", tgt.Path, res)
	}
}

// OpenCode 2 nests servers under mcp.servers; OpenCode 1 (and an unknown version)
// gets mcp.<name>, which OpenCode 2 reads too.
func TestOpenCodeShapes(t *testing.T) {
	for _, tc := range []struct {
		version, existing, wantPath string
	}{
		{"v2.1.3", "", `"servers"`},
		{"1.0.20", "", `"mcp": {` + "\n    \"agnthub\""},
		{"", "", `"mcp": {` + "\n    \"agnthub\""},
		{"", `{"mcp": {"servers": {"x": {"type": "remote", "url": "https://x"}}}}`, `"servers"`},
		{"v2.1.3", `{"mcp": {"x": {"type": "remote", "url": "https://x"}}}`, `"agnthub":`},
	} {
		s, home := newSetup(t, nil)
		if tc.version != "" {
			s.Ctx.LookPath = func(name string) (string, error) {
				if name == "opencode" {
					return "/bin/opencode", nil
				}
				return "", os.ErrNotExist
			}
			s.Run = func(_ context.Context, _ string, _ []string, argv []string) ([]byte, error) {
				if slices.Equal(argv, []string{"/bin/opencode", "--version"}) {
					return []byte(tc.version + "\n"), nil
				}
				return nil, errors.New("unexpected command")
			}
		}
		path := filepath.Join(home, userFile[agent.OpenCode])
		if tc.existing != "" {
			writeFile(t, path, tc.existing, 0o600)
		}
		res := s.Install(
			context.Background(),
			scanOne(t, s, agent.OpenCode, harness.ScopeGlobal, ""),
			false,
		)
		got := readOrNone(t, path)
		if res.Action != ActionInstall || !strings.Contains(got, tc.wantPath) {
			t.Errorf("%q %q: %+v\n%s", tc.version, tc.existing, res, got)
		}
		if tc.version == "v2.1.3" && tc.existing != "" && strings.Contains(got, `"servers"`) {
			t.Errorf("OpenCode 1 servers in the file: agnthub must join them\n%s", got)
		}
	}
	// A v1 entry is moved when OpenCode 2's shape is written.
	s, home := newSetup(t, nil)
	path := filepath.Join(home, userFile[agent.OpenCode])
	writeFile(
		t,
		path,
		`{"mcp": {"servers": {}, "agnthub": {"type": "local", "command": ["npx", "agnthub-cli", "mcp"]}}}`,
		0o600,
	)
	tgt := scanOne(t, s, agent.OpenCode, harness.ScopeGlobal, "")
	if res := s.Install(
		context.Background(),
		tgt,
		false,
	); tgt.State != LegacyStdio ||
		res.Action != ActionUpdate {
		t.Fatalf("%s %+v", tgt.State, res)
	}
	if got := readOrNone(
		t,
		path,
	); strings.Count(got, `"agnthub"`) != 1 ||
		!strings.Contains(got, URL) {
		t.Fatalf("%s", got)
	}
}

// fakeCommand records Harness commands and edits the file as the Harness would.
type fakeCommand struct {
	calls [][]string
	dirs  []string
	run   func(argv []string) error
}

func (f *fakeCommand) Run(
	_ context.Context,
	dir string,
	_ []string,
	argv []string,
) ([]byte, error) {
	f.calls = append(f.calls, argv)
	f.dirs = append(f.dirs, dir)
	if f.run != nil {
		return []byte("output"), f.run(argv)
	}
	return nil, nil
}

func onPath(names ...string) func(string) (string, error) {
	return func(name string) (string, error) {
		if slices.Contains(names, name) {
			return "/usr/bin/" + name, nil
		}
		return "", os.ErrNotExist
	}
}

// Claude Code, Copilot CLI and Gemini CLI are configured with their own mcp
// command when it is on PATH: an older entry is removed first, and the file is
// read back.
func TestHarnessCommands(t *testing.T) {
	cases := []struct {
		id      agent.HarnessID
		add     string
		remove  string
		written string
	}{
		{
			agent.Claude, "claude mcp add --transport http --scope user agnthub " + URL,
			"claude mcp remove --scope user agnthub",
			`{"mcpServers": {"agnthub": {"type": "http", "url": "` + URL + `"}}}`,
		},
		{
			agent.Copilot,
			"copilot mcp add --transport http agnthub " + URL,
			"copilot mcp remove agnthub",
			`{"mcpServers": {"agnthub": {"tools": ["*"], "type": "http", "url": "` + URL + `"}}}`,
		},
		{
			agent.Gemini, "gemini mcp add --scope user --transport http agnthub " + URL,
			"gemini mcp remove --scope user agnthub",
			`{"mcpServers": {"agnthub": {"url": "` + URL + `", "type": "http"}}}`,
		},
	}
	for _, tc := range cases {
		s, home := newSetup(t, nil)
		s.Ctx.LookPath = onPath(string(tc.id))
		path := filepath.Join(home, userFile[tc.id])
		writeFile(t, path, jsonFixtures["legacy-stdio"], 0o600)
		fake := &fakeCommand{run: func(argv []string) error {
			if argv[2] == "add" {
				writeFile(t, path, tc.written, 0o600)
			} else {
				writeFile(t, path, `{"mcpServers": {}}`, 0o600)
			}
			return nil
		}}
		s.Run = fake.Run
		res := s.Install(context.Background(), scanOne(t, s, tc.id, harness.ScopeGlobal, ""), false)
		var calls []string
		for _, call := range fake.calls {
			calls = append(calls, strings.Join(call, " "))
		}
		want := []string{"/usr/bin/" + tc.remove, "/usr/bin/" + tc.add}
		if res.Action != ActionUpdate || res.Mechanism != "command" || !slices.Equal(calls, want) ||
			res.Backup == "" || fake.dirs[0] != home {
			t.Errorf("%s: %+v\n%v", tc.id, res, calls)
		}

		// With an API key the file is edited: the key never goes on a command line.
		fake.calls = nil
		res = s.Install(
			context.Background(),
			scanOne(t, s, tc.id, harness.ScopeGlobal, "agh_secret1"),
			false,
		)
		if res.Action != ActionUpdate || res.Mechanism != "file" || len(fake.calls) != 0 ||
			!strings.Contains(readOrNone(t, path), "Bearer agh_secret1") {
			t.Errorf("%s with a key: %+v %v", tc.id, res, fake.calls)
		}

		fake.calls = nil
		res = s.Uninstall(context.Background(), scanOne(t, s, tc.id, harness.ScopeGlobal, ""))
		if res.Action != ActionRemove || len(fake.calls) != 1 ||
			strings.Join(fake.calls[0], " ") != "/usr/bin/"+tc.remove {
			t.Errorf("%s uninstall: %+v %v", tc.id, res, fake.calls)
		}
	}
}

// A failing Harness command, or one that leaves the wrong entry, restores the
// previous file.
func TestHarnessCommandFailureRestoresTheFile(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(path string) func([]string) error
	}{
		{"add fails after remove", func(path string) func([]string) error {
			return func(argv []string) error {
				if argv[2] == "remove" {
					return os.WriteFile(path, []byte(`{"mcpServers": {}}`), 0o600)
				}
				return errors.New("exit status 1")
			}
		}},
		{"add writes something else", func(path string) func([]string) error {
			return func(argv []string) error {
				return os.WriteFile(path, []byte(`{"mcpServers": {"agnthub": {"url": "https://x"}}}`), 0o600)
			}
		}},
	} {
		s, home := newSetup(t, nil)
		s.Ctx.LookPath = onPath("claude")
		path := filepath.Join(home, ".claude.json")
		writeFile(t, path, jsonFixtures["legacy-api-url"], 0o600)
		fake := &fakeCommand{run: tc.run(path)}
		s.Run = fake.Run
		res := s.Install(
			context.Background(),
			scanOne(t, s, agent.Claude, harness.ScopeGlobal, ""),
			false,
		)
		if res.Action != ActionFail || readOrNone(t, path) != jsonFixtures["legacy-api-url"] {
			t.Errorf("%s: %+v\n%s", tc.name, res, readOrNone(t, path))
		}
	}
}

// Every legacy form and only those are agnthub's.
func TestClassify(t *testing.T) {
	sp := specs[agent.Claude]
	cases := []struct {
		entry string
		want  State
	}{
		{`{"type": "http", "url": "https://mcp.agnthub.sh"}`, Installed},
		{`{"type": "http", "url": "https://MCP.agnthub.sh/"}`, Installed},
		{`{"url": "https://mcp.agnthub.sh"}`, Outdated},
		{`{"type": "http", "url": "https://mcp.agnthub.sh/mcp"}`, Legacy},
		{`{"type": "sse", "url": "https://api.agnthub.sh/mcp"}`, Legacy},
		{`{"command": "npx", "args": ["-y", "agnthub-cli", "mcp"]}`, LegacyStdio},
		{
			`{"command": "npx", "args": ["agnthub-cli@0.4.16", "mcp", "--api-url", "x"]}`,
			LegacyStdio,
		},
		{`{"command": ["npx", "-y", "agnthub-cli", "mcp"]}`, LegacyStdio},
		{`{"command": "/usr/local/bin/agnthub", "args": ["mcp"]}`, LegacyStdio},
		{`{"command": "npx", "args": ["agnthub-cli", "add"]}`, Foreign},
		{`{"command": "my-agnthub"}`, Foreign},
		{`{"url": "https://agnthub.example.com/mcp"}`, Foreign},
	}
	for _, tc := range cases {
		doc, err := parseJSON([]byte(tc.entry))
		if err != nil {
			t.Fatal(err)
		}
		data, err := doc.data()
		if err != nil {
			t.Fatal(err)
		}
		if got := sp.classify(data, ""); got != tc.want {
			t.Errorf("%s: %s, want %s", tc.entry, got, tc.want)
		}
	}
	installed := map[string]any{"type": "http", "url": URL}
	if got := sp.classify(installed, "agh_key"); got != Outdated {
		t.Errorf("an entry without the requested key is %s", got)
	}
	installed["headers"] = map[string]any{"Authorization": "Bearer agh_key"}
	if got := sp.classify(installed, ""); got != Installed {
		t.Errorf("an entry with a key is %s without --api-key", got)
	}
}

// The TOML editor never touches a table inside a multi-line string.
func TestTOMLHeadersInStrings(t *testing.T) {
	src := codexFixtures["comments"]
	lines := splitTOML([]byte(src))
	if got := serverSections(lines, ServerName); len(got) != 0 {
		t.Fatalf("found agnthub tables inside a string: %v", got)
	}
	if !bytes.Contains([]byte(src), []byte("[mcp_servers.agnthub]")) {
		t.Fatal("fixture lost its decoy")
	}
}
