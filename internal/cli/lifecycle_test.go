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

	"github.com/heynemann/agnthub/internal/cli"
	"github.com/heynemann/agnthub/internal/platform/env"
	"github.com/heynemann/agnthub/internal/source"
	"github.com/heynemann/agnthub/internal/state"
)

// lifecycleRun configures one CLI invocation for lifecycle tests.
type lifecycleRun struct {
	git      fakeGit
	env      map[string]string
	prompter cli.Prompter
}

func (h *harnessT) runLifecycle(run lifecycleRun, args ...string) (int, string) {
	h.t.Helper()
	values := map[string]string{"HOME": h.home}
	for key, value := range run.env {
		values[key] = value
	}
	var out bytes.Buffer
	app := cli.NewApp(cli.App{
		Env:         env.FromMap(values),
		WorkDir:     h.project,
		Stdin:       strings.NewReader(""),
		Stdout:      &out,
		Stderr:      &out,
		Interactive: run.prompter != nil,
		Prompter:    run.prompter,
		Now:         func() time.Time { return time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC) },
		LookPath:    func(string) (string, error) { return "", os.ErrNotExist },
		Opener:      source.Opener{Git: run.git},
	})
	return app.Run(context.Background(), args), out.String()
}

// agentsRepo serves one revision of acme/agents both at its default ref and,
// pinned, at its revision.
func agentsRepo(git fakeGit, revision string, files map[string]string) {
	repo := remoteRepo{revision: revision, files: files}
	git["acme/agents"] = repo
	git["acme/agents#"+revision] = repo
}

func reviewerFiles(body string) map[string]string {
	return map[string]string{
		"agents/reviewer.md": "---\nname: reviewer\ndescription: Reviews code\n---\n" + body + "\n",
	}
}

type checkOutput struct {
	Agents []struct {
		Agent    string `json:"agent"`
		Status   string `json:"status"`
		Revision string `json:"revision"`
		Drift    bool   `json:"drift"`
		Error    string `json:"error"`
		Source   struct {
			Revision string `json:"revision"`
		} `json:"source"`
		Artifacts []struct {
			Harness  string              `json:"harness"`
			Change   string              `json:"change"`
			Local    string              `json:"local"`
			Bindings []map[string]string `json:"bindings"`
		} `json:"artifacts"`
	} `json:"agents"`
}

func (h *harnessT) check(run lifecycleRun, args ...string) checkOutput {
	h.t.Helper()
	code, out := h.runLifecycle(run, append([]string{"check", "--json"}, args...)...)
	if code != 0 {
		h.t.Fatalf("check exit %d: %s", code, out)
	}
	var report checkOutput
	if err := sonic.UnmarshalString(out, &report); err != nil {
		h.t.Fatalf("check output is not JSON: %v\n%s", err, out)
	}
	return report
}

func (h *harnessT) mustRun(run lifecycleRun, args ...string) string {
	h.t.Helper()
	code, out := h.runLifecycle(run, args...)
	if code != 0 {
		h.t.Fatalf("%v: exit %d: %s", args, code, out)
	}
	return out
}

func TestCheckAndUpdateApplyChangedSourceOnlyWhenApproved(t *testing.T) {
	h := newHarness(t)
	git := fakeGit{}
	agentsRepo(git, "r1", reviewerFiles("Review v1."))
	run := lifecycleRun{git: git}
	h.mustRun(run, "add", "acme/agents", "--harness", "claude", "--yes")
	installed := h.installed(".claude/agents/reviewer.md")

	if report := h.check(run); report.Agents[0].Status != "up-to-date" {
		t.Fatalf("unchanged source must be up to date: %+v", report)
	}

	agentsRepo(git, "r2", reviewerFiles("Review v2."))
	report := h.check(run)
	if got := report.Agents[0]; got.Status != "update-available" || got.Revision != "r2" ||
		got.Source.Revision != "r1" || got.Artifacts[0].Change != "content" {
		t.Fatalf("changed source must be reported against the locked revision: %+v", report)
	}
	if h.installed(".claude/agents/reviewer.md") != installed {
		t.Fatal("check must not write")
	}

	if code, out := h.runLifecycle(run, "update"); code == 0 ||
		h.installed(".claude/agents/reviewer.md") != installed {
		t.Fatalf("unapproved noninteractive update must not write, exit %d: %s", code, out)
	}
	declined := &scriptedPrompter{confirms: []bool{false}}
	if code, _ := h.runLifecycle(lifecycleRun{git: git, prompter: declined}, "update"); code == 0 ||
		declined.confirmed != 1 || h.installed(".claude/agents/reviewer.md") != installed {
		t.Fatal("a declined update must not write")
	}

	h.mustRun(run, "update", "--yes")
	updated := h.installed(".claude/agents/reviewer.md")
	if !strings.Contains(updated, "Review v2.") {
		t.Fatalf("update did not apply the new content:\n%s", updated)
	}
	locked := h.lock().Agents[0]
	if locked.Source.Revision != "r2" || locked.Origin != state.OriginDirect ||
		locked.Artifacts[0].Digest != state.Digest([]byte(updated)) {
		t.Fatalf("lock must record the new provenance: %+v", locked)
	}
	if report := h.check(run); report.Agents[0].Status != "up-to-date" {
		t.Fatalf("after update the lock must be current: %+v", report)
	}
}

func TestUpdateAddsNewDependencyAndKeepsDroppedOnes(t *testing.T) {
	h := newHarness(t)
	git := fakeGit{}
	manifest := func(dep string) string {
		m := "schemaVersion: 1\nagents:\n  reviewer:\n    portable: agents/reviewer.md\n"
		if dep != "" {
			m += "    dependencies:\n      agents:\n        - agent: " + dep + "\n"
		}
		m += "  planner:\n    portable: agents/planner.md\n" +
			"  linter:\n    portable: agents/linter.md\n"
		return m
	}
	files := func(dep string) map[string]string {
		return map[string]string{
			"agnthub.yaml":       manifest(dep),
			"agents/reviewer.md": agentFile("reviewer"),
			"agents/planner.md":  agentFile("planner"),
			"agents/linter.md":   agentFile("linter"),
		}
	}
	agentsRepo(git, "r1", files("planner"))
	run := lifecycleRun{git: git}
	h.mustRun(run, "add", "acme/agents", "--agent", "reviewer", "--harness", "claude", "--yes")

	agentsRepo(git, "r2", files("linter"))
	statuses := map[string]string{}
	for _, entry := range h.check(run).Agents {
		statuses[entry.Agent] = entry.Status
	}
	if statuses["linter"] != "new-dependency" || statuses["planner"] != "no-longer-required" {
		t.Fatalf("dependency changes must be reported: %v", statuses)
	}
	h.mustRun(run, "update", "--yes")
	h.installed(".claude/agents/linter.md")
	h.installed(".claude/agents/planner.md") // dependencies remain by default
	if lock := h.lock(); lock.Find("github:acme/agents::linter") == nil ||
		lock.Find("github:acme/agents::planner") == nil {
		t.Fatalf("lock must record the new companion and keep the old one: %+v", lock.Agents)
	}
}

func TestUpdateNeverOverwritesLocalDrift(t *testing.T) {
	h := newHarness(t)
	git := fakeGit{}
	agentsRepo(git, "r1", reviewerFiles("Review v1."))
	run := lifecycleRun{git: git}
	h.mustRun(run, "add", "acme/agents", "--harness", "claude", "--yes")
	path := filepath.Join(h.project, ".claude/agents/reviewer.md")
	writeFile(t, path, "my local edit\n")
	agentsRepo(git, "r2", reviewerFiles("Review v2."))

	report := h.check(run)
	if got := report.Agents[0]; !got.Drift || got.Artifacts[0].Local != "modified" {
		t.Fatalf("check must report local drift: %+v", report)
	}
	code, out := h.runLifecycle(run, "update", "--yes")
	if code == 0 || h.installed(".claude/agents/reviewer.md") != "my local edit\n" {
		t.Fatalf("update must refuse to overwrite a modified file, exit %d: %s", code, out)
	}
	if h.lock().Agents[0].Source.Revision != "r1" {
		t.Fatal("a refused update must not touch the lock")
	}
}

func TestUpdateReportsUnavailableSourceWithoutWriting(t *testing.T) {
	h := newHarness(t)
	git := fakeGit{}
	files := reviewerFiles("Review v1.")
	files["agnthub.yaml"] = "schemaVersion: 1\nagents:\n" +
		"  reviewer:\n    portable: agents/reviewer.md\n" +
		"    dependencies:\n      agents:\n        - agent: planner\n" +
		"  planner:\n    portable: agents/planner.md\n"
	files["agents/planner.md"] = agentFile("planner")
	agentsRepo(git, "r1", files)
	h.mustRun(lifecycleRun{git: git}, "add", "acme/agents", "--agent", "reviewer",
		"--harness", "claude", "--yes")
	gone := lifecycleRun{git: fakeGit{}}
	report := h.check(gone)
	if len(report.Agents) != 2 {
		t.Fatalf("want the agent and its companion, got %+v", report)
	}
	for _, entry := range report.Agents {
		if entry.Status != "unavailable" || entry.Error == "" {
			t.Fatalf("an unreachable source and its companions must be unavailable: %+v", report)
		}
	}
	if code, out := h.runLifecycle(gone, "update", "--yes"); code == 0 {
		t.Fatalf("update of an unavailable source must fail: %s", out)
	}
}

// resetMachine deletes installed agent files and all user-local state, as in a
// fresh clone on another machine.
func (h *harnessT) resetMachine(projectPaths ...string) {
	h.t.Helper()
	targets := []string{filepath.Join(h.home, ".local")}
	for _, rel := range projectPaths {
		targets = append(targets, filepath.Join(h.project, rel))
	}
	for _, path := range targets {
		if err := os.RemoveAll(path); err != nil {
			h.t.Fatal(err)
		}
	}
}

func TestRestoreRecreatesExactLockedContent(t *testing.T) {
	h := newHarness(t)
	git := fakeGit{}
	agentsRepo(git, "r1", reviewerFiles("Review v1."))
	h.mustRun(lifecycleRun{git: git}, "add", "acme/agents", "--harness", "claude", "--yes")
	want := h.installed(".claude/agents/reviewer.md")
	// The source moves on; restore must still use the locked revision.
	agentsRepo(git, "r2", reviewerFiles("Review v2."))
	h.resetMachine(".claude")

	run := lifecycleRun{git: git}
	if code, _ := h.runLifecycle(run, "restore"); code == 0 {
		t.Fatal("noninteractive restore without --yes must not write")
	}
	h.mustRun(run, "restore", "--yes")
	if got := h.installed(".claude/agents/reviewer.md"); got != want {
		t.Fatalf("restore must reproduce the locked bytes, got:\n%s", got)
	}
	if h.lock().Agents[0].Source.Revision != "r1" {
		t.Fatal("restore must not move the lock")
	}
	if out := h.mustRun(run, "restore", "--yes"); !strings.Contains(out, "Already restored") {
		t.Fatalf("a second restore has nothing to do: %s", out)
	}
}

func TestRestoreRefusesUnavailableMismatchedOrUnsupportedEvidence(t *testing.T) {
	setup := func(t *testing.T) (*harnessT, fakeGit) {
		h := newHarness(t)
		git := fakeGit{}
		agentsRepo(git, "r1", reviewerFiles("Review v1."))
		h.mustRun(lifecycleRun{git: git}, "add", "acme/agents", "--harness", "claude", "--yes")
		h.resetMachine(".claude")
		return h, git
	}
	cases := map[string]func(h *harnessT, git fakeGit){
		"unavailable revision": func(_ *harnessT, git fakeGit) {
			delete(git, "acme/agents#r1")
		},
		"changed source bytes": func(_ *harnessT, git fakeGit) {
			git["acme/agents#r1"] = remoteRepo{revision: "r1", files: reviewerFiles("Forged.")}
		},
		"unsupported converter": func(h *harnessT, _ fakeGit) {
			lock := h.lock()
			lock.Agents[0].Artifacts[0].Converter = "claude/v0"
			if err := lock.Save(filepath.Join(h.project, state.LockFileName)); err != nil {
				h.t.Fatal(err)
			}
		},
		"mismatched artifact digest": func(h *harnessT, _ fakeGit) {
			lock := h.lock()
			lock.Agents[0].Artifacts[0].Digest = state.Digest([]byte("other"))
			if err := lock.Save(filepath.Join(h.project, state.LockFileName)); err != nil {
				h.t.Fatal(err)
			}
		},
	}
	for name, breakIt := range cases {
		t.Run(name, func(t *testing.T) {
			h, git := setup(t)
			breakIt(h, git)
			code, out := h.runLifecycle(lifecycleRun{git: git}, "restore", "--yes")
			if code == 0 {
				t.Fatalf("restore must refuse: %s", out)
			}
			if _, err := os.Stat(
				filepath.Join(h.project, ".claude/agents/reviewer.md"),
			); err == nil {
				t.Fatalf("a refused restore must not write: %s", out)
			}
		})
	}
}

func TestRestoreNeverOverwritesLocalModification(t *testing.T) {
	h := newHarness(t)
	git := fakeGit{}
	agentsRepo(git, "r1", reviewerFiles("Review v1."))
	h.mustRun(lifecycleRun{git: git}, "add", "acme/agents", "--harness", "claude", "--yes")
	path := filepath.Join(h.project, ".claude/agents/reviewer.md")
	writeFile(t, path, "edited\n")
	if code, _ := h.runLifecycle(lifecycleRun{git: git}, "restore", "--yes"); code == 0 ||
		h.installed(".claude/agents/reviewer.md") != "edited\n" {
		t.Fatal("restore must refuse to overwrite a locally modified file")
	}
}

func TestUpdateKeepsRecordedOmpProfileBinding(t *testing.T) {
	h := newHarness(t)
	for _, profile := range []string{"hep", "work"} {
		if err := os.MkdirAll(
			filepath.Join(h.home, ".omp", "profiles", profile),
			0o750,
		); err != nil {
			t.Fatal(err)
		}
	}
	git := fakeGit{}
	agentsRepo(git, "r1", reviewerFiles("Review v1."))
	hep := filepath.Join(h.home, ".omp", "profiles", "hep", "agent", "agents", "reviewer.md")
	work := filepath.Join(h.home, ".omp", "profiles", "work", "agent", "agents", "reviewer.md")
	// Installed while omp's active profile was hep, without an explicit --extra.
	h.mustRun(lifecycleRun{git: git, env: map[string]string{"OMP_PROFILE": "hep"}},
		"add", "acme/agents", "--harness", "omp", "--global", "--yes")

	agentsRepo(git, "r2", reviewerFiles("Review v2."))
	active := lifecycleRun{git: git, env: map[string]string{"OMP_PROFILE": "work"}}
	report := h.check(active, "--global")
	if bindings := report.Agents[0].Artifacts[0].Bindings; len(bindings) != 1 ||
		bindings[0]["profile"] != "hep" {
		t.Fatalf("check must show the recorded profile binding: %+v", report)
	}
	if code, out := h.runLifecycle(active, "update", "--global", "--yes",
		"--extra", "omp.profile=work"); code == 0 {
		t.Fatalf("an explicit different profile must not retarget the installation: %s", out)
	}
	h.mustRun(active, "update", "--global", "--yes")
	content, err := os.ReadFile(hep)
	if err != nil || !strings.Contains(string(content), "Review v2.") {
		t.Fatalf("update must write the recorded hep profile: %v %s", err, content)
	}
	if _, err := os.Stat(work); err == nil {
		t.Fatal("update must not follow the newly active work profile")
	}
}

func TestMutatingOmpProjectBindingMustBeExplicitWhenAmbiguousOrAbsent(t *testing.T) {
	h := newHarness(t)
	git := fakeGit{}
	agentsRepo(git, "r1", reviewerFiles("Review v1."))
	run := lifecycleRun{git: git}
	// The shared project file is bound to two omp profiles.
	h.mustRun(run, "add", "acme/agents", "--harness", "omp", "--yes", "--extra", "omp.profile=a")
	h.mustRun(run, "add", "acme/agents", "--harness", "omp", "--yes", "--extra", "omp.profile=b")
	agentsRepo(git, "r2", reviewerFiles("Review v2."))
	if code, out := h.runLifecycle(run, "update", "--yes"); code == 0 ||
		!strings.Contains(out, "--extra") {
		t.Fatalf("an ambiguous binding must fail noninteractively and name --extra: %s", out)
	}
	chooser := &scriptedPrompter{selections: [][]string{{"profile=a"}}, confirms: []bool{true}}
	h.mustRun(lifecycleRun{git: git, prompter: chooser}, "update")
	if len(chooser.offered) != 1 || len(chooser.offered[0]) != 2 {
		t.Fatalf("the prompt must offer both recorded bindings: %+v", chooser.offered)
	}
	if !strings.Contains(h.installed(".omp/agents/reviewer.md"), "Review v2.") {
		t.Fatal("the chosen binding must be updated")
	}

	// A fresh machine has no recorded binding: restore needs one explicitly.
	h.resetMachine(".omp")
	if code, out := h.runLifecycle(run, "restore", "--yes"); code == 0 ||
		!strings.Contains(out, "omp.profile") {
		t.Fatalf("restore without a recorded omp binding must require --extra: %s", out)
	}
	h.mustRun(run, "restore", "--yes", "--extra", "omp.profile=a")
	ownership, err := state.LoadOwnership(
		state.OwnershipPath(filepath.Join(h.home, ".local", "state")),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(ownership.Records) != 1 || ownership.Records[0].Settings["profile"] != "a" {
		t.Fatalf("restore must record the explicit binding: %+v", ownership.Records)
	}
}

func TestLockNeverHoldsUserLocalIdentities(t *testing.T) {
	h := newHarness(t)
	git := fakeGit{}
	agentsRepo(git, "r1", reviewerFiles("Review v1."))
	run := lifecycleRun{git: git}
	h.mustRun(run, "add", "acme/agents", "--harness", "claude", "--harness", "omp", "--yes",
		"--extra", "omp.profile=a")
	agentsRepo(git, "r2", reviewerFiles("Review v2."))
	h.mustRun(run, "update", "--yes")
	h.resetMachine(".claude", ".omp")
	h.mustRun(run, "restore", "--yes", "--extra", "omp.profile=a")
	content, err := os.ReadFile(filepath.Join(h.project, state.LockFileName))
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{h.home, h.project, "installedAt", "profile", "settings"} {
		if strings.Contains(string(content), private) {
			t.Fatalf("the shareable lock must not contain %q:\n%s", private, content)
		}
	}
}

func TestUpdateAndRestoreKeepSkillsPinnedToTheLockedRevision(t *testing.T) {
	h := newHarness(t)
	skillsRepo := newSkillFixture(h, map[string]string{"code-review": "1.2.0"})
	git := skillsRepo.git()
	pinned := git["acme/skills#v1"]
	git["acme/skills#"+skillsRevision] = pinned
	agentFiles := func(body string) map[string]string {
		return map[string]string{
			"agnthub.yaml": "schemaVersion: 1\nagents:\n" +
				"  reviewer:\n    portable: agents/reviewer.md\n" +
				"    dependencies:\n      skills:\n" +
				"        - source: acme/skills#v1\n          skill: code-review\n" + boundsV1,
			"agents/reviewer.md": agentFile("reviewer") + body,
		}
	}
	agentsRepo(git, "r1", agentFiles(""))
	run := lifecycleRun{git: git, env: map[string]string{
		"FAKE_SRC": skillsRepo.src, "FAKE_ARGS": skillsRepo.args,
	}}
	h.mustRun(run, "add", "acme/agents", "--harness", "claude", "--yes")
	agentsRepo(git, "r2", agentFiles("Second revision.\n"))
	h.mustRun(run, "update", "--yes")
	if skills := h.lock().Skills; len(skills) != 1 || skills[0].Source.Revision != skillsRevision {
		t.Fatalf("update must keep the locked skill revision: %+v", skills)
	}

	h.resetMachine(".claude")
	// The skill's ref moves on; restore must install the locked revision.
	git["acme/skills#v1"] = remoteRepo{revision: "6666666666666666666666666666666666666666"}
	h.mustRun(run, "restore", "--yes")
	calls := skillsRepo.calls()
	if last := calls[len(calls)-1]; last[0] != "acme/skills#"+skillsRevision {
		t.Fatalf("restore must pin the skill backend to the locked revision: %v", last)
	}
	if _, err := os.Stat(
		filepath.Join(h.project, ".claude/skills/code-review/SKILL.md"),
	); err != nil {
		t.Fatalf("restore must reinstall the locked skill: %v", err)
	}

	h.resetMachine(".claude")
	tampered := remoteRepo{revision: skillsRevision, files: map[string]string{}}
	for name, content := range pinned.files {
		tampered.files[name] = content + "tampered\n"
	}
	git["acme/skills#"+skillsRevision] = tampered
	if code, out := h.runLifecycle(run, "restore", "--yes"); code == 0 {
		t.Fatalf("restore must refuse skill bytes that differ from the lock: %s", out)
	}
	if _, err := os.Stat(filepath.Join(h.project, ".claude/agents/reviewer.md")); err == nil {
		t.Fatal("a refused restore must not write agents")
	}
}
