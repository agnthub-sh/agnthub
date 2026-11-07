package cli_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bytedance/sonic"

	"github.com/agnthub-sh/agnthub/internal/cli"
	"github.com/agnthub-sh/agnthub/platform/env"
	"github.com/agnthub-sh/agnthub/source"
)

// runSplit runs agnthub with separate stdout and stderr.
func (h *harnessT) runSplit(
	git fakeGit,
	extraEnv map[string]string,
	args ...string,
) (code int, stdout, stderr string) {
	h.t.Helper()
	values := map[string]string{"HOME": h.home}
	maps.Copy(values, extraEnv)
	var out, errOut bytes.Buffer
	app := cli.NewApp(cli.App{
		Env:      env.FromMap(values),
		WorkDir:  h.project,
		Stdin:    strings.NewReader(""),
		Stdout:   &out,
		Stderr:   &errOut,
		Now:      func() time.Time { return time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC) },
		LookPath: func(string) (string, error) { return "", os.ErrNotExist },
		Opener:   source.Opener{Git: git},
		Adoption: offline,
	})
	code = app.Run(context.Background(), args)
	return code, out.String(), errOut.String()
}

// snapshotTree maps every file and link below dir to its content, skipping .git
// and the relative directories in skip.
func snapshotTree(t *testing.T, dir string, skip ...string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		rel = filepath.ToSlash(rel)
		for _, s := range append([]string{".git"}, skip...) {
			if rel == s {
				return filepath.SkipDir
			}
		}
		switch {
		case entry.Type()&fs.ModeSymlink != 0:
			target, err := os.Readlink(path)
			files[rel] = "link:" + target
			return err
		case entry.IsDir():
			return nil
		}
		content, err := os.ReadFile(path)
		files[rel] = string(content)
		return err
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	return files
}

// applied copies dir into a fresh Git repository, checks the patch with
// `git apply --check`, applies it and returns the copy.
func applied(t *testing.T, dir, patch string, skip ...string) string {
	t.Helper()
	copyDir := t.TempDir()
	for rel, content := range snapshotTree(t, dir, skip...) {
		path := filepath.Join(copyDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		var err error
		if target, ok := strings.CutPrefix(content, "link:"); ok {
			err = os.Symlink(target, path)
		} else {
			err = os.WriteFile(path, []byte(content), 0o600)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", copyDir}, args...)...)
		cmd.Stdin = strings.NewReader(patch)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s\npatch:\n%s", args, err, out, patch)
		}
	}
	git("init", "-q")
	git("apply", "--check", "-")
	git("apply", "-")
	return copyDir
}

// stateSkip is agnthub's private state below the test HOME, which diffs leave out.
const stateSkip = ".local/state"

// checkDiff runs args with --diff, proves nothing was written and that the diff
// applies to base, then runs args with --yes and proves the result is exactly
// the patched copy, byte for byte. Everything outside base stays as it was.
func (h *harnessT) checkDiff(
	git fakeGit,
	extraEnv map[string]string,
	base string,
	args ...string,
) string {
	h.t.Helper()
	other := h.home
	if base == h.home {
		other = h.project
	}
	beforeBase, beforeOther := snapshotTree(h.t, base), snapshotTree(h.t, other, stateSkip)
	code, patch, stderr := h.runSplit(git, extraEnv, append(args, "--diff")...)
	if code != 0 {
		h.t.Fatalf("%v --diff: exit %d\nstdout:\n%s\nstderr:\n%s", args, code, patch, stderr)
	}
	if !strings.HasPrefix(patch, "# agnthub "+args[0]+" ") ||
		!strings.Contains(patch, "\ndiff --git ") {
		h.t.Fatalf("%v --diff must print a commented git diff on stdout, got:\n%s", args, patch)
	}
	if strings.Contains(patch, "\x1b[") {
		h.t.Fatalf("redirected diff must be plain:\n%q", patch)
	}
	if !maps.Equal(beforeBase, snapshotTree(h.t, base)) ||
		!maps.Equal(beforeOther, snapshotTree(h.t, other, stateSkip)) {
		h.t.Fatalf("%v --diff wrote files", args)
	}
	want := applied(h.t, base, patch, stateSkip)
	if code, out, stderr := h.runSplit(git, extraEnv, append(args, "--yes")...); code != 0 {
		h.t.Fatalf("%v --yes: exit %d\n%s\n%s", args, code, out, stderr)
	}
	got, patched := snapshotTree(h.t, base, stateSkip), snapshotTree(h.t, want, stateSkip)
	if !maps.Equal(got, patched) {
		for rel := range mergeKeys(got, patched) {
			if got[rel] != patched[rel] {
				h.t.Errorf("%s: written %q, diff gave %q", rel, got[rel], patched[rel])
			}
		}
		h.t.Fatalf("%v: the diff is not what was written", args)
	}
	if !maps.Equal(beforeOther, snapshotTree(h.t, other, stateSkip)) {
		h.t.Fatalf("%v wrote outside %s, which its diff cannot show", args, base)
	}
	return patch
}

func mergeKeys(a, b map[string]string) map[string]bool {
	keys := map[string]bool{}
	for k := range a {
		keys[k] = true
	}
	for k := range b {
		keys[k] = true
	}
	return keys
}

func dependencyRepos(body string) fakeGit {
	tools := toolsRepo("1111111111111111")
	git := fakeGit{"acme/tools#v1": tools, "acme/tools#1111111111111111": tools}
	agentsRepo(git, "r-"+body, map[string]string{
		"agnthub.yaml":       reviewerManifest,
		"agents/reviewer.md": agentFile("reviewer") + body + "\n",
		"agents/planner.md":  agentFile("planner"),
	})
	return git
}

func TestDiffIsExactlyWhatEachCommandWritesInEveryHarness(t *testing.T) {
	h := newHarness(t)
	git := dependencyRepos("v1")

	created := h.checkDiff(git, nil, h.project,
		"add", "acme/agents", "--agent", "reviewer", "--harness", "all")
	for _, want := range []string{
		"\nnew file mode 100644\n", "\n--- /dev/null\n", "+++ b/agnthub-lock.json\n",
		"+++ b/.claude/agents/linter.md\n", "+++ b/.claude/agents/style-guide.md\n",
	} {
		if !strings.Contains(created, want) {
			t.Fatalf("create diff lacks %q:\n%s", want, created)
		}
	}
	for _, dir := range []string{
		".claude", ".codex", ".opencode", ".github", ".cursor",
		".gemini", ".omp",
	} {
		if !strings.Contains(created, "diff --git a/"+dir+"/") {
			t.Fatalf("create diff lacks the %s harness:\n%s", dir, created)
		}
	}

	git = dependencyRepos("v2")
	updated := h.checkDiff(git, nil, h.project, "update")
	if !strings.Contains(updated, "\n+v2\n") || !strings.Contains(updated, "\n--- a/") ||
		strings.Contains(updated, "new file mode") {
		t.Fatalf("update diff must change the existing files in place:\n%s", updated)
	}

	if err := os.RemoveAll(filepath.Join(h.project, ".claude")); err != nil {
		t.Fatal(err)
	}
	h.checkDiff(git, nil, h.project, "restore")

	removed := h.checkDiff(git, nil, h.project, "remove", "reviewer")
	if !strings.Contains(removed, "\ndeleted file mode 100644\n") ||
		!strings.Contains(removed, "\n+++ /dev/null\n") {
		t.Fatalf("remove diff must delete files:\n%s", removed)
	}
	h.checkDiff(git, nil, h.project, "remove", "--clean-dependencies")
}

func TestDiffShowsSkillFilesTheInstallerPlacesAndRemoves(t *testing.T) {
	h := newHarness(t)
	h.writeSkillAgent(boundsV1)
	f := newSkillFixture(h, map[string]string{"code-review": "1.2.0"})
	vars := map[string]string{"FAKE_SRC": f.src, "FAKE_ARGS": f.args}

	created := h.checkDiff(f.git(), vars, h.project, "add", h.source,
		"--harness", "claude", "--harness", "codex", "--harness", "omp")
	for _, want := range []string{
		"+++ b/.claude/skills/code-review/SKILL.md\n",
		"+++ b/.agents/skills/code-review/reference.md\n",
		"# Skill files (code-review) are placed by the skill installer",
	} {
		if !strings.Contains(created, want) {
			t.Fatalf("skill diff lacks %q:\n%s", want, created)
		}
	}
	h.checkDiff(f.git(), vars, h.project, "remove", "reviewer")
	cleaned := h.checkDiff(f.git(), vars, h.project, "remove", "--clean-dependencies")
	if !strings.Contains(cleaned, "diff --git a/.claude/skills/code-review/SKILL.md") {
		t.Fatalf("cleanup diff must delete the skill's files:\n%s", cleaned)
	}
}

func TestUserScopeDiffIsRelativeToHome(t *testing.T) {
	h := newHarness(t)
	git := dependencyRepos("v1")
	patch := h.checkDiff(git, nil, h.home,
		"add", "acme/agents", "--agent", "reviewer", "--harness", "all", "--global")
	if !strings.Contains(patch, "# User scope: paths are relative to your home directory "+
		h.home) || !strings.Contains(patch, "+++ b/.config/agnthub/global-lock.json\n") ||
		strings.Contains(patch, "diff --git a/"+strings.TrimPrefix(h.home, "/")) {
		t.Fatalf("user-scope diff must use home-relative paths:\n%s", patch)
	}
	h.checkDiff(git, nil, h.home, "remove", "reviewer", "--global")
}

type diffPlanJSON struct {
	Status string `json:"status"`
	Diff   string `json:"diff"`
	Files  []struct {
		Path   string `json:"path"`
		Action string `json:"action"`
		SHA256 string `json:"sha256"`
	} `json:"files"`
}

func TestJSONDiffCarriesThePatchAndFileDigests(t *testing.T) {
	h := newHarness(t)
	h.write("agents/reviewer.md", reviewer)
	code, out, _ := h.runSplit(nil, nil, "add", h.source, "--harness", "claude", "--diff",
		"--json")
	if code != 0 {
		t.Fatalf("--json --diff writes nothing and succeeds, exit %d: %s", code, out)
	}
	var plan diffPlanJSON
	if err := sonic.UnmarshalString(out, &plan); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if plan.Status != "planned" || !strings.Contains(plan.Diff, "diff --git ") ||
		len(plan.Files) != 2 {
		t.Fatalf("want the planned patch and two files: %s", out)
	}
	code, out, _ = h.runSplit(nil, nil, "add", h.source, "--harness", "claude", "--diff",
		"--json", "--yes")
	if code != 0 {
		t.Fatalf("--diff --yes applies, exit %d: %s", code, out)
	}
	var appliedPlan diffPlanJSON
	if err := sonic.UnmarshalString(out, &appliedPlan); err != nil {
		t.Fatal(err)
	}
	if appliedPlan.Status != "applied" || appliedPlan.Diff != plan.Diff {
		t.Fatalf("the applied report keeps the patch it showed: %s", out)
	}
	for _, file := range plan.Files {
		content, err := os.ReadFile(filepath.Join(h.project, filepath.FromSlash(file.Path)))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(content)
		if file.Action != "create" || file.SHA256 != hex.EncodeToString(sum[:]) {
			t.Fatalf("%s: want create with the written bytes' sha256, got %+v", file.Path, file)
		}
	}
	code, out, _ = h.runSplit(nil, nil, "add", h.source, "--harness", "claude", "--diff",
		"--json")
	var current diffPlanJSON
	if err := sonic.UnmarshalString(out, &current); err != nil || code != 0 {
		t.Fatalf("exit %d, %v: %s", code, err, out)
	}
	if current.Status != "up-to-date" || current.Diff != "" || current.Files == nil ||
		len(current.Files) != 0 {
		t.Fatalf("nothing to change is an empty diff: %s", out)
	}
}

func TestDiffColorsOnlyAColorTerminal(t *testing.T) {
	h := newHarness(t)
	h.write("agents/reviewer.md", reviewer)
	args := []string{"add", h.source, "--harness", "claude", "--diff"}
	_, colored, _ := h.runSplit(nil, map[string]string{
		"TERM": "xterm-256color", "CLICOLOR_FORCE": "1",
	}, args...)
	if !strings.Contains(colored, "\x1b[32m+Review the diff carefully.\x1b[m\n") ||
		!strings.Contains(colored, "\x1b[1mdiff --git ") {
		t.Fatalf("a color terminal gets standard diff colors:\n%q", colored)
	}
	for name, vars := range map[string]map[string]string{
		"NO_COLOR":   {"TERM": "xterm-256color", "CLICOLOR_FORCE": "1", "NO_COLOR": "1"},
		"TERM=dumb":  {"TERM": "dumb"},
		"redirected": nil,
	} {
		_, plain, _ := h.runSplit(nil, vars, args...)
		if strings.Contains(plain, "\x1b[") || !strings.Contains(plain, "+Review the diff") {
			t.Fatalf("%s: diff must be plain:\n%q", name, plain)
		}
	}
}

func TestDiffExitCodes(t *testing.T) {
	h := newHarness(t)
	h.writeTwoAgents()
	blocker := filepath.Join(h.project, ".codex", "agents", "reviewer.toml")
	writeFile(t, blocker, "mine")
	before := snapshotTree(t, h.project)

	code, patch, stderr := h.runSplit(nil, nil, "add", h.source, "--agent", "reviewer",
		"--harness", "claude", "--harness", "codex", "--diff")
	if code != 1 {
		t.Fatalf("a blocked plan exits 1 as without --diff, got %d: %s", code, stderr)
	}
	if !strings.Contains(patch, "+++ b/.claude/agents/reviewer.md\n") ||
		strings.Contains(patch, ".codex/agents/reviewer.toml") ||
		!strings.Contains(patch, "# Blocked items are left out") {
		t.Fatalf("a blocked plan shows its valid items' changes only:\n%s", patch)
	}
	if !maps.Equal(before, snapshotTree(t, h.project)) {
		t.Fatal("--diff must not write")
	}
	code, _, _ = h.runSplit(nil, nil, "add", h.source, "--agent", "reviewer",
		"--harness", "claude", "--harness", "codex", "--diff", "--yes")
	if code != 1 || !maps.Equal(before, snapshotTree(t, h.project)) {
		t.Fatalf("--diff --yes refuses a blocked plan before writing, exit %d", code)
	}

	code, patch, _ = h.runSplit(nil, nil, "add", h.source, "--agent", "reviewer",
		"--harness", "claude", "-d", "-y")
	if code != 0 || !strings.Contains(patch, "+++ b/.claude/agents/reviewer.md") {
		t.Fatalf("-d -y shows the diff and applies, exit %d", code)
	}
	h.installed(".claude/agents/reviewer.md")
	code, patch, _ = h.runSplit(nil, nil, "add", h.source, "--agent", "reviewer",
		"--harness", "claude", "-d")
	if code != 0 || patch != "" {
		t.Fatalf("nothing to change prints no diff and exits 0, exit %d:\n%s", code, patch)
	}
}

func TestDiffCheckHintWorksInAProjectBelowAGitTop(t *testing.T) {
	h := newHarness(t)
	h.write("agents/reviewer.md", reviewer)
	top := filepath.Dir(h.project)
	if out, err := exec.Command("git", "-C", top, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	_, patch, _ := h.runSplit(nil, nil, "add", h.source, "--harness", "claude", "--diff")
	hint := "# Check: git -C " + top + " apply --check --directory=project\n"
	if !strings.Contains(patch, hint) {
		t.Fatalf("want %q in:\n%s", hint, patch)
	}
	cmd := exec.Command("git", "-C", top, "apply", "--check", "-v", "--directory=project", "-")
	cmd.Stdin = strings.NewReader(patch)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "Checking patch project/.claude/agents/") {
		t.Fatalf("the hinted command must check every file, %v:\n%s", err, out)
	}
}

func TestEveryPlanningCommandHasShortYesAndDiff(t *testing.T) {
	h := newHarness(t)
	for _, cmd := range [][]string{
		{"add"}, {"update"}, {"restore"}, {"remove"}, {"mcp", "install"}, {"mcp", "uninstall"},
	} {
		code, help, _ := h.runSplit(nil, nil, append(cmd, "--help")...)
		if code != 0 || !strings.Contains(help, "-y --yes") {
			t.Fatalf("%v: want -y --yes, exit %d:\n%s", cmd, code, help)
		}
		if cmd[0] != "mcp" && !strings.Contains(help, "-d --diff") {
			t.Fatalf("%v: want -d --diff:\n%s", cmd, help)
		}
	}
}
