package cli_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/bytedance/sonic"

	"github.com/heynemann/agnthub/state"
)

type removeResultJSON struct {
	Status string `json:"status"`
	Items  []struct {
		Kind     string            `json:"kind"`
		Name     string            `json:"name"`
		Harness  string            `json:"harness"`
		Settings map[string]string `json:"settings"`
		Action   string            `json:"action"`
		Result   string            `json:"result"`
		Reason   string            `json:"reason"`
	} `json:"items"`
	DependenciesKept []struct {
		Kind string `json:"kind"`
		Name string `json:"name"`
	} `json:"dependenciesKept"`
}

func parseRemoval(t *testing.T, out string) removeResultJSON {
	t.Helper()
	var result removeResultJSON
	if err := sonic.UnmarshalString(out, &result); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	return result
}

// actions maps "name/harness[/settings]" to each item's action.
func (r removeResultJSON) actions() map[string]string {
	actions := map[string]string{}
	for _, item := range r.Items {
		key := item.Name + "/" + item.Harness
		if profile := item.Settings["profile"]; profile != "" {
			key += "/" + profile
		}
		actions[key] = item.Action
	}
	return actions
}

func (h *harnessT) exists(rel string) bool {
	h.t.Helper()
	_, err := os.Lstat(filepath.Join(h.project, rel))
	if err != nil && !os.IsNotExist(err) {
		h.t.Fatal(err)
	}
	return err == nil
}

func (h *harnessT) records() []state.Record {
	h.t.Helper()
	ownership, err := state.LoadOwnership(
		state.OwnershipPath(filepath.Join(h.home, ".local", "state")))
	if err != nil {
		h.t.Fatal(err)
	}
	return ownership.Records
}

func lockedAgents(lock *state.Lock) []string {
	var names []string
	for _, locked := range lock.Agents {
		for _, artifact := range locked.Artifacts {
			names = append(names, locked.Agent+"/"+artifact.Harness)
		}
	}
	slices.Sort(names)
	return names
}

// installReviewer installs reviewer with planner, linter and style-guide companions.
func installReviewer(h *harnessT, harnesses ...string) fakeGit {
	h.t.Helper()
	h.writeReviewerSource(reviewerManifest)
	git := fakeGit{"acme/tools#v1": toolsRepo("1111")}
	args := []string{"add", h.source, "--agent", "reviewer", "--yes"}
	for _, id := range harnesses {
		args = append(args, "--harness", id)
	}
	if code, out := h.runWith(git, nil, args...); code != 0 {
		h.t.Fatalf("install exit %d: %s", code, out)
	}
	return git
}

func TestRemoveDeletesOwnedArtifactsAndKeepsDependenciesAndNativeConfig(t *testing.T) {
	h := newHarness(t)
	git := installReviewer(h, "claude", "codex")
	native := map[string]string{
		".claude/agents/mine.md": "my own agent\n",
		".claude/settings.json":  "{}\n",
		".codex/config.toml":     "model = \"x\"\n",
	}
	for rel, content := range native {
		writeFile(t, filepath.Join(h.project, rel), content)
	}

	code, out := h.runWith(git, nil, "remove", "reviewer", "--json")
	if code == 0 || h.exists(".claude/agents/reviewer.md") == false {
		t.Fatalf("an unattended removal without --yes must not change anything: %s", out)
	}

	code, out = h.runWith(git, nil, "remove", "reviewer", "--yes", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	result := parseRemoval(t, out)
	if result.Status != "applied" || len(result.DependenciesKept) != 2 {
		t.Fatalf("removal must report the dependencies it keeps: %s", out)
	}
	for _, rel := range []string{".claude/agents/reviewer.md", ".codex/agents/reviewer.toml"} {
		if h.exists(rel) {
			t.Fatalf("%s must be removed", rel)
		}
	}
	for _, rel := range []string{
		".claude/agents/planner.md", ".claude/agents/linter.md",
		".claude/agents/style-guide.md", ".codex/agents/planner.toml",
	} {
		h.installed(rel)
	}
	for rel, content := range native {
		if h.installed(rel) != content {
			t.Fatalf("unrelated native configuration %s changed", rel)
		}
	}
	for _, name := range lockedAgents(h.lock()) {
		if strings.HasPrefix(name, "reviewer/") {
			t.Fatalf("lock still lists %s", name)
		}
	}
	for _, record := range h.records() {
		if record.Agent == "reviewer" {
			t.Fatalf("ownership still records %+v", record)
		}
	}
	if code, out = h.runWith(git, nil, "remove", "mine", "--yes"); code == 0 ||
		!strings.Contains(out, "not installed") || h.installed(".claude/agents/mine.md") == "" {
		t.Fatalf("an unmanaged agent can never be selected: %s", out)
	}
}

func TestRemoveRefusesChangedFilesAndRequiredAgents(t *testing.T) {
	h := newHarness(t)
	git := installReviewer(h, "claude", "codex")
	before := lockedAgents(h.lock())
	edited := filepath.Join(h.project, ".claude", "agents", "reviewer.md")
	writeFile(t, edited, "edited locally\n")

	code, out := h.runWith(git, nil, "remove", "reviewer", "--yes", "--json")
	if code == 0 {
		t.Fatalf("a removal with a changed file must be refused: %s", out)
	}
	if got := parseRemoval(t, out).actions(); got["reviewer/claude"] != "refuse" {
		t.Fatalf("the changed file must be refused: %v", got)
	}
	if h.installed(".claude/agents/reviewer.md") != "edited locally\n" ||
		!h.exists(".codex/agents/reviewer.toml") {
		t.Fatal("a refused removal must not delete anything")
	}
	if !slices.Equal(lockedAgents(h.lock()), before) {
		t.Fatal("a refused removal must not change the lock")
	}

	code, out = h.runWith(git, nil, "remove", "reviewer", "--harness", "codex", "--yes")
	if code != 0 || h.exists(".codex/agents/reviewer.toml") {
		t.Fatalf("the unchanged codex artifact can be removed alone, exit %d: %s", code, out)
	}
	if !slices.Contains(lockedAgents(h.lock()), "reviewer/claude") {
		t.Fatal("the protected claude artifact must stay locked")
	}

	code, out = h.runWith(git, nil, "remove", "planner", "--harness", "claude", "--yes",
		"--json")
	if code == 0 || !h.exists(".claude/agents/planner.md") {
		t.Fatalf("a companion a remaining agent requires must stay: %s", out)
	}
	if reason := parseRemoval(t, out).Items[0].Reason; !strings.Contains(reason, "reviewer") {
		t.Fatalf("the refusal must name the requiring agent: %s", out)
	}
}

func TestCleanupRemovesOnlyUnrequiredOwnedCompanions(t *testing.T) {
	h := newHarness(t)
	git := installReviewer(h, "claude")
	// auditor shares planner with reviewer.
	h.write("agnthub.yaml", reviewerManifest+
		"  auditor:\n    portable: agents/auditor.md\n"+
		"    dependencies:\n      agents:\n        - agent: planner\n")
	h.write("agents/auditor.md", agentFile("auditor"))
	if code, out := h.runWith(git, nil, "add", h.source, "--agent", "auditor",
		"--harness", "claude", "--yes"); code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}

	code, out := h.runWith(git, nil, "remove", "--clean-dependencies", "--yes", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	for key, action := range parseRemoval(t, out).actions() {
		if action != "preserve" {
			t.Fatalf("every companion is still required, %s: %s", key, action)
		}
	}

	if code, out = h.runWith(git, nil, "remove", "reviewer", "--yes"); code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	code, out = h.runWith(git, nil, "remove", "--clean-dependencies", "--yes", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	got := parseRemoval(t, out).actions()
	if got["planner/claude"] != "preserve" || got["linter/claude"] != "delete" ||
		got["style-guide/claude"] != "delete" {
		t.Fatalf("only companions no remaining agent requires are removed: %v", got)
	}
	if !h.exists(".claude/agents/planner.md") || h.exists(".claude/agents/linter.md") ||
		h.exists(".claude/agents/style-guide.md") {
		t.Fatal("disk must match the cleanup decisions")
	}
	want := []string{"auditor/claude", "planner/claude"}
	if got := lockedAgents(h.lock()); !slices.Equal(got, want) {
		t.Fatalf("lock = %v, want %v", got, want)
	}
	if code, out = h.runWith(git, nil, "remove", "--clean-dependencies", "auditor",
		"--yes"); code == 0 || !strings.Contains(out, "installed directly") {
		t.Fatalf("cleanup never removes a direct agent: %s", out)
	}
}

func TestCleanupTreatsUnknownRequirementsAsInUse(t *testing.T) {
	h := newHarness(t)
	git := installReviewer(h, "claude")
	lock := h.lock()
	for i := range lock.Agents {
		for j := range lock.Agents[i].Artifacts {
			if lock.Agents[i].Agent == "reviewer" {
				lock.Agents[i].Artifacts[j].Requires = nil
			}
		}
	}
	if err := lock.Save(filepath.Join(h.project, state.LockFileName)); err != nil {
		t.Fatal(err)
	}
	code, out := h.runWith(git, nil, "remove", "--clean-dependencies", "--yes", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	for key, action := range parseRemoval(t, out).actions() {
		if action != "preserve" {
			t.Fatalf("a lock without requirements cannot prove %s unused: %s", key, action)
		}
	}
}

func TestCleanupRemovesInstalledSkillsButPreservesReusedAndSharedOnes(t *testing.T) {
	h := newHarness(t)
	h.writeSkillAgent(boundsV1)
	f := newSkillFixture(h, map[string]string{"code-review": "1.2.0"})
	if code, out := f.run("", "add", h.source, "--harness", "claude", "--harness", "codex",
		"--harness", "omp", "--yes"); code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if code, out := f.run("", "remove", "reviewer", "--yes"); code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !h.exists(".claude/skills/code-review") || !h.exists(".agents/skills/code-review") {
		t.Fatal("removing an agent keeps its skills")
	}

	// Cleaning codex alone keeps the directory omp still uses.
	code, out := f.run("", "remove", "--clean-dependencies", "--harness", "codex", "--yes",
		"--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if got := parseRemoval(t, out).actions(); got["code-review/codex"] != "unbind" ||
		!h.exists(".agents/skills/code-review/SKILL.md") {
		t.Fatalf("a shared skill directory stays while omp uses it: %v", got)
	}

	// A changed skill is preserved; an unchanged one is removed.
	writeFile(t, filepath.Join(h.project, ".agents", "skills", "code-review", "notes.md"), "mine")
	code, out = f.run("", "remove", "--clean-dependencies", "--yes", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	got := parseRemoval(t, out).actions()
	if got["code-review/claude"] != "delete" || got["code-review/omp"] != "preserve" {
		t.Fatalf("cleanup = %v", got)
	}
	if h.exists(".claude/skills/code-review") || !h.exists(".agents/skills/code-review/notes.md") {
		t.Fatal("disk must match the cleanup decisions")
	}
	for _, record := range h.ownedSkills() {
		if record.Harness == "claude" || record.Harness == "codex" {
			t.Fatalf("removed bindings must drop their records: %+v", record)
		}
	}

	// A reused skill is never deleted; once unrequired it only leaves the lock.
	h2 := newHarness(t)
	h2.writeSkillAgent(boundsV1)
	f2 := newSkillFixture(h2, map[string]string{"code-review": "1.2.0"})
	reused := filepath.Join(h2.home, ".claude", "skills", "code-review", "SKILL.md")
	writeFile(t, reused, skillMD("code-review", "1.5.0"))
	if code, out := f2.run("", "add", h2.source, "--harness", "claude", "--yes"); code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if code, out := f2.run("", "remove", "reviewer", "--yes"); code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	code, out = f2.run("", "remove", "--clean-dependencies", "--yes", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if got := parseRemoval(t, out).actions(); got["code-review/claude"] != "release" {
		t.Fatalf("a reused skill is released, never deleted: %v", got)
	}
	if _, err := os.Stat(reused); err != nil {
		t.Fatalf("a reused skill must stay on disk: %v", err)
	}
	if len(h2.lock().Skills) != 0 {
		t.Fatalf("an unrequired reused skill leaves the lock: %+v", h2.lock().Skills)
	}
}

func TestRemoveLimitsToTheSelectedOMPProfileBinding(t *testing.T) {
	h := newHarness(t)
	h.write("agents/reviewer.md", reviewer)
	for _, profile := range []string{"hep", "work"} {
		if code, out := h.run("add", h.source, "--harness", "omp",
			"--extra", "omp.profile="+profile, "--yes"); code != 0 {
			t.Fatalf("exit %d: %s", code, out)
		}
	}
	shared := ".omp/agents/reviewer.md"
	if len(h.records()) != 2 {
		t.Fatalf("one shared project file, two profile bindings: %+v", h.records())
	}

	code, out := h.run("remove", "reviewer", "--yes")
	if code == 0 || !strings.Contains(out, "ambiguous") || len(h.records()) != 2 {
		t.Fatalf("an ambiguous profile must be refused unattended: %s", out)
	}
	prompter := &scriptedPrompter{
		selections: [][]string{{"omp profile=hep"}},
		confirms:   []bool{false},
	}
	if code, out = h.runInteractive(prompter, "remove", "reviewer"); code == 0 ||
		len(prompter.offered) != 1 || len(prompter.offered[0]) != 2 {
		t.Fatalf("a terminal user chooses among recorded bindings: %s", out)
	}

	code, out = h.run("remove", "reviewer", "--extra", "omp.profile=hep", "--yes", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if got := parseRemoval(t, out).actions(); got["reviewer/omp/hep"] != "unbind" {
		t.Fatalf("the shared file stays while work uses it: %v", got)
	}
	records := h.records()
	if !h.exists(shared) || len(records) != 1 || records[0].Settings["profile"] != "work" {
		t.Fatalf("only the hep binding goes: %+v", records)
	}

	if code, out = h.run("remove", "reviewer", "--extra", "omp.profile=hep",
		"--yes"); code == 0 || !strings.Contains(out, "no recorded binding") {
		t.Fatalf("a removed binding cannot be selected again: %s", out)
	}
	if code, out = h.run("remove", "reviewer", "--yes"); code != 0 {
		t.Fatalf("the only binding left is unambiguous, exit %d: %s", code, out)
	}
	if h.exists(shared) || len(h.records()) != 0 || len(h.lock().Agents) != 0 {
		t.Fatal("the last binding removes the file, its record and its lock entry")
	}
}

func TestRemoveGlobalProfileLeavesOtherProfiles(t *testing.T) {
	h := newHarness(t)
	h.write("agents/reviewer.md", reviewer)
	for _, profile := range []string{"hep", "work"} {
		if err := os.MkdirAll(
			filepath.Join(h.home, ".omp", "profiles", profile),
			0o750,
		); err != nil {
			t.Fatal(err)
		}
		if code, out := h.run("add", h.source, "--harness", "omp", "--global",
			"--extra", "omp.profile="+profile, "--yes"); code != 0 {
			t.Fatalf("exit %d: %s", code, out)
		}
	}
	code, out := h.run("remove", "reviewer", "--global", "--extra", "omp.profile=work", "--yes")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	agentPath := func(profile string) string {
		return filepath.Join(h.home, ".omp", "profiles", profile, "agent", "agents", "reviewer.md")
	}
	if _, err := os.Stat(agentPath("work")); !os.IsNotExist(err) {
		t.Fatal("the selected profile's agent must be removed")
	}
	if _, err := os.Stat(agentPath("hep")); err != nil {
		t.Fatalf("other profiles keep their installation: %v", err)
	}
}

func TestPartialRemovalFailureKeepsStateMatchingDisk(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("directory permissions do not stop root")
	}
	h := newHarness(t)
	git := installReviewer(h, "claude", "codex")
	agents := filepath.Join(h.project, ".claude", "agents")
	if err := os.Chmod(agents, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(agents, 0o750) })

	code, out := h.runWith(git, nil, "remove", "reviewer", "--yes", "--json")
	if code == 0 {
		t.Fatalf("a failed deletion must fail the command: %s", out)
	}
	result := parseRemoval(t, out)
	results := map[string]string{}
	for _, item := range result.Items {
		results[item.Harness] = item.Result
	}
	if result.Status != "failed" || results["claude"] != "failed" || results["codex"] != "removed" {
		t.Fatalf("each artifact reports what happened: %s", out)
	}
	locked := lockedAgents(h.lock())
	if !slices.Contains(locked, "reviewer/claude") || slices.Contains(locked, "reviewer/codex") {
		t.Fatalf("the lock must match disk: %v", locked)
	}
	if !h.exists(".claude/agents/reviewer.md") || h.exists(".codex/agents/reviewer.toml") {
		t.Fatal("disk must match the reported results")
	}
}

func TestGlobalCleanupKeepsASkillAProjectLockStillUses(t *testing.T) {
	h := newHarness(t)
	h.writeSkillAgent(boundsV1)
	f := newSkillFixture(h, map[string]string{"code-review": "1.2.0"})
	if code, out := f.run("", "add", h.source, "--harness", "claude", "--global",
		"--yes"); code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if code, out := f.run("", "add", h.source, "--harness", "claude", "--yes"); code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if code, out := f.run("", "remove", "reviewer", "--global", "--yes"); code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	code, out := f.run("", "remove", "--clean-dependencies", "--global", "--yes", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if got := parseRemoval(t, out).actions(); got["code-review/claude"] != "unbind" {
		t.Fatalf("the project lock still uses the global skill: %v", got)
	}
	skill := filepath.Join(h.home, ".claude", "skills", "code-review", "SKILL.md")
	if _, err := os.Stat(skill); err != nil {
		t.Fatalf("a skill another lock uses must stay: %v", err)
	}
}
