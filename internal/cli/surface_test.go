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

	"github.com/heynemann/agnthub/agent"
	"github.com/heynemann/agnthub/internal/cli"
	"github.com/heynemann/agnthub/platform/env"
)

// runEnv runs the CLI noninteractively with extra environment variables.
func (h *harnessT) runEnv(vars map[string]string, args ...string) (int, string) {
	h.t.Helper()
	values := map[string]string{"HOME": h.home}
	for key, value := range vars {
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
	})
	return app.Run(context.Background(), args), out.String()
}

type listedJSON []struct {
	Agent     string `json:"agent"`
	Artifacts []struct {
		Harness string `json:"harness"`
	} `json:"artifacts"`
}

func (h *harnessT) listed(args ...string) map[string][]string {
	h.t.Helper()
	code, out := h.run(append([]string{"ls", "--json"}, args...)...)
	if code != 0 {
		h.t.Fatalf("ls exit %d: %s", code, out)
	}
	var listed listedJSON
	if err := sonic.UnmarshalString(out, &listed); err != nil {
		h.t.Fatalf("ls output is not JSON: %v\n%s", err, out)
	}
	byAgent := map[string][]string{}
	for _, entry := range listed {
		for _, artifact := range entry.Artifacts {
			byAgent[entry.Agent] = append(byAgent[entry.Agent], artifact.Harness)
		}
	}
	return byAgent
}

func TestSkillsStyleAliasesSelectorsAndFilters(t *testing.T) {
	h := newHarness(t)
	h.writeTwoAgents()

	code, out := h.run("a", h.source, "-l", "--json")
	var inventory struct {
		Agents []struct {
			ID string `json:"id"`
		} `json:"agents"`
	}
	if code != 0 || sonic.UnmarshalString(out, &inventory) != nil || len(inventory.Agents) != 2 {
		t.Fatalf("`a <source> -l --json` must list both agents, exit %d: %s", code, out)
	}
	if files := writtenUnder(t, h.project); len(files) != 0 {
		t.Fatalf("listing wrote %v", files)
	}

	code, out = h.run("add", h.source, "--agent", "*", "--harness", "claude", "--yes", "--json")
	if code != 0 {
		t.Fatalf("--agent '*' exit %d: %s", code, out)
	}
	if plan := decodePlan(t, out); plan.Status != "applied" || len(plan.Items) != 2 {
		t.Fatalf("--agent '*' must install every agent: %s", out)
	}

	if got := h.listed(); len(got) != 2 {
		t.Fatalf("ls lists %v, want both agents", got)
	}
	if got := h.listed("-H", "claude-code"); len(got["reviewer"]) != 1 ||
		len(got["planner"]) != 1 {
		t.Fatalf("ls -H claude-code lists %v", got)
	}
	if got := h.listed("--harness", "codex"); len(got) != 0 {
		t.Fatalf("ls --harness codex must list nothing, got %v", got)
	}

	if code, out = h.run("upgrade", "--json"); code != 0 {
		t.Fatalf("upgrade exit %d: %s", code, out)
	}
	if code, out = h.run("rm", "planner", "--yes", "--json"); code != 0 {
		t.Fatalf("rm exit %d: %s", code, out)
	}
	if got := h.listed(); len(got) != 1 || got["reviewer"] == nil {
		t.Fatalf("after rm planner, ls lists %v", got)
	}
}

func TestHarnessWildcardSelectsEverySupportedHarness(t *testing.T) {
	h := newHarness(t)
	h.writeTwoAgents()
	if err := os.MkdirAll(filepath.Join(h.home, ".omp", "profiles", "hep"), 0o750); err != nil {
		t.Fatal(err)
	}
	code, out := h.run("add", h.source, "--agent", "reviewer", "--harness", "*",
		"--extra", "omp.profile=hep", "--yes", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	seen := map[string]bool{}
	for _, item := range decodePlan(t, out).Items {
		seen[item.Harness] = true
	}
	if len(seen) != len(agent.AllHarnesses) {
		t.Fatalf("--harness '*' must select every supported harness, got %v", seen)
	}
}

func TestWildcardSelectorsCannotBeMixedWithNames(t *testing.T) {
	h := newHarness(t)
	h.writeTwoAgents()
	for _, args := range [][]string{
		{"--agent", "*", "--agent", "reviewer", "--harness", "claude", "--yes"},
		{"--agent", "reviewer", "--harness", "*", "--harness", "claude", "--yes"},
	} {
		if code, out := h.run(append([]string{"add", h.source}, args...)...); code == 0 {
			t.Fatalf("%v must be refused: %s", args, out)
		}
	}
	if files := writtenUnder(t, h.project); len(files) != 0 {
		t.Fatalf("refused selections wrote %v", files)
	}
}

// pickPrompter selects the options whose label starts with prefix and confirms.
type pickPrompter struct {
	prefix  string
	offered []cli.Option
}

func (p *pickPrompter) MultiSelect(
	_ context.Context, _, _ string, options []cli.Option,
) ([]string, error) {
	p.offered = options
	var chosen []string
	for _, option := range options {
		if strings.HasPrefix(option.Label, p.prefix) {
			chosen = append(chosen, option.Value)
		}
	}
	return chosen, nil
}

func (p *pickPrompter) Confirm(context.Context, string, string) (bool, error) {
	return true, nil
}

func TestRemoveWithoutNamesAsksWhichAgentsInATerminalOnly(t *testing.T) {
	h := newHarness(t)
	h.writeTwoAgents()
	if code, out := h.run("add", h.source, "--all"); code != 0 {
		t.Fatalf("install exit %d: %s", code, out)
	}
	if code, out := h.run("remove", "--yes"); code == 0 {
		t.Fatalf("noninteractive remove without names must be refused: %s", out)
	}
	if got := h.listed(); len(got) != 2 {
		t.Fatalf("refused remove changed the installation: %v", got)
	}

	prompter := &pickPrompter{prefix: "planner"}
	if code, out := h.runInteractive(prompter, "rm", "--harness", "claude"); code != 0 {
		t.Fatalf("interactive remove exit %d: %s", code, out)
	}
	if len(prompter.offered) != 2 {
		t.Fatalf("want both installed agents offered once, got %+v", prompter.offered)
	}
	got := h.listed()
	if len(got["reviewer"]) != len(agent.AllHarnesses) ||
		len(got["planner"]) != len(agent.AllHarnesses)-1 {
		t.Fatalf("want only planner's claude artifact removed, got %v", got)
	}
}

func TestMachineAndRedirectedOutputCarryNoEscapeSequences(t *testing.T) {
	h := newHarness(t)
	h.writeTwoAgents()
	forced := map[string]string{"CLICOLOR_FORCE": "1"}
	code, out := h.runEnv(forced, "add", h.source, "--agent", "reviewer", "--harness", "claude",
		"--yes", "--json")
	if code != 0 || strings.Contains(out, "\x1b") {
		t.Fatalf("--json must stay plain JSON even when color is forced, exit %d: %q", code, out)
	}
	decodePlan(t, out)
	if code, out = h.runEnv(forced, "ls", "--json"); code != 0 || strings.Contains(out, "\x1b") {
		t.Fatalf("ls --json must stay plain, exit %d: %q", code, out)
	}

	for name, vars := range map[string]map[string]string{
		"redirected": nil,
		"NO_COLOR":   {"NO_COLOR": "1"},
		"TERM=dumb":  {"TERM": "dumb"},
	} {
		for _, args := range [][]string{
			{"ls"},
			{"add", h.source, "-l"},
			{"add", h.source, "--agent", "planner", "--harness", "codex"},
			{"add", filepath.Join(h.source, "missing")},
			{"--help"},
			{"add", "--help"},
		} {
			_, out := h.runEnv(vars, args...)
			if strings.Contains(out, "\x1b") || strings.TrimSpace(out) == "" {
				t.Fatalf("%s %v: want plain, nonempty output, got %q", name, args, out)
			}
		}
	}
}

func TestHelpVersionAndCompletion(t *testing.T) {
	h := newHarness(t)
	code, out := h.run("--help")
	if code != 0 {
		t.Fatalf("--help exit %d: %s", code, out)
	}
	for _, command := range []string{"add", "list", "remove", "check", "update", "restore"} {
		if !strings.Contains(out, command) {
			t.Fatalf("help does not offer %s: %s", command, out)
		}
	}
	for _, args := range [][]string{{"--version"}, {"-v"}, {"completion", "bash"}} {
		if code, out := h.run(args...); code != 0 || strings.TrimSpace(out) == "" {
			t.Fatalf("%v exit %d: %q", args, code, out)
		}
	}
	if code, out := h.run("frobnicate"); code == 0 {
		t.Fatalf("an unknown command must fail: %s", out)
	}
}
