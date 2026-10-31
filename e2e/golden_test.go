//go:build e2e && linux

package e2e

import (
	"bytes"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"testing"
)

// volatile are values that legitimately differ between runs; they are replaced
// before comparing or writing goldens.
var volatile = []struct {
	pattern *regexp.Regexp
	replace string
}{
	// Ownership records note when each file was installed.
	{regexp.MustCompile(`"installedAt": "[^"]*"`), `"installedAt": "<time>"`},
	// skills.sh's terminal output (banner, spinner frames) is not agnthub's
	// contract and its spinner depends on timing; its effects are compared instead.
	{regexp.MustCompile(`"output": "(?:[^"\\]|\\.)*"`), `"output": "<skill installer output>"`},
}

func normalize(content []byte) []byte {
	for _, v := range volatile {
		content = v.pattern.ReplaceAll(content, []byte(v.replace))
	}
	return content
}

// snapshot reads the project and home trees, keyed "project/<rel>" and
// "home/<rel>".
func snapshot(t *testing.T) map[string]file {
	t.Helper()
	files := map[string]file{}
	for prefix, root := range map[string]string{"project": projectDir, "home": homeDir} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			name := prefix + "/" + filepath.ToSlash(rel)
			info, err := d.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("%s is not a regular file (%s)", name, info.Mode())
			}
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			files[name] = file{mode: info.Mode().Perm(), content: normalize(content)}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return files
}

// goldenFiles is what a step's golden directory holds: the command and its exit
// status and stderr (run.txt), stdout (stdout.json), every file of the project
// and home trees with its mode (files.txt) and each file's exact bytes.
func goldenFiles(env, args []string, res *result) map[string][]byte {
	var run bytes.Buffer
	fmt.Fprintf(&run, "$ %s\nexit status: %d\n",
		shellQuote(append(append(slices.Clone(env), "agnthub"), args...)), res.exit)
	if len(res.stderr) > 0 {
		fmt.Fprintf(&run, "--- stderr\n%s", normalize(res.stderr))
	}
	out := map[string][]byte{"run.txt": run.Bytes()}
	if len(res.stdout) > 0 {
		out["stdout.json"] = normalize(res.stdout)
	}
	names := make([]string, 0, len(res.files))
	for name := range res.files {
		names = append(names, name)
	}
	slices.Sort(names)
	var listing bytes.Buffer
	for _, name := range names {
		fmt.Fprintf(&listing, "%04o %s\n", res.files[name].mode, name)
		out[name] = res.files[name].content
	}
	out["files.txt"] = listing.Bytes()
	return out
}

var shellSafe = regexp.MustCompile(`^[A-Za-z0-9@%_+=:,./#-]+$`)

func shellQuote(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		if shellSafe.MatchString(arg) {
			quoted[i] = arg
		} else {
			quoted[i] = "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
		}
	}
	return strings.Join(quoted, " ")
}

// compareGolden compares want with testdata/golden/<harness>/<step>, or rewrites
// that directory with -update.
func (s *suite) compareGolden(t *testing.T, step string, want map[string][]byte) {
	t.Helper()
	dir := filepath.Join(s.golden, step)
	if *update {
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
		for name, content := range want {
			path := filepath.Join(dir, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, content, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return
	}
	got := map[string][]byte{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		content, err := os.ReadFile(path)
		got[filepath.ToSlash(rel)] = content
		return err
	})
	if err != nil {
		t.Fatalf("read golden %s: %v (run make e2e-update HARNESS=%s and review the diff)",
			dir, err, s.harness)
	}
	names := make([]string, 0, len(want)+len(got))
	for name := range want {
		names = append(names, name)
	}
	for name := range got {
		if _, ok := want[name]; !ok {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	for _, name := range names {
		golden, inGolden := got[name]
		actual, inRun := want[name]
		switch {
		case !inRun:
			t.Errorf("%s/%s: in the golden, not produced by this run", step, name)
		case !inGolden:
			t.Errorf("%s/%s: produced by this run, not in the golden:\n%s", step, name, actual)
		case !bytes.Equal(golden, actual):
			t.Errorf("%s/%s differs from the golden:\n%s", step, name, lineDiff(golden, actual))
		}
	}
	if t.Failed() {
		t.Logf("if the change is intended, run make e2e-update HARNESS=%s and review the diff",
			s.harness)
	}
}

// lineDiff shows the first differing line with a little context.
func lineDiff(golden, actual []byte) string {
	g := strings.Split(string(golden), "\n")
	a := strings.Split(string(actual), "\n")
	first := 0
	for first < len(g) && first < len(a) && g[first] == a[first] {
		first++
	}
	from := max(first-3, 0)
	var b strings.Builder
	fmt.Fprintf(&b, "first difference at line %d\n--- golden\n", first+1)
	for _, line := range g[from:min(first+4, len(g))] {
		fmt.Fprintf(&b, "  %q\n", line)
	}
	b.WriteString("+++ this run\n")
	for _, line := range a[from:min(first+4, len(a))] {
		fmt.Fprintf(&b, "  %q\n", line)
	}
	return b.String()
}

// finishGoldens reports (or, with -update, removes) golden directories no step
// produced, and gives rewritten goldens the owner of the checkout.
func (s *suite) finishGoldens(t *testing.T) {
	t.Helper()
	if run := flag.Lookup("test.run"); run == nil || run.Value.String() == "" {
		entries, err := os.ReadDir(s.golden)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if slices.Contains(s.ran, entry.Name()) {
				continue
			}
			if !*update {
				t.Errorf(
					"stale golden %s: no step produces it",
					filepath.Join(s.golden, entry.Name()),
				)
				continue
			}
			if err := os.RemoveAll(filepath.Join(s.golden, entry.Name())); err != nil {
				t.Fatal(err)
			}
		}
	}
	if *update {
		chownLike(t, filepath.Dir(s.golden), "testdata")
	}
}

// chownLike gives every file under root the owner of reference, so goldens
// rewritten by the container's root user belong to whoever owns the checkout.
func chownLike(t *testing.T, root, reference string) {
	t.Helper()
	info, err := os.Stat(reference)
	if err != nil {
		t.Fatal(err)
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || os.Geteuid() != 0 {
		return
	}
	err = filepath.WalkDir(root, func(path string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Lchown(path, int(owner.Uid), int(owner.Gid))
	})
	if err != nil {
		t.Fatal(err)
	}
}
