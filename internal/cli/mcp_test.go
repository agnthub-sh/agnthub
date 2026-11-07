package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bytedance/sonic"

	"github.com/agnthub-sh/agnthub/internal/cli"
	"github.com/agnthub-sh/agnthub/platform/env"
)

// mcpRun runs agnthub with HOME=home in dir; prompter non-nil makes it interactive.
func mcpRun(
	t *testing.T,
	vars map[string]string,
	dir string,
	prompter cli.Prompter,
	args ...string,
) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	app := cli.NewApp(cli.App{
		Env:         env.FromMap(vars),
		WorkDir:     dir,
		Stdin:       strings.NewReader(""),
		Stdout:      &out,
		Stderr:      &errOut,
		Interactive: prompter != nil,
		Prompter:    prompter,
		Now:         func() time.Time { return time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC) },
		LookPath:    func(string) (string, error) { return "", os.ErrNotExist },
	})
	code := app.Run(context.Background(), args)
	return code, out.String(), errOut.String()
}

type mcpEnv struct {
	home, project string
	vars          map[string]string
}

func newMCPEnv(t *testing.T) *mcpEnv {
	t.Helper()
	base := t.TempDir()
	e := &mcpEnv{home: filepath.Join(base, "home"), project: filepath.Join(base, "project")}
	for _, dir := range []string{
		e.home, e.project, filepath.Join(e.home, ".omp", "profiles", "work"),
		filepath.Join(e.home, ".cursor"),
	} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	e.vars = map[string]string{"HOME": e.home}
	return e
}

func (e *mcpEnv) run(t *testing.T, prompter cli.Prompter, args ...string) (int, string, string) {
	t.Helper()
	return mcpRun(t, e.vars, e.project, prompter, args...)
}

type mcpJSON struct {
	Status  string `json:"status"`
	Results []struct {
		Harness    string `json:"harness"`
		Profile    string `json:"profile"`
		ConfigFile string `json:"configFile"`
		Before     string `json:"before"`
		Action     string `json:"action"`
		SignIn     string `json:"signIn"`
	} `json:"results"`
	NotDetected []string `json:"notDetected"`
}

func decodeMCP(t *testing.T, out string) mcpJSON {
	t.Helper()
	var report mcpJSON
	if err := sonic.UnmarshalString(out, &report); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	return report
}

func (r mcpJSON) actions() string {
	var parts []string
	for _, row := range r.Results {
		name := row.Harness
		if row.Profile != "" {
			name += "/" + row.Profile
		}
		parts = append(parts, name+"="+row.Action)
	}
	return strings.Join(parts, " ")
}

// Configurations that still start the removed local MCP server fail with the way to the
// hosted one and to `mcp install`, whatever flags they pass; help lists the command group.
func TestMCPPointsToTheHostedServer(t *testing.T) {
	h := newHarness(t)
	for _, args := range [][]string{{"mcp"}, {"mcp", "--api-url", "http://localhost:8080"}} {
		code, out := h.run(args...)
		if code != 1 || !strings.Contains(out, "https://mcp.agnthub.sh") ||
			!strings.Contains(out, "removed in v0.5.0") || !strings.Contains(out, "mcp install") {
			t.Fatalf("%v: exit %d: %s", args, code, out)
		}
	}
	if code, out := h.run("mcp", "--help"); code != 0 || !strings.Contains(out, "uninstall") {
		t.Fatalf("mcp --help: exit %d\n%s", code, out)
	}
	if _, out := h.run("--help"); !strings.Contains(out, "mcp") {
		t.Fatalf("help does not list mcp:\n%s", out)
	}
}

// Without a terminal, install and uninstall need --all or --harness (--yes alone
// is not enough), and a refused run writes nothing.
func TestMCPNeedsAChoiceWithoutATerminal(t *testing.T) {
	e := newMCPEnv(t)
	for _, args := range [][]string{
		{"mcp", "install"},
		{"mcp", "install", "--yes"},
		{"mcp", "uninstall"},
		{"mcp", "uninstall", "--yes"},
	} {
		code, out, errOut := e.run(t, nil, args...)
		if code != 1 || !strings.Contains(errOut, "--all") ||
			!strings.Contains(errOut, "--harness") {
			t.Fatalf("%v: exit %d\n%s%s", args, code, out, errOut)
		}
	}
	if entries, _ := os.ReadDir(e.home); len(entries) != 2 {
		t.Fatalf("a refused run wrote into the home directory: %v", entries)
	}
}

// --harness all was replaced by --all, which does not combine with --harness.
func TestMCPAllReplacesHarnessAll(t *testing.T) {
	e := newMCPEnv(t)
	for _, args := range [][]string{
		{"mcp", "install", "--harness", "all", "--yes"},
		{"mcp", "uninstall", "--harness", "all", "--yes"},
		{"mcp", "install", "--all", "--harness", "cursor", "--yes"},
	} {
		code, out, errOut := e.run(t, nil, args...)
		if code != 1 || !strings.Contains(errOut, "--all") {
			t.Fatalf("%v: exit %d\n%s%s", args, code, out, errOut)
		}
	}
	if entries, _ := os.ReadDir(e.home); len(entries) != 2 {
		t.Fatalf("a refused run wrote into the home directory: %v", entries)
	}
}

// omp gets the server in every profile; --profile narrows it.
func TestMCPInstallOMPProfiles(t *testing.T) {
	e := newMCPEnv(t)
	code, out, errOut := e.run(t, nil, "mcp", "install", "--profile", "work", "--json")
	if report := decodeMCP(t, out); code != 0 || report.actions() != "omp/work=install" {
		t.Fatalf("exit %d: %s\n%s", code, report.actions(), errOut)
	}
	code, out, _ = e.run(t, nil, "mcp", "install", "--harness", "omp", "--yes", "--json")
	report := decodeMCP(t, out)
	if code != 0 || report.actions() != "omp/default=install omp/work=unchanged" {
		t.Fatalf("exit %d: %s", code, report.actions())
	}
	if !strings.Contains(report.Results[0].SignIn, "/mcp reauth agnthub") {
		t.Fatalf("no sign-in step: %+v", report.Results[0])
	}
	if _, err := os.Stat(
		filepath.Join(e.home, ".omp", "profiles", "work", "agent", "mcp.json"),
	); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := e.run(
		t,
		nil,
		"mcp",
		"install",
		"--profile",
		"nope",
		"--yes",
	); code != 1 ||
		!strings.Contains(errOut, "does not exist") {
		t.Fatalf("a missing profile: exit %d %s", code, errOut)
	}
	if code, _, errOut := e.run(
		t,
		nil,
		"mcp",
		"install",
		"--harness",
		"claude",
		"--profile",
		"work",
	); code != 1 ||
		!strings.Contains(errOut, "--harness omp") {
		t.Fatalf("--profile without omp: exit %d %s", code, errOut)
	}
}

// A foreign agnthub entry is skipped with exit status 2 unless --force.
func TestMCPInstallForeignEntry(t *testing.T) {
	e := newMCPEnv(t)
	path := filepath.Join(e.home, ".cursor", "mcp.json")
	foreign := `{"mcpServers": {"agnthub": {"url": "https://example.com/mine"}}}`
	if err := os.WriteFile(path, []byte(foreign), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, _ := e.run(t, nil, "mcp", "install", "--harness", "cursor", "--yes")
	if content, _ := os.ReadFile(path); code != 2 || string(content) != foreign ||
		!strings.Contains(out, "! skipped") || !strings.Contains(out, "--force") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	code, out, _ = e.run(
		t,
		nil,
		"mcp",
		"install",
		"--harness",
		"cursor",
		"--yes",
		"--force",
		"--json",
	)
	if report := decodeMCP(t, out); code != 0 || report.actions() != "cursor=update" {
		t.Fatalf("exit %d %s", code, report.actions())
	}
}

// mcpPrompter answers like scriptedPrompter and records each confirmation.
type mcpPrompter struct {
	scriptedPrompter

	questions []string
}

func answers(selections [][]string, confirms ...bool) *mcpPrompter {
	return &mcpPrompter{
		scriptedPrompter: scriptedPrompter{selections: selections, confirms: confirms},
	}
}

func (p *mcpPrompter) Confirm(ctx context.Context, title, description string) (bool, error) {
	p.questions = append(p.questions, title+"\n"+description)
	return p.scriptedPrompter.Confirm(ctx, title, description)
}

// listed is the toggle list offered, one "x label" line per option ("x" when
// preselected), with spacing collapsed.
func (p *mcpPrompter) listed(t *testing.T) []string {
	t.Helper()
	if len(p.offered) != 1 {
		t.Fatalf("%d toggle lists, want 1", len(p.offered))
	}
	labels := make([]string, 0, len(p.offered[0]))
	for _, option := range p.offered[0] {
		mark := "-"
		if option.Selected {
			mark = "x"
		}
		labels = append(labels, mark+" "+strings.Join(strings.Fields(option.Label), " "))
	}
	return labels
}

func (e *mcpEnv) mustRun(t *testing.T, args ...string) {
	t.Helper()
	if code, out, errOut := e.run(t, nil, args...); code != 0 {
		t.Fatalf("%v: exit %d\n%s%s", args, code, out, errOut)
	}
}

// seedLegacyCodex gives Codex (detected through its directory) the local stdio
// server next to another server.
func (e *mcpEnv) seedLegacyCodex(t *testing.T) string {
	t.Helper()
	codex := filepath.Join(e.home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(codex), 0o750); err != nil {
		t.Fatal(err)
	}
	legacy := "[mcp_servers.other]\ncommand = \"x\"\n\n[mcp_servers.agnthub]\ncommand = \"npx\"\n" +
		"args = [\"-y\", \"agnthub-cli\", \"mcp\"]\n"
	if err := os.WriteFile(codex, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	return codex
}

func (e *mcpEnv) exists(t *testing.T, rel string) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(e.home, rel))
	return err == nil
}

const (
	cursorMCP  = ".cursor/mcp.json"
	ompMCP     = ".omp/agent/mcp.json"
	ompWorkMCP = ".omp/profiles/work/agent/mcp.json"
)

// install offers only the harnesses without the hosted server: a legacy entry as
// an update, the set-up ones named in one line; the toggle list is confirmed.
func TestMCPInstallOffersOnlyMissing(t *testing.T) {
	e := newMCPEnv(t)
	codex := e.seedLegacyCodex(t)
	e.mustRun(t, "mcp", "install", "--harness", "omp", "--profile", "default", "--yes")
	p := answers([][]string{nil}, true)
	code, out, errOut := e.run(t, p, "mcp", "install")
	if code != 0 || len(p.questions) != 1 {
		t.Fatalf("exit %d, %d confirmations\n%s%s", code, len(p.questions), out, errOut)
	}
	listed := p.listed(t)
	if len(listed) != 3 || !strings.HasPrefix(listed[0], "x codex") ||
		!strings.Contains(listed[0], "update") || !strings.HasPrefix(listed[1], "x cursor") ||
		!strings.HasPrefix(listed[2], "x omp") || !strings.Contains(listed[2], "work") {
		t.Fatalf("offered:\n%s", strings.Join(listed, "\n"))
	}
	if !strings.Contains(out, "Already set up: omp · default") ||
		!strings.Contains(out, "~ update") ||
		!strings.Contains(out, "+ install") ||
		strings.Contains(out, "= already installed") {
		t.Fatalf("output:\n%s", out)
	}
	if content, _ := os.ReadFile(
		codex,
	); !strings.Contains(string(content), "https://mcp.agnthub.sh") ||
		!e.exists(t, cursorMCP) ||
		!e.exists(t, ompWorkMCP) {
		t.Fatalf("not installed everywhere chosen:\n%s", content)
	}
}

// One missing harness is a yes/no question; declining exits 1 and writes nothing,
// --yes asks nothing.
func TestMCPInstallOneMissing(t *testing.T) {
	e := newMCPEnv(t)
	e.mustRun(t, "mcp", "install", "--harness", "omp", "--yes")
	p := answers(nil, false)
	code, out, errOut := e.run(t, p, "mcp", "install")
	if code != 1 || len(p.offered) != 0 || len(p.questions) != 1 ||
		!strings.Contains(
			p.questions[0],
			"cursor",
		) || !strings.Contains(errOut, "nothing was written") {
		t.Fatalf("exit %d, %d lists, %v\n%s%s", code, len(p.offered), p.questions, out, errOut)
	}
	if e.exists(t, cursorMCP) {
		t.Fatal("a declined install wrote cursor's configuration")
	}
	p = answers(nil)
	if code, out, errOut := e.run(t, p, "mcp", "install", "--yes"); code != 0 || p.prompts() != 0 ||
		!e.exists(t, cursorMCP) {
		t.Fatalf("--yes: exit %d, %d prompts\n%s%s", code, p.prompts(), out, errOut)
	}
}

// With every detected harness set up, install says so and asks nothing, in a
// terminal or not; --json reports them unchanged.
func TestMCPInstallNoneMissing(t *testing.T) {
	e := newMCPEnv(t)
	e.mustRun(t, "mcp", "install", "--all")
	p := answers(nil)
	code, out, _ := e.run(t, p, "mcp", "install")
	if code != 0 || p.prompts() != 0 ||
		!strings.Contains(out, "already set up in every detected harness") ||
		!strings.Contains(out, "cursor, omp · default, omp · work") {
		t.Fatalf("exit %d, %d prompts\n%s", code, p.prompts(), out)
	}
	if code, out, _ := e.run(t, nil, "mcp", "install", "--all", "--yes"); code != 0 ||
		!strings.Contains(out, "already set up in every detected harness") {
		t.Fatalf("--all --yes: exit %d\n%s", code, out)
	}
	code, out, _ = e.run(t, nil, "mcp", "install", "--json")
	report := decodeMCP(t, out)
	if code != 0 ||
		report.actions() != "cursor=unchanged omp/default=unchanged omp/work=unchanged" {
		t.Fatalf("--json: exit %d %s", code, report.actions())
	}
}

// --all skips the list, --yes the confirmation; --json and --all without a
// terminal ask nothing; a declined confirmation writes nothing.
func TestMCPInstallFlags(t *testing.T) {
	everything := "cursor=install omp/default=install omp/work=install"
	for _, tc := range []struct {
		name          string
		prompter      *mcpPrompter
		args          []string
		lists, asks   int
		code          int
		wantInstalled []string
	}{
		{
			"terminal", answers([][]string{nil}, true), nil, 1, 1, 0,
			[]string{cursorMCP, ompMCP, ompWorkMCP},
		},
		{
			"terminal-some", answers([][]string{{"0"}}, true), nil, 1, 1, 0,
			[]string{cursorMCP},
		},
		{"terminal-decline", answers([][]string{nil}, false), nil, 1, 1, 1, nil},
		{
			"yes", answers([][]string{nil}),
			[]string{"--yes"},
			1, 0, 0,
			[]string{cursorMCP, ompMCP, ompWorkMCP},
		},
		{
			"all", answers(nil, true),
			[]string{"--all"},
			0, 1, 0,
			[]string{cursorMCP, ompMCP, ompWorkMCP},
		},
		{
			"all-yes", answers(nil),
			[]string{"--all", "--yes"},
			0, 0, 0,
			[]string{cursorMCP, ompMCP, ompWorkMCP},
		},
		{
			"harness", answers(nil, true),
			[]string{"--harness", "cursor"},
			0, 1, 0,
			[]string{cursorMCP},
		},
		{
			"json-terminal", answers(nil),
			[]string{"--json"},
			0, 0, 0,
			[]string{cursorMCP, ompMCP, ompWorkMCP},
		},
		{
			"no-terminal-all", nil,
			[]string{"--all"},
			0, 0, 0,
			[]string{cursorMCP, ompMCP, ompWorkMCP},
		},
		{
			"no-terminal-json", nil,
			[]string{"--json"},
			0, 0, 0,
			[]string{cursorMCP, ompMCP, ompWorkMCP},
		},
		{
			"no-terminal-json-harness", nil,
			[]string{"--json", "--harness", "cursor"},
			0, 0, 0,
			[]string{cursorMCP},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newMCPEnv(t)
			var prompter cli.Prompter
			if tc.prompter != nil {
				prompter = tc.prompter
			}
			code, out, errOut := e.run(
				t,
				prompter,
				append([]string{"mcp", "install"}, tc.args...)...)
			if code != tc.code {
				t.Fatalf("exit %d\n%s%s", code, out, errOut)
			}
			if tc.prompter != nil &&
				(len(tc.prompter.offered) != tc.lists || len(tc.prompter.questions) != tc.asks) {
				t.Fatalf("%d lists and %d confirmations, want %d and %d",
					len(tc.prompter.offered), len(tc.prompter.questions), tc.lists, tc.asks)
			}
			for _, rel := range []string{cursorMCP, ompMCP, ompWorkMCP} {
				if want := slices.Contains(tc.wantInstalled, rel); e.exists(t, rel) != want {
					t.Errorf("%s written: %v, want %v", rel, !want, want)
				}
			}
			if slices.Contains(tc.args, "--json") && len(tc.wantInstalled) == 3 {
				if report := decodeMCP(t, out); report.actions() != everything {
					t.Fatalf("%s", report.actions())
				}
			}
		})
	}
}

// A foreign agnthub entry chosen in a terminal is replaced once confirmed.
func TestMCPInstallConfirmReplacesForeign(t *testing.T) {
	e := newMCPEnv(t)
	e.mustRun(t, "mcp", "install", "--harness", "omp", "--yes")
	path := filepath.Join(e.home, cursorMCP)
	foreign := `{"mcpServers": {"agnthub": {"url": "https://example.com/mine"}}}`
	if err := os.WriteFile(path, []byte(foreign), 0o600); err != nil {
		t.Fatal(err)
	}
	p := answers(nil, true)
	code, out, errOut := e.run(t, p, "mcp", "install")
	content, _ := os.ReadFile(path)
	if code != 0 || len(p.questions) != 1 ||
		!strings.Contains(p.questions[0], "example.com/mine") ||
		!strings.Contains(string(content), "https://mcp.agnthub.sh") {
		t.Fatalf("exit %d %v\n%s\n%s%s", code, p.questions, content, out, errOut)
	}
}

// uninstall finds agnthub's entries in any form (never a foreign one): none says
// so; one is a yes/no question; several are a toggle list, all preselected, then
// a confirmation; --all skips the list and --yes the confirmation.
func TestMCPUninstall(t *testing.T) {
	e := newMCPEnv(t)
	p := answers(nil)
	code, out, _ := e.run(t, p, "mcp", "uninstall")
	if code != 0 || p.prompts() != 0 ||
		strings.TrimSpace(out) != "No agnthub MCP installs detected." {
		t.Fatalf("none: exit %d\n%s", code, out)
	}
	if code, out, _ := e.run(t, nil, "mcp", "uninstall", "--all", "--yes"); code != 0 ||
		strings.TrimSpace(out) != "No agnthub MCP installs detected." {
		t.Fatalf("none with --all --yes: exit %d\n%s", code, out)
	}

	e.mustRun(t, "mcp", "install", "--harness", "cursor")
	p = answers(nil, false)
	code, out, errOut := e.run(t, p, "mcp", "uninstall")
	if content, _ := os.ReadFile(filepath.Join(e.home, cursorMCP)); code != 1 ||
		len(
			p.offered,
		) != 0 || len(p.questions) != 1 || !strings.Contains(p.questions[0], "cursor") ||
		!strings.Contains(string(content), "agnthub") {
		t.Fatalf("one, declined: exit %d %v\n%s%s", code, p.questions, out, errOut)
	}
	p = answers(nil, true)
	if code, out, errOut := e.run(t, p, "mcp", "uninstall"); code != 0 || len(p.offered) != 0 ||
		!strings.Contains(out, "- remove") {
		t.Fatalf("one: exit %d\n%s%s", code, out, errOut)
	}

	codex := e.seedLegacyCodex(t)
	foreign := `{"mcpServers": {"agnthub": {"url": "https://example.com/mine"}}}`
	cursor := filepath.Join(e.home, cursorMCP)
	if err := os.WriteFile(cursor, []byte(foreign), 0o600); err != nil {
		t.Fatal(err)
	}
	e.mustRun(t, "mcp", "install", "--harness", "omp")
	p = answers([][]string{nil}, true)
	code, out, errOut = e.run(t, p, "mcp", "uninstall")
	if code != 0 || len(p.questions) != 1 {
		t.Fatalf("several: exit %d\n%s%s", code, out, errOut)
	}
	listed := p.listed(t)
	if len(listed) != 3 || !strings.HasPrefix(listed[0], "x codex") ||
		!strings.HasPrefix(listed[1], "x omp") || !strings.HasPrefix(listed[2], "x omp") {
		t.Fatalf("offered:\n%s", strings.Join(listed, "\n"))
	}
	if content, _ := os.ReadFile(
		codex,
	); string(
		content,
	) != "[mcp_servers.other]\ncommand = \"x\"\n" {
		t.Fatalf("codex:\n%s", content)
	}
	if content, _ := os.ReadFile(cursor); string(content) != foreign {
		t.Fatalf("a foreign entry was removed:\n%s", content)
	}
	if e.exists(t, ompMCP) {
		if content, _ := os.ReadFile(
			filepath.Join(e.home, ompMCP),
		); strings.Contains(
			string(content),
			"agnthub",
		) {
			t.Fatalf("omp still has agnthub:\n%s", content)
		}
	}

	for _, tc := range []struct {
		args        []string
		prompter    *mcpPrompter
		lists, asks int
	}{
		{[]string{"--all"}, answers(nil, true), 0, 1},
		{[]string{"--yes"}, answers([][]string{nil}), 1, 0},
		{[]string{"--all", "--yes"}, nil, 0, 0},
		{[]string{"--json"}, nil, 0, 0},
	} {
		e.mustRun(t, "mcp", "install", "--harness", "omp")
		var prompter cli.Prompter
		if tc.prompter != nil {
			prompter = tc.prompter
		}
		code, out, errOut := e.run(t, prompter, append([]string{"mcp", "uninstall"}, tc.args...)...)
		if code != 0 {
			t.Fatalf("%v: exit %d\n%s%s", tc.args, code, out, errOut)
		}
		if tc.prompter != nil &&
			(len(tc.prompter.offered) != tc.lists || len(tc.prompter.questions) != tc.asks) {
			t.Fatalf("%v: %d lists and %d confirmations, want %d and %d", tc.args,
				len(tc.prompter.offered), len(tc.prompter.questions), tc.lists, tc.asks)
		}
		if content, _ := os.ReadFile(filepath.Join(e.home, ompWorkMCP)); strings.Contains(
			string(content), "agnthub") {
			t.Fatalf("%v: omp work still has agnthub:\n%s", tc.args, content)
		}
	}
}

// --api-key writes the key from AGNTHUB_API_KEY as a header, with a warning; it
// needs a key and is refused for project scope.
func TestMCPInstallAPIKey(t *testing.T) {
	e := newMCPEnv(t)
	if code, _, errOut := e.run(
		t,
		nil,
		"mcp",
		"install",
		"--harness",
		"cursor",
		"--api-key",
		"--yes",
	); code != 1 ||
		!strings.Contains(errOut, "auth login") {
		t.Fatalf("without a key: exit %d %s", code, errOut)
	}
	e.vars["AGNTHUB_API_KEY"] = "agh_testkey42"
	code, out, errOut := e.run(
		t,
		nil,
		"mcp",
		"install",
		"--harness",
		"cursor",
		"--api-key",
		"--yes",
	)
	content, _ := os.ReadFile(filepath.Join(e.home, ".cursor", "mcp.json"))
	if code != 0 || !strings.Contains(string(content), `"Authorization": "Bearer agh_testkey42"`) ||
		!strings.Contains(errOut, "plain text") || strings.Contains(out, "sign in") {
		t.Fatalf("exit %d\n%s\n%s%s", code, content, out, errOut)
	}
	if code, _, errOut := e.run(
		t,
		nil,
		"mcp",
		"install",
		"--harness",
		"cursor",
		"--api-key",
		"--scope",
		"project",
		"--yes",
	); code != 1 ||
		!strings.Contains(errOut, "project") {
		t.Fatalf("project scope: exit %d %s", code, errOut)
	}
}

// Project scope writes the project's file and is refused in the home directory.
func TestMCPProjectScope(t *testing.T) {
	e := newMCPEnv(t)
	code, _, errOut := e.run(
		t,
		nil,
		"mcp",
		"install",
		"--harness",
		"cursor",
		"--scope",
		"project",
		"--yes",
	)
	if code != 0 {
		t.Fatal(errOut)
	}
	if _, err := os.Stat(filepath.Join(e.project, ".cursor", "mcp.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.home, ".cursor", "mcp.json")); err == nil {
		t.Fatal("project scope wrote the user file")
	}
	code, _, errOut = mcpRun(
		t,
		e.vars,
		e.home,
		nil,
		"mcp",
		"install",
		"--harness",
		"cursor",
		"--scope",
		"project",
		"--yes",
	)
	if code != 1 || !strings.Contains(errOut, "home") {
		t.Fatalf("home as project: exit %d %s", code, errOut)
	}
	code, out, _ := e.run(t, nil, "mcp", "uninstall", "--scope", "project", "--yes", "--json")
	if report := decodeMCP(t, out); code != 0 || report.actions() != "cursor=remove" {
		t.Fatalf("exit %d %s", code, report.actions())
	}
}

// An unparseable configuration is never changed and fails the run.
func TestMCPUnparseableConfig(t *testing.T) {
	e := newMCPEnv(t)
	path := filepath.Join(e.home, ".cursor", "mcp.json")
	if err := os.WriteFile(path, []byte(`{"mcpServers": `), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, _ := e.run(t, nil, "mcp", "install", "--harness", "cursor,omp", "--yes")
	if content, _ := os.ReadFile(path); code != 1 || string(content) != `{"mcpServers": ` ||
		!strings.Contains(out, "! failed") || !strings.Contains(out, "+ install") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}
