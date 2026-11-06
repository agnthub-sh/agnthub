package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
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

// Without a terminal, install needs --harness, and uninstall --yes or --harness.
func TestMCPNeedsAChoiceWithoutATerminal(t *testing.T) {
	e := newMCPEnv(t)
	for _, args := range [][]string{
		{"mcp", "install"}, {"mcp", "install", "--yes"}, {"mcp", "install", "--json"}, {"mcp", "uninstall"},
	} {
		code, out, errOut := e.run(t, nil, args...)
		if code != 1 || !strings.Contains(errOut, "--harness") {
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

// Interactive install lists detected harnesses and omp profiles: the ones without
// agnthub preselected, installed ones marked and unselected.
func TestMCPInstallPrompt(t *testing.T) {
	e := newMCPEnv(t)
	if code, _, errOut := e.run(
		t,
		nil,
		"mcp",
		"install",
		"--harness",
		"omp",
		"--profile",
		"default",
		"--yes",
	); code != 0 {
		t.Fatal(errOut)
	}
	prompter := &scriptedPrompter{selections: [][]string{nil}}
	code, out, errOut := e.run(t, prompter, "mcp", "install")
	if code != 0 || len(prompter.offered) != 1 {
		t.Fatalf("exit %d\n%s%s", code, out, errOut)
	}
	var labels []string
	for _, option := range prompter.offered[0] {
		mark := " "
		if option.Selected {
			mark = "x"
		}
		labels = append(labels, mark+" "+strings.Join(strings.Fields(option.Label), " "))
	}
	want := []string{
		"x cursor Cursor",
		"  omp omp · profile default (already installed)",
		"x omp omp · profile work",
	}
	if strings.Join(labels, "\n") != strings.Join(want, "\n") {
		t.Fatalf("offered:\n%s", strings.Join(labels, "\n"))
	}
	if !strings.Contains(out, "+ install") || !strings.Contains(out, "= already installed") ||
		!strings.Contains(out, "Not detected: claude, opencode, codex, copilot, gemini") {
		t.Fatalf("output:\n%s", out)
	}
	// Everything installed: nothing to ask.
	prompter = &scriptedPrompter{}
	if code, out, _ := e.run(
		t,
		prompter,
		"mcp",
		"install",
	); code != 0 ||
		len(prompter.offered) != 0 ||
		!strings.Contains(out, "already installed") {
		t.Fatalf("exit %d, %d prompts\n%s", code, len(prompter.offered), out)
	}
}

// uninstall removes only agnthub's entries, every one found with --yes.
func TestMCPUninstall(t *testing.T) {
	e := newMCPEnv(t)
	code, out, _ := e.run(t, nil, "mcp", "uninstall", "--yes")
	if code != 0 || strings.TrimSpace(out) != "No agnthub MCP installs detected." {
		t.Fatalf("exit %d\n%s", code, out)
	}
	codex := filepath.Join(e.home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(codex), 0o750); err != nil {
		t.Fatal(err)
	}
	legacy := "[mcp_servers.other]\ncommand = \"x\"\n\n[mcp_servers.agnthub]\ncommand = \"npx\"\n" +
		"args = [\"-y\", \"agnthub-cli\", \"mcp\"]\n"
	if err := os.WriteFile(codex, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	foreign := `{"mcpServers": {"agnthub": {"url": "https://example.com/mine"}}}`
	cursor := filepath.Join(e.home, ".cursor", "mcp.json")
	if err := os.WriteFile(cursor, []byte(foreign), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := e.run(t, nil, "mcp", "install", "--harness", "omp", "--yes"); code != 0 {
		t.Fatal(errOut)
	}
	prompter := &scriptedPrompter{selections: [][]string{nil}}
	code, out, errOut := e.run(t, prompter, "mcp", "uninstall")
	if code != 0 || len(prompter.offered) != 1 || len(prompter.offered[0]) != 3 {
		t.Fatalf("exit %d %v\n%s%s", code, prompter.offered, out, errOut)
	}
	for _, option := range prompter.offered[0] {
		if !option.Selected {
			t.Fatalf("not preselected: %s", option.Label)
		}
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
	if !strings.Contains(out, "- remove") {
		t.Fatalf("%s", out)
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
