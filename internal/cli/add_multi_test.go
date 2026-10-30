package cli_test

import (
	"bytes"
	"context"
	"errors"
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

// scriptedPrompter answers prompts in order: a nil selection keeps the preselected
// options, and running out of answers fails the prompt.
type scriptedPrompter struct {
	selections [][]string
	confirms   []bool
	offered    [][]cli.Option
	confirmed  int
}

func (p *scriptedPrompter) MultiSelect(
	_ context.Context,
	_, _ string,
	options []cli.Option,
) ([]string, error) {
	p.offered = append(p.offered, options)
	if len(p.selections) == 0 {
		return nil, errors.New("unexpected selection prompt")
	}
	answer := p.selections[0]
	p.selections = p.selections[1:]
	if answer != nil {
		return answer, nil
	}
	var preselected []string
	for _, option := range options {
		if option.Selected {
			preselected = append(preselected, option.Value)
		}
	}
	return preselected, nil
}

func (p *scriptedPrompter) Confirm(context.Context, string, string) (bool, error) {
	p.confirmed++
	if len(p.confirms) == 0 {
		return false, errors.New("unexpected confirmation prompt")
	}
	answer := p.confirms[0]
	p.confirms = p.confirms[1:]
	return answer, nil
}

func (p *scriptedPrompter) prompts() int {
	return len(p.offered) + p.confirmed
}

// runInteractive runs the CLI as a terminal user answering with prompter.
func (h *harnessT) runInteractive(prompter cli.Prompter, args ...string) (int, string) {
	h.t.Helper()
	var out bytes.Buffer
	app := cli.NewApp(cli.App{
		Env:         env.FromMap(map[string]string{"HOME": h.home}),
		WorkDir:     h.project,
		Stdin:       strings.NewReader(""),
		Stdout:      &out,
		Stderr:      &out,
		Interactive: true,
		Prompter:    prompter,
		Now:         func() time.Time { return time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC) },
		LookPath:    func(string) (string, error) { return "", os.ErrNotExist },
	})
	return app.Run(context.Background(), args), out.String()
}

type planOutput struct {
	Status    string `json:"status"`
	Scope     string `json:"scope"`
	Harnesses []struct {
		ID       string `json:"id"`
		Settings []struct {
			Key    string `json:"key"`
			Value  string `json:"value"`
			Source string `json:"source"`
		} `json:"settings"`
	} `json:"harnesses"`
	Items []struct {
		Agent             string            `json:"agent"`
		Harness           string            `json:"harness"`
		Settings          map[string]string `json:"settings"`
		EffectiveSettings map[string]string `json:"effectiveSettings"`
		Path              string            `json:"path"`
		Action            string            `json:"action"`
	} `json:"items"`
}

func decodePlan(t *testing.T, out string) planOutput {
	t.Helper()
	var plan planOutput
	if err := sonic.UnmarshalString(out, &plan); err != nil {
		t.Fatalf("output is not a JSON plan: %v\n%s", err, out)
	}
	return plan
}

func (h *harnessT) writeTwoAgents() {
	h.write("agents/reviewer.md", reviewer)
	h.write("agents/planner.md", strings.ReplaceAll(reviewer, "reviewer", "planner"))
}

// writtenUnder lists the regular files below dir.
func writtenUnder(t *testing.T, dir string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			files = append(files, path)
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return files
}

func TestAllRejectsExplicitSelectors(t *testing.T) {
	h := newHarness(t)
	h.writeTwoAgents()
	for _, args := range [][]string{
		{"--all", "--harness", "claude"},
		{"--all", "--agent", "reviewer"},
	} {
		code, out := h.run(append([]string{"add", h.source}, args...)...)
		if code == 0 || !strings.Contains(out, "--all") {
			t.Fatalf("%v must be rejected as contradictory, exit %d: %s", args, code, out)
		}
	}
	if files := writtenUnder(t, h.project); len(files) != 0 {
		t.Fatalf("rejected selections wrote %v", files)
	}
}

func TestAllInstallsEveryAgentIntoEverySupportedHarness(t *testing.T) {
	for _, scope := range []string{"project", "global"} {
		t.Run(scope, func(t *testing.T) {
			h := newHarness(t)
			h.writeTwoAgents()
			if err := os.MkdirAll(
				filepath.Join(h.home, ".omp", "profiles", "hep"),
				0o750,
			); err != nil {
				t.Fatal(err)
			}
			args := []string{"add", h.source, "--all", "--json", "--extra", "omp.profile=hep"}
			root, other := h.project, h.home
			if scope == "global" {
				args = append(args, "--global")
				root, other = h.home, h.project
			}
			code, out := h.run(args...)
			if code != 0 {
				t.Fatalf("exit %d: %s", code, out)
			}
			plan := decodePlan(t, out)
			if plan.Status != "applied" || plan.Scope != scope {
				t.Fatalf("want applied %s plan, got %s %s", scope, plan.Status, plan.Scope)
			}
			want := len(agent.AllHarnesses) * 2
			if len(plan.Items) != want {
				t.Fatalf("want %d items (2 agents × every harness), got %d", want, len(plan.Items))
			}
			seen := map[string]bool{}
			for _, item := range plan.Items {
				seen[item.Harness] = true
				if item.Action != "create" ||
					!strings.HasPrefix(item.Path, root+string(filepath.Separator)) {
					t.Fatalf("%s/%s: want create under %s, got %s %s",
						item.Agent, item.Harness, root, item.Action, item.Path)
				}
				if _, err := os.Stat(item.Path); err != nil {
					t.Fatalf("%s/%s not installed: %v", item.Agent, item.Harness, err)
				}
				isOMP := item.Harness == string(agent.OMP)
				if isOMP != (item.Settings["profile"] == "hep") || len(item.Settings) > 1 {
					t.Fatalf(
						"omp.profile must apply to omp only: %s %v",
						item.Harness,
						item.Settings,
					)
				}
				if isOMP != (item.EffectiveSettings["profile"] == "hep") {
					t.Fatalf(
						"effective settings misreported: %s %v",
						item.Harness,
						item.EffectiveSettings,
					)
				}
			}
			if len(seen) != len(agent.AllHarnesses) {
				t.Fatalf("want every harness, got %v", seen)
			}
			for _, h := range plan.Harnesses {
				explicit := len(h.Settings) == 1 && h.Settings[0].Value == "hep" &&
					h.Settings[0].Source == "explicit"
				if (h.ID == string(agent.OMP)) != explicit {
					t.Fatalf("harness %s reports settings %+v", h.ID, h.Settings)
				}
			}
			if scope == "global" {
				if files := writtenUnder(t, other); len(files) != 0 {
					t.Fatalf("global install wrote into the project: %v", files)
				}
			} else if _, err := os.Stat(filepath.Join(other, ".claude")); !os.IsNotExist(err) {
				t.Fatalf("project install wrote into the user directory")
			}
		})
	}
}

func TestUnattendedRunsNeverReduceABlockedPlan(t *testing.T) {
	h := newHarness(t)
	h.writeTwoAgents()
	blocker := filepath.Join(h.project, ".codex", "agents", "reviewer.toml")
	if err := os.MkdirAll(filepath.Dir(blocker), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blocker, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	runs := map[string][]string{
		"all": {"--all", "--json"},
		"yes": {
			"--agent",
			"reviewer",
			"--harness",
			"claude",
			"--harness",
			"codex",
			"--yes",
			"--json",
		},
		"none": {"--agent", "reviewer", "--harness", "claude", "--harness", "codex", "--json"},
	}
	for name, args := range runs {
		t.Run(name, func(t *testing.T) {
			prompter := &scriptedPrompter{}
			code, out := h.runInteractive(prompter, append([]string{"add", h.source}, args...)...)
			if code == 0 || prompter.prompts() != 0 {
				t.Fatalf("blocked plan must fail without prompting, exit %d prompts %d: %s",
					code, prompter.prompts(), out)
			}
			plan := decodePlan(t, out)
			conflict := false
			for _, item := range plan.Items {
				conflict = conflict || (item.Harness == "codex" && item.Action == "conflict")
			}
			if plan.Status != "refused" || !conflict {
				t.Fatalf("want refused plan reporting the conflict: %s", out)
			}
			if files := writtenUnder(t, h.project); len(files) != 1 {
				t.Fatalf("nothing may be written when any item blocks, got %v", files)
			}
		})
	}
}

func TestInteractiveReducedPlanNeedsExplicitApproval(t *testing.T) {
	h := newHarness(t)
	h.writeTwoAgents()
	blocker := filepath.Join(h.project, ".codex", "agents", "reviewer.toml")
	if err := os.MkdirAll(filepath.Dir(blocker), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blocker, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	selection := [][]string{{"reviewer"}, {"claude", "codex"}}

	declined := &scriptedPrompter{selections: selection, confirms: []bool{false}}
	code, out := h.runInteractive(declined, "add", h.source)
	if code == 0 || declined.confirmed != 1 {
		t.Fatalf("declining the reduced plan must cancel, exit %d: %s", code, out)
	}
	if files := writtenUnder(t, h.project); len(files) != 1 {
		t.Fatalf("a declined plan wrote %v", files)
	}

	approved := &scriptedPrompter{selections: selection, confirms: []bool{true}}
	code, out = h.runInteractive(approved, "add", h.source)
	if code != 0 {
		t.Fatalf("an approved reduced plan must apply, exit %d: %s", code, out)
	}
	if !strings.Contains(h.installed(".claude/agents/reviewer.md"), "name: reviewer") {
		t.Fatal("the valid item was not installed")
	}
	if h.installed(".codex/agents/reviewer.toml") != "mine" {
		t.Fatal("a skipped conflict was overwritten")
	}
	if _, err := os.Stat(filepath.Join(h.project, ".claude", "agents", "planner.md")); err == nil {
		t.Fatal("an unselected agent was installed")
	}
}

func TestInteractiveRefusesWhenNothingValidRemains(t *testing.T) {
	h := newHarness(t)
	h.write("agents/reviewer.md", reviewer)
	blocker := filepath.Join(h.project, ".claude", "agents", "reviewer.md")
	if err := os.MkdirAll(filepath.Dir(blocker), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blocker, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	prompter := &scriptedPrompter{confirms: []bool{true}}
	code, out := h.runInteractive(prompter, "add", h.source, "--harness", "claude")
	if code == 0 || prompter.confirmed != 0 {
		t.Fatalf("a plan with no valid item must be refused without asking, exit %d: %s", code, out)
	}
}

func TestInteractiveOffersEverySupportedHarnessPreselectingDetected(t *testing.T) {
	h := newHarness(t)
	h.write("agents/reviewer.md", reviewer)
	if err := os.MkdirAll(filepath.Join(h.home, ".claude"), 0o750); err != nil {
		t.Fatal(err)
	}
	prompter := &scriptedPrompter{selections: [][]string{nil}, confirms: []bool{true}}
	code, out := h.runInteractive(prompter, "add", h.source)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if len(prompter.offered) != 1 || len(prompter.offered[0]) != len(agent.AllHarnesses) {
		t.Fatalf("want one harness prompt offering every harness, got %+v", prompter.offered)
	}
	for _, option := range prompter.offered[0] {
		if option.Selected != (option.Value == string(agent.Claude)) {
			t.Fatalf("only detected harnesses may be preselected: %+v", option)
		}
	}
	if files := writtenUnder(t, h.project); len(files) != 2 { // artifact + lock
		t.Fatalf("accepting the defaults must install only into claude, got %v", files)
	}
}

func TestUnattendedRunsNeverInventASelection(t *testing.T) {
	h := newHarness(t)
	h.writeTwoAgents()
	prompter := &scriptedPrompter{}
	code, out := h.runInteractive(prompter, "add", h.source, "--harness", "claude", "--yes")
	if code == 0 || !strings.Contains(out, "choose agents") || prompter.prompts() != 0 {
		t.Fatalf(
			"--yes on a multi-agent source must fail without prompting, exit %d: %s",
			code,
			out,
		)
	}
	code, out = h.runInteractive(prompter, "add", h.source, "--agent", "reviewer", "--json")
	if code == 0 || prompter.prompts() != 0 {
		t.Fatalf("--json must never prompt, exit %d: %s", code, out)
	}
	if files := writtenUnder(t, h.project); len(files) != 0 {
		t.Fatalf("unconfirmed runs wrote %v", files)
	}
}

func TestInvalidExtrasFailBeforeAnyPrompt(t *testing.T) {
	h := newHarness(t)
	h.writeTwoAgents()
	for _, extra := range []string{"omp.profile=a=b", "profile=hep", "omp.model=x", "claude.profile=x"} {
		prompter := &scriptedPrompter{}
		code, out := h.runInteractive(prompter, "add", h.source, "--extra", extra)
		if code == 0 || prompter.prompts() != 0 {
			t.Fatalf("--extra %s must fail before prompting, exit %d: %s", extra, code, out)
		}
	}
	prompter := &scriptedPrompter{selections: [][]string{{"reviewer"}, {"claude"}}}
	code, out := h.runInteractive(prompter, "add", h.source, "--extra", "omp.profile=hep")
	if code == 0 || !strings.Contains(out, "not selected") {
		t.Fatalf("an extra never selects its harness, exit %d: %s", code, out)
	}
}

func TestScopeIsNeverRedirected(t *testing.T) {
	h := newHarness(t)
	h.write("agents/reviewer.md", reviewer)
	code, out := h.run("add", h.source, "--harness", "omp", "--harness", "claude", "--global",
		"--extra", "omp.profile=missing", "--yes", "--json")
	if code == 0 {
		t.Fatalf("a destination unavailable in the chosen scope must fail: %s", out)
	}
	plan := decodePlan(t, out)
	for _, item := range plan.Items {
		if item.Harness == "omp" && item.Action != "incompatible" {
			t.Fatalf("omp must be incompatible in global scope, got %s", item.Action)
		}
	}
	if files := append(writtenUnder(t, h.project), writtenUnder(t, h.home)...); len(files) != 0 {
		t.Fatalf("nothing may be written in either scope, got %v", files)
	}
}
