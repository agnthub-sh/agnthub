//go:build e2e && linux

package e2e

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/bytedance/sonic"

	"github.com/agnthub-sh/agnthub/platform/env"
)

var update = flag.Bool("update", false, "rewrite testdata/golden/<harness> from this run")

// runRoot is fixed so the absolute paths in plans, locks and ownership records
// are the same on every run; the suite runs in a throwaway container.
const runRoot = "/agnthub-e2e"

var (
	processEnv = env.FromProcess()

	projectDir = filepath.Join(runRoot, "project")
	homeDir    = filepath.Join(runRoot, "home")
	// elsewhereDir is an empty directory outside the project, where Harnesses see
	// only user-scope agents.
	elsewhereDir = filepath.Join(runRoot, "elsewhere")
	scratchDir   = filepath.Join(runRoot, "scratch")
)

// projectTrust is the environment that makes a Harness trust the project, for
// Harnesses that ignore project content otherwise.
var projectTrust = map[string][]string{
	"gemini": {"GEMINI_CLI_TRUST_WORKSPACE=true"},
}

// skillInstallerConfig runs the preinstalled skills.sh (the default backend,
// pinned in the image) instead of `npx skills`, which would download it.
const skillInstallerConfig = `skillInstaller:
  executable: skills
  args: [add, "{source}", --skill, "{skill}", --agent, "{target}", --yes]
  projectArgs: []
  globalArgs: [--global]
`

type suite struct {
	harness string
	caFile  string
	golden  string
	check   nativeCheck
	// ran are the golden directories this run produced.
	ran []string
}

// TestHarness installs the fixture repositories into one Harness (E2E_HARNESS),
// compares every run with testdata/golden/<harness> and asks the real Harness
// which agents it loads.
func TestHarness(t *testing.T) {
	harness := processEnv.Get("E2E_HARNESS")
	if harness == "" {
		t.Skip("E2E_HARNESS is not set; run the suite in its container with make e2e")
	}
	check, ok := nativeChecks[harness]
	if !ok {
		t.Fatalf("unknown E2E_HARNESS %q", harness)
	}
	syscall.Umask(0o022)
	if err := os.RemoveAll(runRoot); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{runRoot, elsewhereDir, scratchDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	publishFixtures(t, filepath.Join("testdata", "fixtures"), runRoot)
	s := &suite{
		harness: harness,
		caFile:  serveGit(t, runRoot),
		golden:  filepath.Join("testdata", "golden", harness),
		check:   check,
	}
	t.Logf("harness version: %s %s", harness, harnessVersion(t, harness))

	layoutsLocal := filepath.Join(runRoot, "src", "layouts")
	layoutsGit := gitBase + "layouts.git"
	ambiguous := filepath.Join(runRoot, "src", "ambiguous")
	manifest := gitBase + "manifest.git"
	h := []string{"--harness", harness}

	// Inventories are Harness-independent but cheap; every Harness run checks them.
	s.run(t, step{name: "list-layouts", args: argv("add", layoutsLocal, "--list", "--json")})
	s.run(t, step{name: "list-ambiguous", args: argv("add", ambiguous, "--list", "--json")})
	s.run(t, step{name: "list-manifest", args: argv("add", manifest, "--list", "--json")})

	// Ambiguous identities refuse their agents and nothing else.
	s.run(t, step{
		name: "refuse-ambiguous-portable", refused: true,
		args: argv("add", ambiguous, "--agent", "twin", h, "--yes", "--json"),
	})
	s.run(t, step{
		name: "refuse-ambiguous-native", refused: true,
		args: argv("add", ambiguous, "--agent", "echo", h, "--yes", "--json"),
	})
	s.run(t, step{
		name: "install-ambiguous-unaffected", native: scopeProject,
		args: argv("add", ambiguous, "--agent", "solo", h, "--yes", "--json"),
	})

	// The plan of every layout agent records which install and which are refused
	// (and why) for this Harness; the install steps then select exactly the
	// installable ones.
	plan := s.run(t, step{
		name: "plan-layouts", planOnly: true,
		args: argv("add", layoutsLocal, "--agent", "*", h, "--json"),
	})
	installable, refused := plan.plan.split()
	if len(installable) == 0 {
		t.Fatal("plan-layouts: no installable agents")
	}
	selected := agentFlags(installable)
	s.run(t, step{
		name: "install-layouts-project-local", native: scopeProject,
		args: argv("add", layoutsLocal, selected, h, "--yes", "--json"),
	})
	s.run(t, step{
		name: "install-layouts-project-git", native: scopeProject,
		args: argv("add", layoutsGit, selected, h, "--yes", "--json"),
	})
	s.run(t, step{
		name: "install-layouts-global-git", native: scopeGlobal,
		args: argv("add", layoutsGit+"#v1.0.0", selected, h, "--global", "--yes", "--json"),
	})
	if len(refused) > 0 {
		// --yes never reduces a plan: one refused agent fails the whole run.
		s.run(t, step{
			name: "refuse-layouts", refused: true,
			args: argv("add", layoutsLocal, agentFlags(refused), h, "--yes", "--json"),
		})
	}

	// agnthub.yaml: Variants, same-repository and external Agent Dependencies, a
	// Variant-only dependency and a Skill Dependency installed by skills.sh.
	manifestPlan := s.run(t, step{
		name: "plan-manifest", planOnly: true,
		args: argv("add", manifest, "--agent", "*", h, "--json"),
	})
	direct := []string{"lead"}
	if ok, _ := manifestPlan.plan.split(); slices.Contains(ok, "native-only") {
		direct = append(direct, "native-only")
	}
	// Gemini CLI ignores project skills outside trusted folders, so agnthub
	// refuses the Skill Dependency until the project is trusted.
	trust := projectTrust[harness]
	if len(trust) > 0 {
		s.run(t, step{
			name: "refuse-manifest-project-untrusted", refused: true,
			args: argv("add", manifest, agentFlags(direct), h, "--yes", "--json"),
		})
	}
	s.run(t, step{
		name: "install-manifest-project", native: scopeProject, env: trust,
		args: argv("add", manifest, agentFlags(direct), h, "--yes", "--json"),
	})
	s.run(t, step{
		name: "install-manifest-global", native: scopeGlobal,
		args: argv("add", manifest, agentFlags(direct), h, "--global", "--yes", "--json"),
	})

	s.finishGoldens(t)
}

// argv flattens strings and string slices into one argument list.
func argv(parts ...any) []string {
	var args []string
	for _, part := range parts {
		switch part := part.(type) {
		case string:
			args = append(args, part)
		case []string:
			args = append(args, part...)
		default:
			panic(fmt.Sprintf("argv: unexpected %T", part))
		}
	}
	return args
}

func agentFlags(ids []string) []string {
	flags := make([]string, 0, 2*len(ids))
	for _, id := range ids {
		flags = append(flags, "--agent", id)
	}
	return flags
}

type scope string

const (
	scopeProject scope = "project"
	scopeGlobal  scope = "global"
)

// step is one agnthub invocation in a fresh project and home.
type step struct {
	name string
	args []string
	// env is added to agnthub's environment.
	env []string
	// refused steps must fail without writing anything.
	refused bool
	// planOnly steps preview without --yes: they must not write anything.
	planOnly bool
	// native asks the Harness to load what the step installed.
	native scope
}

type result struct {
	exit   int
	stdout []byte
	stderr []byte
	// files are the project and home trees after the run, keyed by
	// "project/<rel>" or "home/<rel>", normalized.
	files map[string]file
	plan  planOutput
}

type file struct {
	mode    os.FileMode
	content []byte
}

// run executes st, compares it with its golden directory (or rewrites it with
// -update), then asks the Harness to load what it installed.
func (s *suite) run(t *testing.T, st step) *result {
	t.Helper()
	res := &result{}
	s.ran = append(s.ran, st.name)
	t.Run(st.name, func(t *testing.T) {
		res = s.execute(t, st)
		if st.refused && res.exit == 0 {
			t.Errorf("expected a refusal, got exit status 0")
		}
		if !st.refused && !st.planOnly && res.exit != 0 {
			t.Errorf("exit status %d\n%s", res.exit, res.stderr)
		}
		if st.refused || st.planOnly {
			for name := range res.files {
				if !seeded(name) {
					t.Errorf("%s wrote %s", st.name, name)
				}
			}
		}
		s.compareGolden(t, st.name, goldenFiles(st.env, st.args, res))
		if st.native != "" && res.exit == 0 {
			t.Run("native", func(t *testing.T) {
				s.check(t, s.nativeContext(st.native, res))
			})
		}
	})
	return res
}

func (s *suite) execute(t *testing.T, st step) *result {
	t.Helper()
	resetDirs(t)
	cmd := exec.Command("agnthub", st.args...)
	cmd.Dir = projectDir
	cmd.Env = append(s.agnthubEnv(), st.env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := runTimeout(cmd, 5*time.Minute)
	res := &result{stdout: stdout.Bytes(), stderr: stderr.Bytes()}
	var exitErr *exec.ExitError
	switch {
	case errors.As(err, &exitErr):
		res.exit = exitErr.ExitCode()
	case err != nil:
		t.Fatalf("agnthub %v: %v", st.args, err)
	}
	if len(res.stdout) > 0 && bytes.HasPrefix(bytes.TrimSpace(res.stdout), []byte("{")) {
		if err := sonic.Unmarshal(res.stdout, &res.plan); err != nil {
			t.Fatalf("agnthub --json printed invalid JSON: %v\n%s", err, res.stdout)
		}
	}
	res.files = snapshot(t)
	return res
}

func (s *suite) agnthubEnv() []string {
	return []string{
		"PATH=" + processEnv.Get("PATH"),
		"HOME=" + homeDir,
		"LANG=C.UTF-8",
		"TERM=dumb",
		"NO_COLOR=1",
		"DO_NOT_TRACK=1",
		"DISABLE_TELEMETRY=1",
		// Install reports only go to the registry for public GitHub sources;
		// this keeps any other request off the network as well.
		"AGNTHUB_API_URL=http://127.0.0.1:9",
		"GIT_SSL_CAINFO=" + s.caFile,
		"TMPDIR=" + scratchDir,
	}
}

// resetDirs gives every step an empty project, an empty home with only the
// skill installer configuration, and an empty scratch directory.
func resetDirs(t *testing.T) {
	t.Helper()
	for _, dir := range []string{projectDir, homeDir, scratchDir} {
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	config := filepath.Join(homeDir, seededConfig)
	if err := os.MkdirAll(filepath.Dir(config), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, []byte(skillInstallerConfig), 0o644); err != nil {
		t.Fatal(err)
	}
}

const seededConfig = ".config/agnthub/config.yaml"

func seeded(name string) bool {
	return name == "home/"+seededConfig
}

func runTimeout(cmd *exec.Cmd, timeout time.Duration) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
		<-done
		return context.DeadlineExceeded
	}
}

// planOutput is the part of agnthub's --json plan the suite reads.
type planOutput struct {
	Status string `json:"status"`
	Items  []struct {
		Agent   string `json:"agent"`
		Harness string `json:"harness"`
		Path    string `json:"path"`
		Origin  string `json:"origin"`
		Action  string `json:"action"`
	} `json:"items"`
}

// split returns the agents whose items all install and the agents with any
// refused item, both sorted.
func (p planOutput) split() (installable, refused []string) {
	blocked := map[string]bool{}
	for _, item := range p.Items {
		switch item.Action {
		case "create", "replace", "unchanged":
			if _, seen := blocked[item.Agent]; !seen {
				blocked[item.Agent] = false
			}
		default:
			blocked[item.Agent] = true
		}
	}
	for agent, isBlocked := range blocked {
		if isBlocked {
			refused = append(refused, agent)
		} else {
			installable = append(installable, agent)
		}
	}
	slices.Sort(installable)
	slices.Sort(refused)
	return installable, refused
}

// installed returns agent → installed file for the items of harness.
func (p planOutput) installed(harness string) map[string]string {
	agents := map[string]string{}
	for _, item := range p.Items {
		if item.Harness == harness && item.Path != "" &&
			(item.Action == "create" || item.Action == "replace" || item.Action == "unchanged") {
			agents[item.Agent] = item.Path
		}
	}
	return agents
}

func harnessVersion(t *testing.T, harness string) string {
	t.Helper()
	binary := map[string]string{
		"claude": "claude", "opencode": "opencode", "codex": "codex", "copilot": "copilot",
		"cursor": "cursor-agent", "gemini": "gemini", "omp": "omp",
	}[harness]
	cmd := exec.Command(binary, "--version")
	cmd.Env = []string{"PATH=" + processEnv.Get("PATH"), "HOME=" + scratchDir}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s --version: %v\n%s", binary, err, out)
	}
	return strings.TrimSpace(string(out))
}
