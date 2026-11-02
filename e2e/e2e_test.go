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
	t.Run("image-isolation", func(t *testing.T) { checkImageIsolation(t, harness) })
	publishFixtures(t, filepath.Join("testdata", "fixtures"), runRoot)
	s := &suite{
		harness: harness,
		caFile:  serveGit(t, runRoot),
		golden:  filepath.Join("testdata", "golden", harness),
		check:   check,
	}
	t.Logf("harness version: %s %s", harness, harnessVersion(t, harness))

	s.inventories(t)
	s.ambiguity(t)
	s.layouts(t)
	s.manifest(t)
	s.selection(t)
	s.lifecycle(t)

	s.finishGoldens(t)
}

func source(name string) string { return filepath.Join(runRoot, "src", name) }

func (s *suite) harnessFlag() []string { return []string{"--harness", s.harness} }

// inventories: `add --list` is Harness-independent but cheap; every Harness run
// checks it.
func (s *suite) inventories(t *testing.T) {
	s.run(t, step{
		name: "list-layouts", planOnly: true,
		args: argv("add", source("layouts"), "--list", "--json"),
	})
	s.run(t, step{
		name: "list-ambiguous", planOnly: true,
		args: argv("add", source("ambiguous"), "--list", "--json"),
	})
	s.run(t, step{
		name: "list-manifest", planOnly: true,
		args: argv("add", gitBase+"manifest.git", "--list", "--json"),
	})
}

// ambiguity: ambiguous identities refuse their agents and nothing else.
func (s *suite) ambiguity(t *testing.T) {
	ambiguous, h := source("ambiguous"), s.harnessFlag()
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
}

// layouts: the plan of every layout agent records which install and which are
// refused (and why) for this Harness; the install steps then select exactly the
// installable ones, in each scope from each source kind.
func (s *suite) layouts(t *testing.T) {
	local, git, h := source("layouts"), gitBase+"layouts.git", s.harnessFlag()
	plan := s.run(t, step{
		name: stepPlan, planOnly: true,
		args: argv("add", local, "--agent", "*", h, "--json"),
	})
	installable, refused := plan.plan.split()
	if len(installable) == 0 {
		t.Fatal("plan-layouts: no installable agents")
	}
	selected := agentFlags(installable)
	s.run(t, step{
		name: "install-layouts-project-local", native: scopeProject,
		args: argv("add", local, selected, h, "--yes", "--json"),
	})
	s.run(t, step{
		name: "install-layouts-project-git", native: scopeProject,
		args: argv("add", git, selected, h, "--yes", "--json"),
	})
	s.run(t, step{
		name: "install-layouts-global-local", native: scopeGlobal,
		args: argv("add", local, selected, h, "--global", "--yes", "--json"),
	})
	s.run(t, step{
		name: "install-layouts-global-git", native: scopeGlobal,
		args: argv("add", git+"#v1.0.0", selected, h, "--global", "--yes", "--json"),
	})
	if len(refused) == 0 {
		t.Fatal("plan-layouts: no refused agents; the refusal fixtures stopped refusing")
	}
	// --yes never reduces a plan: one refused agent fails the whole run.
	s.run(t, step{
		name: stepRefuseLy, refused: true,
		args: argv("add", local, agentFlags(refused), h, "--yes", "--json"),
	})
}

// manifest: agnthub.yaml with Variants, same-repository and external Agent
// Dependencies, a Variant-only dependency, a Skill Dependency installed by
// skills.sh and an agent whose every Variant is invalid.
func (s *suite) manifest(t *testing.T) {
	git, h := gitBase+"manifest.git", s.harnessFlag()
	manifestPlan := s.run(t, step{
		name: stepPlanMan, planOnly: true,
		args: argv("add", git, "--agent", "*", h, "--json"),
	})
	direct := []string{"lead"}
	if ok, _ := manifestPlan.plan.split(); slices.Contains(ok, "native-only") {
		direct = append(direct, "native-only")
	}
	// Gemini CLI ignores project skills outside trusted folders, so agnthub
	// refuses the Skill Dependency until the project is trusted.
	trust := projectTrust[s.harness]
	if len(trust) > 0 {
		s.run(t, step{
			name: "refuse-manifest-project-untrusted", refused: true,
			args: argv("add", git, agentFlags(direct), h, "--yes", "--json"),
		})
	}
	s.run(t, step{
		name: "install-manifest-project-local", native: scopeProject, env: trust,
		args: argv("add", source("manifest"), agentFlags(direct), h, "--yes", "--json"),
	})
	s.run(t, step{
		name: "install-manifest-project-git", native: scopeProject, env: trust,
		args: argv("add", git, agentFlags(direct), h, "--yes", "--json"),
	})
	s.run(t, step{
		name: "install-manifest-global-git", native: scopeGlobal,
		args: argv("add", git, agentFlags(direct), h, "--global", "--yes", "--json"),
	})
}

// selection: --harness all, --all and --extra omp.profile.
func (s *suite) selection(t *testing.T) {
	companion, h := source("companion"), s.harnessFlag()
	s.run(t, step{
		name: "select-harness-all", native: scopeProject, everyHarness: true,
		args: argv("add", companion, "--agent", "companion", "--harness", "all", "--yes", "--json"),
	})
	s.run(t, step{
		name: "select-agent-star", native: scopeProject,
		args: argv("add", companion, "--agent", "*", h, "--yes", "--json"),
	})
	s.run(t, step{
		name: "select-all", native: scopeProject, everyHarness: true,
		args: argv("add", companion, "--all", "--json"),
	})
	if s.harness != "omp" {
		// A setting for a Harness that is not selected is refused.
		s.run(t, step{
			name: "extra-unselected", refused: true,
			args: argv("add", companion, "--agent", "companion", h,
				"--extra", "omp.profile=work", "--yes", "--json"),
		})
		return
	}
	profileDir := filepath.Join(homeDir, ".omp", "profiles", "work")
	s.run(t, step{
		name: "extra-omp-profile-global", native: scopeGlobal, ompProfile: "work",
		before: []action{{
			note: "create omp profile work (" + profileDir + ")",
			do:   func(t *testing.T) { mkdirAll(t, profileDir) },
		}},
		args: argv("add", companion, "--agent", "companion", h, "--global",
			"--extra", "omp.profile=work", "--yes", "--json"),
	})
	s.run(t, step{
		name: "extra-omp-profile-project", native: scopeProject, ompProfile: "work",
		args: argv("add", companion, "--agent", "companion", h,
			"--extra", "omp.profile=work", "--yes", "--json"),
	})
	// agnthub does not create omp profiles.
	s.run(t, step{
		name: "extra-omp-profile-missing", refused: true,
		args: argv("add", companion, "--agent", "companion", h, "--global",
			"--extra", "omp.profile=work", "--yes", "--json"),
	})
}

// lifecycle: list, check, update, restore and remove after an install from a
// Git source whose tag v1 later moves to a newer revision.
func (s *suite) lifecycle(t *testing.T) {
	h := s.harnessFlag()
	evolving := gitBase + "evolving.git#v1"
	add := action{args: argv("add", evolving, "--agent", "drifter", h, "--yes")}
	addGlobal := action{args: argv("add", evolving, "--agent", "drifter", h, "--global", "--yes")}
	modify := action{
		note: "append a line to the installed drifter",
		do:   func(t *testing.T) { s.editInstalled(t) },
	}
	moveV1 := action{
		note: "move tag v1 of evolving.git to its next revision",
		do:   func(t *testing.T) { moveTag(t, runRoot, "evolving", "v1") },
	}

	s.run(t, step{
		name: "lifecycle-list", planOnly: true, before: []action{add},
		args: argv("list", "--json"),
	})
	s.run(t, step{
		name: "lifecycle-check-current", planOnly: true, before: []action{add},
		args: argv("check", "--json"),
	})
	s.run(t, step{
		name: "lifecycle-check-drift", planOnly: true, before: []action{add, modify},
		args: argv("check", "--json"),
	})
	s.run(t, step{
		name: "lifecycle-update", native: scopeProject,
		before: []action{add, moveV1, {args: argv("check", "--json")}},
		args:   argv("update", "--yes", "--json"),
	})
	s.run(t, step{
		name: "lifecycle-update-refuse-modified", refused: true,
		before: []action{add, modify, moveV1},
		args:   argv("update", "--yes", "--json"),
	})
	fresh := action{
		note: "keep only agnthub-lock.json: delete the installed files and the user's " +
			"ownership records, as in a fresh clone on another machine",
		do: func(t *testing.T) { s.freshClone(t) },
	}
	restore := argv("restore", "--yes", "--json")
	if s.harness == "omp" {
		// A machine without recorded bindings must name the omp profile.
		s.run(t, step{
			name: "lifecycle-restore-refuse-unbound", refused: true,
			before: []action{add, fresh}, args: restore,
		})
		restore = argv(restore, "--extra", "omp.profile=default")
	}
	s.run(t, step{
		name: "lifecycle-restore", native: scopeProject,
		before: []action{add, fresh}, args: restore,
	})
	s.run(t, step{
		name: "lifecycle-restore-refuse-modified", refused: true, before: []action{add, modify},
		args: argv("restore", "--yes", "--json"),
	})
	s.run(t, step{
		name: "lifecycle-remove",
		before: []action{add, {
			note: "write an unmanaged agent file next to the installed drifter",
			do:   func(t *testing.T) { s.writeNeighbor(t) },
		}},
		args: argv("remove", "drifter", "--yes", "--json"),
	})
	s.run(t, step{
		name: "lifecycle-remove-refuse-modified", refused: true, before: []action{add, modify},
		args: argv("remove", "drifter", "--yes", "--json"),
	})
	trust := projectTrust[s.harness]
	s.run(t, step{
		name: "lifecycle-clean-dependencies", env: trust,
		before: []action{
			{args: argv("add", gitBase+"manifest.git", "--agent", "lead", h, "--yes")},
			{args: argv("remove", "lead", "--yes")},
		},
		args: argv("remove", "--clean-dependencies", "--yes", "--json"),
	})
	s.run(t, step{
		name: "lifecycle-global",
		before: []action{
			addGlobal,
			{args: argv("list", "--global", "--json")},
			{args: argv("check", "--global", "--json")},
		},
		args: argv("remove", "drifter", "--global", "--yes", "--json"),
	})
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

// step is one recorded scenario run in a fresh project and home: optional setup
// actions, then the agnthub command whose result is the step's golden.
type step struct {
	name string
	args []string
	// before runs first; its agnthub commands must succeed and are recorded too.
	before []action
	// env is added to agnthub's environment.
	env []string
	// refused steps must fail without changing anything the setup left.
	refused bool
	// planOnly steps (previews, inventories, list, check) must not change
	// anything.
	planOnly bool
	// native asks the Harness to load what agnthub reports installed.
	native scope
	// ompProfile is the omp profile the native check runs in.
	ompProfile string
	// everyHarness steps install into every Harness, not only this one.
	everyHarness bool
}

// action is one setup action of a step: an agnthub command or a Go function
// described by note.
type action struct {
	args []string
	note string
	do   func(t *testing.T)
}

type result struct {
	exit   int
	stdout []byte
	stderr []byte
	// transcript records the setup actions and their output.
	transcript []byte
	// before and files are the project and home trees before and after the
	// step's command, keyed by "project/<rel>" or "home/<rel>", normalized.
	before map[string]file
	files  map[string]file
	plan   planOutput
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
		s.checkSpec(t, st)
		res = s.execute(t, st)
		if st.refused && res.exit == 0 {
			t.Errorf("expected a refusal, got exit status 0")
		}
		if !st.refused && !st.planOnly && res.exit != 0 {
			t.Errorf("exit status %d\n%s", res.exit, res.stderr)
		}
		if st.refused || st.planOnly {
			requireUnchanged(t, res.before, res.files)
		}
		if !st.everyHarness {
			requireOwnDirectories(t, s.harness, res.files)
		}
		s.compareGolden(t, st.name, goldenFiles(st.env, st.args, res))
		if st.native != "" && res.exit == 0 {
			t.Run("native", func(t *testing.T) {
				s.check(t, s.nativeContext(t, st))
			})
		}
	})
	return res
}

// checkSpec requires st to match its declaration in stepSpecs (coverage.go),
// which TestCoverage reads to prove the coverage matrix.
func (s *suite) checkSpec(t *testing.T, st step) {
	t.Helper()
	spec, ok := stepSpecs[st.name]
	if !ok {
		t.Fatalf("step %s is not declared in stepSpecs (coverage.go)", st.name)
	}
	if !spec.appliesTo(s.harness) {
		t.Fatalf("stepSpecs says %s does not run for %s", st.name, s.harness)
	}
	if spec.native != (st.native != "") {
		t.Errorf("stepSpecs native=%v, step native=%q", spec.native, st.native)
	}
	if st.native != "" && string(st.native) != spec.scope {
		t.Errorf("stepSpecs scope %q, native check in %s scope", spec.scope, st.native)
	}
}

func (s *suite) execute(t *testing.T, st step) *result {
	t.Helper()
	resetDirs(t)
	res := &result{}
	var transcript bytes.Buffer
	for _, a := range st.before {
		if a.do != nil {
			fmt.Fprintf(&transcript, "# %s\n", a.note)
			a.do(t)
			continue
		}
		exit, stdout, stderr := s.agnthub(t, st.env, a.args)
		fmt.Fprintf(&transcript, "$ %s\nexit status: %d\n",
			shellQuote(append(append(slices.Clone(st.env), "agnthub"), a.args...)), exit)
		if len(stdout) > 0 {
			fmt.Fprintf(&transcript, "--- stdout\n%s", normalize(stdout))
		}
		if len(stderr) > 0 {
			fmt.Fprintf(&transcript, "--- stderr\n%s", normalize(stderr))
		}
		if exit != 0 {
			t.Fatalf("setup agnthub %v: exit status %d\n%s", a.args, exit, stderr)
		}
	}
	res.transcript = transcript.Bytes()
	res.before = snapshot(t)
	res.exit, res.stdout, res.stderr = s.agnthub(t, st.env, st.args)
	if len(res.stdout) > 0 && bytes.HasPrefix(bytes.TrimSpace(res.stdout), []byte("{")) {
		if err := sonic.Unmarshal(res.stdout, &res.plan); err != nil {
			t.Fatalf("agnthub --json printed invalid JSON: %v\n%s", err, res.stdout)
		}
	}
	res.files = snapshot(t)
	return res
}

// agnthub runs the CLI in the project with the suite's environment.
func (s *suite) agnthub(t *testing.T, extraEnv, args []string) (exit int, stdout, stderr []byte) {
	t.Helper()
	cmd := exec.Command("agnthub", args...)
	cmd.Dir = projectDir
	cmd.Env = append(s.agnthubEnv(), extraEnv...)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := runTimeout(cmd, 5*time.Minute)
	var exitErr *exec.ExitError
	switch {
	case errors.As(err, &exitErr):
		exit = exitErr.ExitCode()
	case err != nil:
		t.Fatalf("agnthub %v: %v", args, err)
	}
	return exit, out.Bytes(), errOut.Bytes()
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

// installedArtifact is one file agnthub reports installed for a Harness.
type installedArtifact struct {
	agent   string
	path    string
	profile string
}

// installed asks agnthub which files it installed for this Harness in the
// project, or in user scope with global.
func (s *suite) installed(t *testing.T, global bool) []installedArtifact {
	t.Helper()
	args := []string{"list", "--json"}
	if global {
		args = append(args, "--global")
	}
	exit, stdout, stderr := s.agnthub(t, nil, args)
	if exit != 0 {
		t.Fatalf("agnthub %v: exit status %d\n%s", args, exit, stderr)
	}
	var agents []struct {
		Agent     string `json:"agent"`
		Artifacts []struct {
			Harness  string `json:"harness"`
			Path     string `json:"path"`
			Status   string `json:"status"`
			Bindings []struct {
				Profile string `json:"profile"`
			} `json:"bindings"`
		} `json:"artifacts"`
	}
	if err := sonic.Unmarshal(stdout, &agents); err != nil {
		t.Fatalf("agnthub list --json: %v\n%s", err, stdout)
	}
	var out []installedArtifact
	for _, a := range agents {
		for _, art := range a.Artifacts {
			if art.Harness != s.harness || art.Status != "ok" {
				continue
			}
			item := installedArtifact{agent: a.Agent, path: art.Path}
			for _, b := range art.Bindings {
				item.profile = b.Profile
			}
			out = append(out, item)
		}
	}
	return out
}

// editInstalled appends a line to every file agnthub installed for this
// Harness: a comment in TOML, a line of instructions in Markdown.
func (s *suite) editInstalled(t *testing.T) {
	t.Helper()
	for _, a := range s.installed(t, false) {
		f, err := os.OpenFile(a.path, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteString("# edited locally\n"); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

// freshClone deletes everything but the project's agnthub-lock.json.
func (s *suite) freshClone(t *testing.T) {
	t.Helper()
	for _, a := range s.installed(t, false) {
		if err := os.Remove(a.path); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.RemoveAll(filepath.Join(homeDir, ".local", "state", "agnthub")); err != nil {
		t.Fatal(err)
	}
}

// writeNeighbor writes an agent file agnthub does not own beside each
// installed one, with the same suffix.
func (s *suite) writeNeighbor(t *testing.T) {
	t.Helper()
	for _, a := range s.installed(t, false) {
		suffix := strings.TrimPrefix(filepath.Base(a.path), a.agent)
		neighbor := filepath.Join(filepath.Dir(a.path), "hand-written"+suffix)
		content := "---\nname: hand-written\ndescription: Written by hand.\n---\nKeep me.\n"
		if strings.HasSuffix(suffix, ".toml") {
			content = "name = \"hand-written\"\ndescription = \"Written by hand.\"\n" +
				"developer_instructions = \"Keep me.\"\n"
		}
		if err := os.WriteFile(neighbor, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func mkdirAll(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
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
		mkdirAll(t, dir)
	}
	config := filepath.Join(homeDir, seededConfig)
	mkdirAll(t, filepath.Dir(config))
	if err := os.WriteFile(config, []byte(skillInstallerConfig), 0o644); err != nil {
		t.Fatal(err)
	}
}

const seededConfig = ".config/agnthub/config.yaml"

// requireUnchanged fails for every file the step's command created, changed or
// deleted.
func requireUnchanged(t *testing.T, before, after map[string]file) {
	t.Helper()
	for name, f := range after {
		old, ok := before[name]
		switch {
		case !ok:
			t.Errorf("wrote %s", name)
		case old.mode != f.mode || !bytes.Equal(old.content, f.content):
			t.Errorf("changed %s", name)
		}
	}
	for name := range before {
		if _, ok := after[name]; !ok {
			t.Errorf("deleted %s", name)
		}
	}
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

// harnessBinaries are the CLI each Harness image provides.
var harnessBinaries = map[string]string{
	"claude": "claude", "opencode": "opencode", "codex": "codex", "copilot": "copilot",
	"cursor": "cursor-agent", "gemini": "gemini", "omp": "omp",
}

func harnessVersion(t *testing.T, harness string) string {
	t.Helper()
	binary := harnessBinaries[harness]
	cmd := exec.Command(binary, "--version")
	cmd.Env = []string{"PATH=" + processEnv.Get("PATH"), "HOME=" + scratchDir}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s --version: %v\n%s", binary, err, out)
	}
	return strings.TrimSpace(string(out))
}
