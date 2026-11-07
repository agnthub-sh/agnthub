//go:build e2e && linux

package e2e

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/bytedance/sonic"
)

// diffs: --diff prints exactly what the same command writes with --yes, for a
// create, an update, a deletion and a dependency closure with a skill.
func (s *suite) diffs(t *testing.T) {
	h := s.harnessFlag()
	evolving := gitBase + "evolving.git#v1"
	add := action{args: argv("add", evolving, "--agent", "drifter", h, "--yes")}
	moveV1 := action{
		note: "move tag v1 of evolving.git to its next revision",
		do:   func(t *testing.T) { moveTag(t, runRoot, "evolving", "v1") },
	}
	steps := []struct {
		step
		// again redoes what the step's setup did that ends with the step's subtest.
		again func(t *testing.T)
	}{
		{
			step: step{
				name: "diff-add",
				args: argv("add", evolving, "--agent", "drifter", h, "--diff"),
			},
		},
		{
			step: step{
				name:   "diff-update",
				before: []action{add, moveV1},
				args:   argv("update", "--diff"),
			},
			again: moveV1.do,
		},
		{
			step: step{
				name:   "diff-remove",
				before: []action{add},
				args:   argv("remove", "drifter", "--diff"),
			},
		},
		{step: step{
			name: "diff-dependencies", env: projectTrust[s.harness],
			args: argv("add", gitBase+"manifest.git", "--agent", "lead", h, "--diff"),
		}},
	}
	for _, sc := range steps {
		st := sc.step
		st.planOnly = true
		res := s.run(t, st)
		t.Run(st.name+"-applies", func(t *testing.T) {
			if sc.again != nil {
				sc.again(t)
			}
			s.checkDiffApplies(t, st, res)
		})
	}
}

// checkDiffApplies proves a --diff step's output is what the same command writes
// with --yes: git apply accepts it on a copy of the project, and after the real
// run every file the diff names holds the patched copy's bytes (read through
// links, as the Harness reads them). Any other change is agnthub's private state
// or the skill installer's own bookkeeping, which the diff's header excludes.
func (s *suite) checkDiffApplies(t *testing.T, st step, res *result) {
	t.Helper()
	patch := res.stdout
	if res.exit != 0 || !bytes.Contains(patch, []byte("\ndiff --git a/")) {
		t.Fatalf("--diff: exit %d, stdout:\n%s\nstderr:\n%s", res.exit, patch, res.stderr)
	}
	copyDir := filepath.Join(scratchDir, "patched")
	if err := os.CopyFS(copyDir, os.DirFS(projectDir)); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"apply", "--check", "-"}, {"apply", "-"}} {
		cmd := exec.Command("git", append([]string{"-C", copyDir}, args...)...)
		cmd.Stdin = bytes.NewReader(patch)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	before := snapshot(t)
	args := slices.DeleteFunc(slices.Clone(st.args), func(a string) bool { return a == "--diff" })
	exit, stdout, stderr := s.agnthub(t, st.env, argv(args, "--yes", "--json"))
	if exit != 0 {
		t.Fatalf("%v --yes: exit %d\n%s", args, exit, stderr)
	}
	var applied struct {
		SkillEffects []struct {
			Path string `json:"path"`
		} `json:"skillEffects"`
	}
	if err := sonic.Unmarshal(stdout, &applied); err != nil {
		t.Fatalf("%v: %s", err, stdout)
	}
	named := map[string]bool{}
	for line := range strings.SplitSeq(string(patch), "\n") {
		if rest, ok := strings.CutPrefix(line, "diff --git a/"); ok {
			rel, _, _ := strings.Cut(rest, " b/")
			named[rel] = true
		}
	}
	for rel := range named {
		got, gotErr := os.ReadFile(filepath.Join(projectDir, rel))
		want, wantErr := os.ReadFile(filepath.Join(copyDir, rel))
		if !bytes.Equal(got, want) || errors.Is(gotErr, fs.ErrNotExist) !=
			errors.Is(wantErr, fs.ErrNotExist) {
			t.Errorf("%s: written %q (%v), diff gave %q (%v)", rel, got, gotErr, want, wantErr)
		}
	}
	bookkeeping := func(name string) bool {
		if strings.HasPrefix(name, "home/.local/state/agnthub/") {
			return true
		}
		for _, effect := range applied.SkillEffects {
			path := effect.Path
			if !filepath.IsAbs(path) {
				path = filepath.Join(projectDir, path)
			}
			for prefix, root := range map[string]string{"project/": projectDir, "home/": homeDir} {
				rel, ok := strings.CutPrefix(name, prefix)
				if ok && within(path, filepath.Join(root, rel)) {
					return true
				}
			}
		}
		return false
	}
	after := snapshot(t)
	for name := range mergedNames(before, after) {
		old, wasThere := before[name]
		now, isThere := after[name]
		if wasThere == isThere && bytes.Equal(old.content, now.content) && old.mode == now.mode {
			continue
		}
		rel, inProject := strings.CutPrefix(name, "project/")
		if inProject && named[rel] || bookkeeping(name) {
			continue
		}
		t.Errorf("%v --yes changed %s, which its diff does not show", args, name)
	}
}

func within(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, "../")
}

func mergedNames(a, b map[string]file) map[string]bool {
	names := map[string]bool{}
	for name := range a {
		names[name] = true
	}
	for name := range b {
		names[name] = true
	}
	return names
}
