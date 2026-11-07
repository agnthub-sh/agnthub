package gitdiff_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/agnthub-sh/agnthub/gitdiff"
	"github.com/agnthub-sh/agnthub/state"
)

func lines(n int, change func(i int) string) string {
	var b strings.Builder
	for i := range n {
		if s := change(i); s != "" {
			b.WriteString(s + "\n")
			continue
		}
		b.WriteString("line " + strconv.Itoa(i) + "\n")
	}
	return b.String()
}

func git(t *testing.T, dir string, stdin []byte, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Stdin = bytes.NewReader(stdin)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s\npatch:\n%s", args, err, out, stdin)
	}
}

// TestPatchesApplyWithGit writes old content, records the new content through a
// state.Recorder, and proves `git apply` turns the old tree into the new one.
func TestPatchesApplyWithGit(t *testing.T) {
	binaryOld := append([]byte("PNG\x00\x01"), bytes.Repeat([]byte{0xff, 0x00, 0x7f}, 300)...)
	binaryNew := append([]byte("PNG\x00\x02"), bytes.Repeat([]byte{0x01, 0x00, 0x80}, 200)...)
	cases := map[string]struct {
		old   map[string][]byte // nil content: absent
		write map[string][]byte
		perm  map[string]os.FileMode
		del   []string
		tree  []string
	}{
		"create, empty and nested": {
			write: map[string][]byte{
				"a.md": []byte("one\ntwo\n"), "empty": {}, "d/e/f.txt": []byte("x"),
			},
		},
		"update far apart and at the edges": {
			old: map[string][]byte{"big.txt": []byte(lines(200, func(int) string { return "" }))},
			write: map[string][]byte{"big.txt": []byte(lines(201, func(i int) string {
				switch i {
				case 0, 100, 200:
					return "changed " + strconv.Itoa(i)
				}
				return ""
			}))},
		},
		"repeated lines": {
			old:   map[string][]byte{"r": []byte("}\n}\n\n}\nend\n")},
			write: map[string][]byte{"r": []byte("}\n\n}\n}\n\nend\n")},
		},
		"missing final newline": {
			old: map[string][]byte{
				"gain": []byte("a\nb"), "lose": []byte("a\nb\n"), "keep": []byte("a\nb\nc"),
			},
			write: map[string][]byte{
				"gain": []byte("a\nb\n"), "lose": []byte("a\nc"), "keep": []byte("z\nb\nc"),
			},
		},
		"binary create, update and delete": {
			old: map[string][]byte{"img.png": binaryOld, "gone.bin": binaryOld},
			write: map[string][]byte{
				"img.png": binaryNew, "new.bin": binaryNew,
			},
			del: []string{"gone.bin"},
		},
		"delete, mode change and odd names": {
			old: map[string][]byte{
				"old.md": []byte(
					"bye\n",
				),
				"run.sh":       []byte("echo\n"),
				"s p a c e.md": []byte("a\n"),
				"ünï.md":       []byte("a\n"),
				"q\"uote.md":   []byte("a\n"),
			},
			write: map[string][]byte{
				"run.sh": []byte("echo\n"), "s p a c e.md": []byte("b\n"),
				"ünï.md": []byte("b\n"), "q\"uote.md": []byte("b\n"),
			},
			perm: map[string]os.FileMode{"run.sh": 0o755},
			del:  []string{"old.md"},
		},
		"tree removal": {
			old: map[string][]byte{
				"skill/SKILL.md": []byte("s\n"), "skill/ref/a.md": []byte("a\n"),
				"other.md": []byte("o\n"),
			},
			tree: []string{"skill"},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			for rel, content := range tc.old {
				path := filepath.Join(dir, rel)
				if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, content, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			recorder := &state.Recorder{}
			for rel, content := range tc.write {
				perm := tc.perm[rel]
				if perm == 0 {
					perm = 0o644
				}
				_ = recorder.WriteFile(filepath.Join(dir, rel), content, perm)
			}
			for _, rel := range tc.del {
				_ = recorder.Remove(filepath.Join(dir, rel))
			}
			for _, rel := range tc.tree {
				_ = recorder.RemoveAll(filepath.Join(dir, rel))
			}
			files, err := gitdiff.FromChanges(recorder.Changes(), gitdiff.Options{Base: dir})
			if err != nil {
				t.Fatal(err)
			}
			patch := (&gitdiff.Patch{Comments: []string{"a comment", ""}, Files: files}).String()
			git(t, dir, nil, "init", "-q")
			git(t, dir, []byte(patch), "apply", "--check", "-")
			git(t, dir, []byte(patch), "apply", "-")
			git(t, dir, []byte(patch), "apply", "--check", "-R", "-")
			for rel, content := range tc.write {
				got, err := os.ReadFile(filepath.Join(dir, rel))
				if err != nil || !bytes.Equal(got, content) {
					t.Fatalf("%s: got %q (%v), want %q\n%s", rel, got, err, content, patch)
				}
				info, _ := os.Stat(filepath.Join(dir, rel))
				if exec := info.Mode().Perm()&0o100 != 0; exec != (tc.perm[rel]&0o100 != 0) {
					t.Fatalf("%s: executable %v\n%s", rel, exec, patch)
				}
			}
			for _, rel := range append(tc.del, tc.tree...) {
				if _, err := os.Lstat(filepath.Join(dir, rel)); !os.IsNotExist(err) {
					t.Fatalf("%s must be deleted (%v)\n%s", rel, err, patch)
				}
			}
		})
	}
}

func TestUnchangedWritesAndExcludedDirectoriesAreLeftOut(t *testing.T) {
	dir := t.TempDir()
	same := filepath.Join(dir, "same.json")
	if err := os.WriteFile(same, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	recorder := &state.Recorder{}
	_ = recorder.WriteFile(same, []byte("{}\n"), 0o644)
	_ = recorder.WriteFile(filepath.Join(dir, "state", "records.json"), []byte("x"), 0o600)
	_ = recorder.Remove(filepath.Join(dir, "absent.md"))
	files, err := gitdiff.FromChanges(recorder.Changes(), gitdiff.Options{
		Base: dir, Exclude: []string{filepath.Join(dir, "state")},
	})
	if err != nil || len(files) != 0 {
		t.Fatalf("want no file changes, got %+v (%v)", files, err)
	}
	if patch := (&gitdiff.Patch{Comments: []string{"c"}}); !patch.Empty() {
		t.Fatal("a patch without files is empty")
	}
}

func TestSymlinkRemovalIsALinkDeletion(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "shared", "skill"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "shared", "skill", "SKILL.md"), []byte("s\n"),
		0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "skill")
	if err := os.Symlink(filepath.Join("shared", "skill"), link); err != nil {
		t.Fatal(err)
	}
	recorder := &state.Recorder{}
	_ = recorder.Remove(link)
	files, err := gitdiff.FromChanges(recorder.Changes(), gitdiff.Options{Base: dir})
	if err != nil {
		t.Fatal(err)
	}
	patch := (&gitdiff.Patch{Files: files}).String()
	if !strings.Contains(patch, "deleted file mode 120000") {
		t.Fatalf("a removed link is a link deletion:\n%s", patch)
	}
	git(t, dir, nil, "init", "-q")
	git(t, dir, []byte(patch), "apply", "-")
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatal("the link must be gone")
	}
	if _, err := os.Stat(filepath.Join(dir, "shared", "skill", "SKILL.md")); err != nil {
		t.Fatal("the link's target stays")
	}
}
