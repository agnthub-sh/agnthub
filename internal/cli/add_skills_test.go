package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bytedance/sonic"

	"github.com/heynemann/agnthub/internal/state"
)

const skillsRevision = "5555555555555555555555555555555555555555"

// fakeBackendScript stands in for skills.sh: it records its arguments, then
// places <skill> where skills.sh would (claude-code into .claude/skills, every
// other target into .agents/skills; under HOME with "global"). FAKE_MODE=fail
// creates the directory and exits 1; FAKE_MODE=wrong installs other content.
const fakeBackendScript = `#!/bin/sh
for arg in "$@"; do printf '%s\n' "$arg"; done >> "$FAKE_ARGS"
printf -- '--\n' >> "$FAKE_ARGS"
case "$3" in claude-code) rel=.claude/skills ;; *) rel=.agents/skills ;; esac
if [ "$4" = global ]; then base="$HOME"; else base="$PWD"; fi
dest="$base/$rel/$2"
/bin/mkdir -p "$dest"
case "$FAKE_MODE" in
fail) echo "backend exploded" >&2; exit 1 ;;
wrong) printf -- '---\nname: %s\ndescription: tampered\n---\nTampered.\n' "$2" > "$dest/SKILL.md"; exit 0 ;;
esac
/bin/cp -R "$FAKE_SRC/$2/." "$dest/"
`

func skillMD(name, version string) string {
	md := "---\nname: " + name + "\ndescription: The " + name + " skill\n"
	if version != "" {
		md += "metadata:\n  version: \"" + version + "\"\n"
	}
	return md + "---\nUse " + name + " " + version + ".\n"
}

// skillFixture is a skills repository served by fakeGit plus the backend's copy.
type skillFixture struct {
	h       *harnessT
	files   map[string]string
	src     string
	args    string
	backend string
}

func newSkillFixture(h *harnessT, skills map[string]string) *skillFixture {
	h.t.Helper()
	base := filepath.Dir(h.home)
	f := &skillFixture{
		h:       h,
		files:   map[string]string{},
		src:     filepath.Join(base, "backend-src"),
		args:    filepath.Join(base, "backend-args"),
		backend: filepath.Join(base, "fake-skills"),
	}
	for name, version := range skills {
		content := skillMD(name, version)
		f.files["skills/"+name+"/SKILL.md"] = content
		f.files["skills/"+name+"/reference.md"] = "Reference for " + name + ".\n"
		writeFile(h.t, filepath.Join(f.src, name, "SKILL.md"), content)
		writeFile(h.t, filepath.Join(f.src, name, "reference.md"), "Reference for "+name+".\n")
	}
	writeFile(h.t, f.backend, fakeBackendScript)
	if err := os.Chmod(f.backend, 0o700); err != nil {
		h.t.Fatal(err)
	}
	writeFile(h.t, filepath.Join(h.home, ".config", "agnthub", "config.yaml"),
		"skillInstaller:\n  executable: "+f.backend+"\n"+
			"  args: [\"{source}\", \"{skill}\", \"{target}\"]\n"+
			"  projectArgs: [project]\n  globalArgs: [global]\n")
	return f
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (f *skillFixture) git() fakeGit {
	return fakeGit{"acme/skills#v1": remoteRepo{revision: skillsRevision, files: f.files}}
}

func (f *skillFixture) run(mode string, args ...string) (int, string) {
	f.h.t.Helper()
	return f.h.runWith(f.git(), map[string]string{
		"FAKE_MODE": mode, "FAKE_SRC": f.src, "FAKE_ARGS": f.args,
	}, args...)
}

// calls returns each backend invocation's arguments.
func (f *skillFixture) calls() [][]string {
	raw, err := os.ReadFile(f.args)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		f.h.t.Fatal(err)
	}
	var calls [][]string
	for call := range strings.SplitSeq(strings.TrimSuffix(string(raw), "--\n"), "--\n") {
		calls = append(calls, strings.Split(strings.TrimSuffix(call, "\n"), "\n"))
	}
	return calls
}

// writeSkillAgent declares reviewer requiring code-review with bounds.
func (h *harnessT) writeSkillAgent(bounds string) {
	h.write("agnthub.yaml", "schemaVersion: 1\nagents:\n"+
		"  reviewer:\n    portable: agents/reviewer.md\n"+
		"    dependencies:\n      skills:\n"+
		"        - source: acme/skills#v1\n          skill: code-review\n"+bounds)
	h.write("agents/reviewer.md", agentFile("reviewer"))
}

const boundsV1 = "          minVersion: \"1.0.0\"\n          maxVersion: \"1.9.9\"\n"

type skillPlanJSON struct {
	Status string `json:"status"`
	Skills []struct {
		Skill    string   `json:"skill"`
		Harness  string   `json:"harness"`
		Decision string   `json:"decision"`
		Path     string   `json:"path"`
		Target   string   `json:"target"`
		Version  string   `json:"version"`
		Command  []string `json:"command"`
		Reason   string   `json:"reason"`
		Shadowed string   `json:"shadowed"`
	} `json:"skills"`
	SkillRuns []struct {
		ExitCode int    `json:"exitCode"`
		Error    string `json:"error"`
	} `json:"skillRuns"`
	SkillEffects []struct {
		Path   string `json:"path"`
		Change string `json:"change"`
	} `json:"skillEffects"`
	SkillFailures []string `json:"skillFailures"`
}

func parseSkillPlan(t *testing.T, out string) skillPlanJSON {
	t.Helper()
	var plan skillPlanJSON
	if err := sonic.UnmarshalString(out, &plan); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	return plan
}

func (h *harnessT) ownedSkills() []state.Record {
	h.t.Helper()
	ownership, err := state.LoadOwnership(
		state.OwnershipPath(filepath.Join(h.home, ".local", "state")))
	if err != nil {
		h.t.Fatal(err)
	}
	var records []state.Record
	for _, record := range ownership.Records {
		if record.Skill != "" {
			records = append(records, record)
		}
	}
	return records
}

func (h *harnessT) absent(rel string) {
	h.t.Helper()
	if _, err := os.Stat(filepath.Join(h.project, rel)); !os.IsNotExist(err) {
		h.t.Fatalf("%s must not be written (err %v)", rel, err)
	}
}

func TestSkillsInstallAndVerifyBeforeAgentsAreWritten(t *testing.T) {
	h := newHarness(t)
	h.writeSkillAgent(boundsV1)
	f := newSkillFixture(h, map[string]string{"code-review": "1.2.0"})

	code, out := f.run("", "add", h.source, "--harness", "claude", "--harness", "codex",
		"--harness", "omp", "--yes", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	plan := parseSkillPlan(t, out)
	if len(plan.Skills) != 3 {
		t.Fatalf("want one skill item per harness: %s", out)
	}
	for _, item := range plan.Skills {
		if item.Decision != "install" || item.Version != "1.2.0" {
			t.Fatalf("want a verified install of 1.2.0: %+v", item)
		}
	}
	calls := f.calls()
	if len(calls) != 2 {
		t.Fatalf("claude and the shared .agents/skills install once each, got %q", calls)
	}
	want := []string{"acme/skills#" + skillsRevision, "code-review", "claude-code", "project"}
	if strings.Join(calls[0], "|") != strings.Join(want, "|") {
		t.Fatalf("backend args = %q, want %q", calls[0], want)
	}
	for _, call := range calls {
		if call[2] == "omp" {
			t.Fatal("skills.sh has no omp target; omp must use a supported shared target")
		}
	}
	for _, rel := range []string{
		".claude/skills/code-review/SKILL.md", ".agents/skills/code-review/reference.md",
		".claude/agents/reviewer.md", ".codex/agents/reviewer.toml", ".omp/agents/reviewer.md",
	} {
		h.installed(rel)
	}
	lock := h.lock()
	if len(lock.Skills) != 2 {
		t.Fatalf("want two locked skill directories: %+v", lock.Skills)
	}
	for _, locked := range lock.Skills {
		if locked.Mode != state.SkillInstalled || locked.Version != "1.2.0" ||
			locked.Source.Revision != skillsRevision || locked.Source.Ref != "v1" ||
			strings.HasPrefix(locked.Path, "/") {
			t.Fatalf("locked skill = %+v", locked)
		}
	}
	if owned := h.ownedSkills(); len(owned) != 3 {
		t.Fatalf("agnthub owns the installed skill for each harness binding: %+v", owned)
	}

	// A rerun recognizes its own verified install and never runs the backend.
	code, out = f.run("fail", "add", h.source, "--harness", "claude", "--harness", "codex",
		"--harness", "omp", "--yes", "--json")
	if code != 0 {
		t.Fatalf("rerun exit %d: %s", code, out)
	}
	for _, item := range parseSkillPlan(t, out).Skills {
		if item.Decision != "unchanged" {
			t.Fatalf("rerun must keep the owned skill: %+v", item)
		}
	}
	if len(f.calls()) != 2 {
		t.Fatal("rerun must not invoke the backend")
	}
}

func TestCompatibleExistingSkillIsReusedWithoutOwnership(t *testing.T) {
	h := newHarness(t)
	h.writeSkillAgent(boundsV1)
	f := newSkillFixture(h, map[string]string{"code-review": "1.2.0"})
	writeFile(t, filepath.Join(h.home, ".claude", "skills", "code-review", "SKILL.md"),
		skillMD("code-review", "1.5.0"))

	code, out := f.run("fail", "add", h.source, "--harness", "claude", "--yes", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	plan := parseSkillPlan(t, out)
	if len(plan.Skills) != 1 || plan.Skills[0].Decision != "reuse" ||
		plan.Skills[0].Version != "1.5.0" {
		t.Fatalf("a compatible global skill satisfies a project agent: %s", out)
	}
	if len(f.calls()) != 0 {
		t.Fatal("reuse must not run the backend")
	}
	h.installed(".claude/agents/reviewer.md")
	if owned := h.ownedSkills(); len(owned) != 0 {
		t.Fatalf("reuse must not acquire ownership: %+v", owned)
	}
	if locked := h.lock().Skills; len(locked) != 1 || locked[0].Mode != state.SkillReused ||
		locked[0].Path != "~/.claude/skills/code-review" {
		t.Fatalf("reused skill must be locked portably as reused: %+v", locked)
	}
}

func TestBlockedSkillsRefuseBeforeAnyWrite(t *testing.T) {
	cases := map[string]struct {
		bounds   string
		versions map[string]string
		existing map[string]string // path under base ("home/..." or "project/...") → version
		harness  []string
		extra    []string
		decision string
		want     string
	}{
		"shadowed by a higher-priority incompatible copy": {
			bounds:   boundsV1,
			versions: map[string]string{"code-review": "1.2.0"},
			existing: map[string]string{
				"home/.claude/skills/code-review":    "3.0.0",
				"project/.claude/skills/code-review": "1.2.0",
			},
			harness:  []string{"claude"},
			decision: "shadowed",
			want:     "shadows the compatible copy",
		},
		"source revision above the maximum": {
			bounds:   boundsV1,
			versions: map[string]string{"code-review": "2.1.0"},
			harness:  []string{"claude", "codex"},
			decision: "unsatisfied",
			want:     "version 2.1.0 is outside >=1.0.0 <=1.9.9",
		},
		"unknown source version": {
			bounds:   "          minVersion: \"1.0.0\"\n",
			versions: map[string]string{"code-review": ""},
			harness:  []string{"omp"},
			decision: "unsatisfied",
			want:     "has no metadata.version",
		},
		"prerelease without a prerelease bound": {
			bounds:   "          minVersion: \"1.0.0\"\n",
			versions: map[string]string{"code-review": "2.0.0-rc.1"},
			harness:  []string{"gemini"},
			decision: "unsatisfied",
			want:     "prerelease",
		},
		"existing copy without a version cannot satisfy bounds": {
			bounds:   boundsV1,
			versions: map[string]string{"code-review": "1.2.0"},
			existing: map[string]string{"project/.agents/skills/code-review": ""},
			harness:  []string{"codex"},
			decision: "conflict",
			want:     "never replaces it",
		},
		"incompatible unbounded copy is never replaced": {
			versions: map[string]string{"code-review": "1.2.0"},
			existing: map[string]string{"project/.agents/skills/code-review": "1.2.0-local"},
			harness:  []string{"cursor"},
			decision: "conflict",
			want:     "differs from acme/skills#v1",
		},
		"omp profile skills shadow the shared .agents copy": {
			bounds:   boundsV1,
			versions: map[string]string{"code-review": "1.2.0"},
			existing: map[string]string{
				"home/.omp/profiles/hep/agent/skills/code-review": "3.0.0",
				"home/.agents/skills/code-review":                 "1.2.0",
			},
			harness:  []string{"omp"},
			extra:    []string{"--extra", "omp.profile=hep"},
			decision: "shadowed",
			want:     "profile hep",
		},
		"cursor loads the user copy too": {
			bounds:   boundsV1,
			versions: map[string]string{"code-review": "1.2.0"},
			existing: map[string]string{
				"project/.agents/skills/code-review": "1.2.0",
				"home/.cursor/skills/code-review":    "0.9.0",
			},
			harness:  []string{"cursor"},
			decision: "conflict",
			want:     "loads every copy",
		},
		"codex cannot select a name two copies share": {
			versions: map[string]string{"code-review": "1.2.0"},
			existing: map[string]string{
				"project/.agents/skills/code-review": "1.2.0",
				"home/.codex/skills/code-review":     "1.2.0",
			},
			harness:  []string{"codex"},
			decision: "conflict",
			want:     "cannot select it by name",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.writeSkillAgent(tc.bounds)
			f := newSkillFixture(h, tc.versions)
			before := map[string]string{}
			for rel, version := range tc.existing {
				base, path, _ := strings.Cut(rel, "/")
				dir := filepath.Join(filepath.Dir(h.home), base, path)
				content := skillMD("code-review", version)
				writeFile(t, filepath.Join(dir, "SKILL.md"), content)
				before[dir] = content
			}
			args := []string{"add", h.source, "--yes", "--json"}
			for _, harnessID := range tc.harness {
				args = append(args, "--harness", harnessID)
			}
			args = append(args, tc.extra...)
			env := map[string]string{"GEMINI_CLI_TRUST_WORKSPACE": "true"}
			env["FAKE_MODE"], env["FAKE_SRC"], env["FAKE_ARGS"] = "", f.src, f.args
			code, out := h.runWith(f.git(), env, args...)
			if code == 0 {
				t.Fatalf("plan must be refused: %s", out)
			}
			plan := parseSkillPlan(t, out)
			if plan.Status != "refused" || len(plan.Skills) == 0 ||
				plan.Skills[0].Decision != tc.decision ||
				!strings.Contains(plan.Skills[0].Reason, tc.want) {
				t.Fatalf("want %s mentioning %q: %s", tc.decision, tc.want, out)
			}
			if len(f.calls()) != 0 {
				t.Fatal("a refused plan must never run the backend")
			}
			for _, rel := range []string{
				".claude/agents/reviewer.md", ".codex/agents/reviewer.toml",
				".omp/agents/reviewer.md", ".gemini/agents/reviewer.md",
				".cursor/agents/reviewer.md", state.LockFileName,
			} {
				h.absent(rel)
			}
			for dir, content := range before {
				got, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
				if err != nil || string(got) != content {
					t.Fatalf("existing skill %s must stay untouched", dir)
				}
			}
		})
	}
}

func TestBackendFailuresLeaveDependentAgentsUnwritten(t *testing.T) {
	for _, mode := range []string{"fail", "wrong"} {
		t.Run(mode, func(t *testing.T) {
			h := newHarness(t)
			h.writeSkillAgent(boundsV1)
			f := newSkillFixture(h, map[string]string{"code-review": "1.2.0"})
			other := filepath.Join(h.project, ".agents", "skills", "other", "SKILL.md")
			writeFile(t, other, skillMD("other", "9.9.9"))

			code, out := f.run(mode, "add", h.source, "--harness", "codex", "--harness", "omp",
				"--yes", "--json")
			if code == 0 {
				t.Fatalf("a %s backend must fail the install: %s", mode, out)
			}
			plan := parseSkillPlan(t, out)
			if plan.Status != "failed" {
				t.Fatalf("status = %q: %s", plan.Status, out)
			}
			if mode == "fail" && (len(plan.SkillRuns) != 1 || plan.SkillRuns[0].ExitCode != 1) {
				t.Fatalf("the failed run must be reported: %s", out)
			}
			if mode == "wrong" && len(plan.SkillFailures) == 0 {
				t.Fatalf("exit 0 with wrong content must fail verification: %s", out)
			}
			created := false
			for _, effect := range plan.SkillEffects {
				if effect.Path == filepath.Join(h.project, ".agents", "skills", "code-review") &&
					effect.Change == "created" {
					created = true
				}
			}
			if !created {
				t.Fatalf("backend-created effects must be reported: %s", out)
			}
			h.absent(".codex/agents/reviewer.toml")
			h.absent(".omp/agents/reviewer.md")
			h.absent(state.LockFileName)
			if got, err := os.ReadFile(
				other,
			); err != nil ||
				string(got) != skillMD("other", "9.9.9") {
				t.Fatal("pre-existing skills must never be deleted or changed")
			}
			if owned := h.ownedSkills(); len(owned) != 0 {
				t.Fatalf("an unverified skill is never owned: %+v", owned)
			}
		})
	}
}

func TestOMPProfileSettingsThatHideASkillBlockTheProfileOnly(t *testing.T) {
	h := newHarness(t)
	h.writeSkillAgent("")
	f := newSkillFixture(h, map[string]string{"code-review": "1.2.0"})
	writeFile(t, filepath.Join(h.home, ".omp", "profiles", "hep", "agent", "config.yml"),
		"disabledExtensions:\n  - skill:code-review\n")

	code, out := f.run("", "add", h.source, "--harness", "omp", "--extra", "omp.profile=hep",
		"--yes", "--json")
	if code == 0 {
		t.Fatalf("a skill the hep profile disables cannot satisfy the agent: %s", out)
	}
	plan := parseSkillPlan(t, out)
	if plan.Skills[0].Decision != "disabled" || !strings.Contains(plan.Skills[0].Reason, "hep") {
		t.Fatalf("want disabled in profile hep: %s", out)
	}
	h.absent(".omp/agents/reviewer.md")

	code, out = f.run("", "add", h.source, "--harness", "omp", "--yes", "--json")
	if code != 0 {
		t.Fatalf("the default profile still sees the skill: exit %d %s", code, out)
	}
	h.installed(".agents/skills/code-review/SKILL.md")
}

func TestInvalidInstallerConfigFailsBeforeRunning(t *testing.T) {
	h := newHarness(t)
	h.writeSkillAgent("")
	f := newSkillFixture(h, map[string]string{"code-review": "1.2.0"})
	writeFile(t, filepath.Join(h.home, ".config", "agnthub", "config.yaml"),
		"skillInstaller:\n  executable: "+f.backend+"\n  args: [\"{source}\", \"{revision}\"]\n")
	code, out := f.run("", "add", h.source, "--harness", "claude", "--yes")
	if code == 0 || !strings.Contains(out, "{revision}") {
		t.Fatalf("want unknown placeholder error, exit %d: %s", code, out)
	}
	if len(f.calls()) != 0 {
		t.Fatal("an invalid configuration must not run anything")
	}
	h.absent(".claude/agents/reviewer.md")
}

func TestSkillPlanIsPreviewedPerHarness(t *testing.T) {
	h := newHarness(t)
	h.writeSkillAgent(boundsV1)
	f := newSkillFixture(h, map[string]string{"code-review": "1.2.0"})
	code, out := f.run("", "add", h.source, "--harness", "claude", "--harness", "omp")
	if code == 0 {
		t.Fatalf("noninteractive preview without --yes must not apply: %s", out)
	}
	for _, want := range []string{
		"code-review", ">=1.0.0 <=1.9.9", "install", "claude-code", "universal",
		filepath.Join(h.project, ".agents", "skills", "code-review"),
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("preview must mention %q:\n%s", want, out)
		}
	}
	if len(f.calls()) != 0 {
		t.Fatal("a preview never runs the backend")
	}
}

func TestProjectInstallTakesPrecedenceOverAnIncompatibleUserCopyItLeavesAlone(t *testing.T) {
	h := newHarness(t)
	h.writeSkillAgent(boundsV1)
	f := newSkillFixture(h, map[string]string{"code-review": "1.2.0"})
	user := filepath.Join(h.home, ".agents", "skills", "code-review", "SKILL.md")
	writeFile(t, user, skillMD("code-review", "0.1.0"))

	code, out := f.run("", "add", h.source, "--harness", "omp", "--yes", "--json")
	if code != 0 {
		t.Fatalf("omp prefers project .agents over ~/.agents: exit %d %s", code, out)
	}
	if item := parseSkillPlan(t, out).Skills[0]; item.Decision != "install" ||
		!strings.Contains(item.Reason, "stays untouched") {
		t.Fatalf("want an install ahead of the user copy: %+v", item)
	}
	if got, err := os.ReadFile(user); err != nil || string(got) != skillMD("code-review", "0.1.0") {
		t.Fatal("the incompatible user copy must stay untouched")
	}
	h.installed(".omp/agents/reviewer.md")
}

func TestUntrustedGeminiWorkspaceCannotSeeProjectSkills(t *testing.T) {
	h := newHarness(t)
	h.writeSkillAgent("")
	f := newSkillFixture(h, map[string]string{"code-review": "1.2.0"})
	code, out := f.run("", "add", h.source, "--harness", "gemini", "--yes", "--json")
	if code == 0 {
		t.Fatalf("an untrusted workspace never loads project skills: %s", out)
	}
	if item := parseSkillPlan(t, out).Skills[0]; item.Decision != "unsupported" ||
		!strings.Contains(item.Reason, "trusted folder") {
		t.Fatalf("want the trust requirement explained: %+v", item)
	}
	h.absent(".gemini/agents/reviewer.md")
}
