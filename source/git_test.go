package source

import (
	"context"
	"crypto/rand"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/agnthub-sh/agnthub/platform/env"
)

type fixture struct {
	t      *testing.T
	dir    string
	base   string
	gitEnv []string
}

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
}

// newFixture creates a source repository with an isolated Git configuration.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	requireGit(t)
	base := t.TempDir()
	f := &fixture{t: t, base: base, dir: filepath.Join(base, "upstream")}
	f.gitEnv = []string{
		"HOME=" + base,
		"PATH=" + env.FromProcess().Get("PATH"),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=" + os.DevNull,
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
	}
	f.git("init", "--quiet", "--initial-branch=main", f.dir)
	f.git("-C", f.dir, "config", "uploadpack.allowFilter", "true")
	f.git("-C", f.dir, "config", "uploadpack.allowAnySHA1InWant", "true")
	return f
}

func (f *fixture) git(args ...string) string {
	f.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Env = f.gitEnv
	out, err := cmd.CombinedOutput()
	if err != nil {
		f.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (f *fixture) write(rel, content string) {
	f.t.Helper()
	path := filepath.Join(f.dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) commit(message string) string {
	f.t.Helper()
	f.git("-C", f.dir, "add", "-A")
	f.git("-C", f.dir, "commit", "--quiet", "--no-verify", "-m", message)
	return f.git("-C", f.dir, "rev-parse", "HEAD")
}

func (f *fixture) locator(ref string) Locator {
	return Locator{
		Kind:     KindGit,
		Host:     "example.test",
		RepoPath: "upstream",
		CloneURL: "file://" + f.dir,
		Ref:      ref,
	}
}

// fetcher returns a fetcher whose user configuration is extra (a gitconfig body).
func (f *fixture) fetcher(extra string) *GitFetcher {
	f.t.Helper()
	global := filepath.Join(f.base, "user.gitconfig")
	if err := os.WriteFile(global, []byte(extra), 0o600); err != nil {
		f.t.Fatal(err)
	}
	return &GitFetcher{
		Env: env.FromMap(map[string]string{
			"HOME":                f.base,
			"PATH":                env.FromProcess().Get("PATH"),
			"GIT_CONFIG_NOSYSTEM": "1",
			"GIT_CONFIG_GLOBAL":   global,
		}),
		TempDir:           filepath.Join(f.base, "tmp"),
		allowFileProtocol: true,
	}
}

func (f *fixture) fetch(fetcher *GitFetcher, ref string) (*Snapshot, error) {
	f.t.Helper()
	if err := os.MkdirAll(fetcher.TempDir, 0o750); err != nil {
		f.t.Fatal(err)
	}
	return fetcher.Fetch(context.Background(), f.locator(ref))
}

func read(t *testing.T, tree Tree, name string) string {
	t.Helper()
	data, err := tree.ReadFile(name, DefaultMaxBlobBytes)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(data)
}

func TestGitFetchResolvesRefsToImmutableCommits(t *testing.T) {
	f := newFixture(t)
	f.write("agents/reviewer.md", "v1\n")
	first := f.commit("first")
	f.git("-C", f.dir, "tag", "v1")
	f.git("-C", f.dir, "tag", "--annotate", "-m", "release", "r1")
	f.write("agents/reviewer.md", "v2\n")
	second := f.commit("second")
	f.git("-C", f.dir, "branch", "stable", first)
	fetcher := f.fetcher("")

	cases := []struct {
		ref, revision, content string
	}{
		{"", second, "v2\n"},
		{"main", second, "v2\n"},
		{"stable", first, "v1\n"},
		{"v1", first, "v1\n"},
		{"r1", first, "v1\n"},
		{first, first, "v1\n"},
	}
	for _, tc := range cases {
		for _, fullFetchBytes := range []int64{0, 1} {
			fetcher.FullFetchBytes = fullFetchBytes
			snapshot, err := f.fetch(fetcher, tc.ref)
			if err != nil {
				t.Fatalf("ref %q: %v", tc.ref, err)
			}
			if snapshot.Revision != tc.revision {
				t.Fatalf("ref %q resolved to %s, want %s", tc.ref, snapshot.Revision, tc.revision)
			}
			if got := read(t, snapshot.Tree, "agents/reviewer.md"); got != tc.content {
				t.Fatalf("ref %q: content %q, want %q", tc.ref, got, tc.content)
			}
			if err := snapshot.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}
	leftovers, err := os.ReadDir(fetcher.TempDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(leftovers) != 0 {
		t.Fatalf("temporary repositories were not removed: %v", leftovers)
	}
}

func TestGitFetchRefusesUnknownRefs(t *testing.T) {
	f := newFixture(t)
	f.write("a.md", "a\n")
	head := f.commit("first")
	fetcher := f.fetcher("")
	for _, ref := range []string{"missing", head[:12]} {
		snapshot, err := f.fetch(fetcher, ref)
		if !errors.Is(err, ErrRefNotFound) {
			if snapshot != nil {
				_ = snapshot.Close()
			}
			t.Fatalf("ref %q: want ErrRefNotFound, got %v", ref, err)
		}
	}
	if leftovers, _ := os.ReadDir(fetcher.TempDir); len(leftovers) != 0 {
		t.Fatalf("failed fetches left temporary repositories: %v", leftovers)
	}
}

// modes runs a test against a whole fetch and a sparse fetch of the same source.
func modes(t *testing.T, test func(t *testing.T, fullFetchBytes int64)) {
	t.Helper()
	t.Run("whole", func(t *testing.T) { test(t, 0) })
	t.Run("sparse", func(t *testing.T) { test(t, 1) })
}

func TestGitTreeExposesLinksAndSubmodulesWithoutContent(t *testing.T) {
	modes(t, func(t *testing.T, fullFetchBytes int64) {
		f := newFixture(t)
		f.write("agents/real.md", "real\n")
		if err := os.Symlink("/etc/passwd", filepath.Join(f.dir, "agents", "link.md")); err != nil {
			t.Fatal(err)
		}
		big := strings.Repeat("x", int(DefaultMaxBlobBytes)+1)
		f.write("agents/big.md", big)
		head := f.commit("first")
		f.git("-C", f.dir, "update-index", "--add", "--cacheinfo", "160000,"+head+",vendor/sub")
		f.git("-C", f.dir, "commit", "--quiet", "--no-verify", "-m", "submodule")

		fetcher := f.fetcher("")
		fetcher.FullFetchBytes = fullFetchBytes
		snapshot, err := f.fetch(fetcher, "")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = snapshot.Close() }()
		tree := snapshot.Tree
		for name, want := range map[string]EntryKind{
			"agents/real.md": EntryFile,
			"agents/link.md": EntrySymlink,
			"vendor/sub":     EntrySubmodule,
			"vendor/sub/x":   EntrySubmodule,
			"agents":         EntryDir,
		} {
			entry, err := tree.Stat(name)
			if err != nil || entry.Kind != want {
				t.Fatalf("%s: got %v %v, want %v", name, entry.Kind, err, want)
			}
		}
		if _, err := tree.Stat("agents/missing.md"); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("missing file: want ErrNotExist, got %v", err)
		}
		entries, err := tree.ReadDir("agents")
		if err != nil || len(entries) != 3 || entries[0].Name != "big.md" ||
			entries[2].Name != "real.md" {
			t.Fatalf("agents listing: %v %v", entries, err)
		}
		if _, err := tree.ReadFile("agents/link.md", DefaultMaxBlobBytes); err == nil {
			t.Fatal("symlink content must not be readable")
		}
		_, err = tree.ReadFile("agents/big.md", DefaultMaxBlobBytes)
		if !errors.Is(err, ErrTooLarge) {
			t.Fatalf("blob above the fetch limit: want ErrTooLarge, got %v", err)
		}
		if got := read(t, tree, "agents/real.md"); got != "real\n" {
			t.Fatalf("unexpected content %q", got)
		}
	})
}

// local reports whether the snapshot's object store holds the blob at name.
func local(t *testing.T, snapshot *Snapshot, name string) bool {
	t.Helper()
	tree := snapshot.Tree.(*gitTree)
	e, err := tree.resolve("stat", name)
	if err != nil {
		t.Fatal(err)
	}
	sizes, err := tree.sizes([]string{e.oid})
	if err != nil {
		t.Fatal(err)
	}
	_, ok := sizes[e.oid]
	return ok
}

func TestGitFetchOfALargeSourceFetchesOnlyWhatIsRead(t *testing.T) {
	f := newFixture(t)
	f.write("agents/reviewer.md", "reviewer\n")
	f.write("agents/writer.md", "writer\n")
	noise := func(size int) string {
		data := make([]byte, size)
		if _, err := rand.Read(data); err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	f.write("assets/blob.bin", noise(3<<20))
	sources := map[string]string{}
	for i := range 200 {
		name := filepath.Join("src", strconv.Itoa(i)+".txt")
		sources[name] = noise(4096)
		f.write(name, sources[name])
	}
	f.commit("large")
	fetcher := f.fetcher("")
	fetcher.FullFetchBytes = 256 << 10
	snapshot, err := f.fetch(fetcher, "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = snapshot.Close() }()
	tree, ok := snapshot.Tree.(Partial)
	if !ok || !snapshot.Tree.(*gitTree).sparse {
		t.Fatal("a source above FullFetchBytes must be fetched sparsely")
	}
	if tree.Fetched("agents/reviewer.md") || local(t, snapshot, "agents/reviewer.md") {
		t.Fatal("no file content may be fetched before it is asked for")
	}
	if entry, err := tree.Stat("agents/reviewer.md"); err != nil || entry.Size != UnknownSize {
		t.Fatalf("unfetched file: size %d (%v), want UnknownSize", entry.Size, err)
	}
	if err := tree.Fetch([]string{"agents/reviewer.md", "agents/writer.md", "nope"}); err != nil {
		t.Fatal(err)
	}
	if !tree.Fetched("agents/reviewer.md") || !local(t, snapshot, "agents/writer.md") {
		t.Fatal("fetched files must be held")
	}
	if entry, err := tree.Stat("agents/writer.md"); err != nil || entry.Size != 7 {
		t.Fatalf("fetched file: size %d (%v), want 7", entry.Size, err)
	}
	for _, name := range []string{"assets/blob.bin", "src/7.txt"} {
		if local(t, snapshot, name) {
			t.Fatalf("%s was fetched but never read", name)
		}
	}
	if got := read(t, tree, "src/7.txt"); got != sources["src/7.txt"] {
		t.Fatal("reading an unfetched file fetches it")
	}
	if local(t, snapshot, "src/8.txt") {
		t.Fatal("a single read fetches that file alone")
	}
}

func TestGitFetchBoundsWhatASparseSourceFetches(t *testing.T) {
	f := newFixture(t)
	noise := make([]byte, 256<<10)
	if _, err := rand.Read(noise); err != nil {
		t.Fatal(err)
	}
	f.write("agents/a.md", "a\n")
	f.write("agents/b.md", "b\n")
	f.write("agents/noise.md", string(noise))
	f.commit("budgets")

	fetcher := f.fetcher("")
	fetcher.FullFetchBytes = 1
	fetcher.MaxFetchFiles = 1
	snapshot, err := f.fetch(fetcher, "")
	if err != nil {
		t.Fatal(err)
	}
	err = snapshot.Tree.(Partial).Fetch([]string{"agents/a.md", "agents/b.md"})
	_ = snapshot.Close()
	if !errors.Is(err, ErrFetchBudget) {
		t.Fatalf("more files than MaxFetchFiles: want ErrFetchBudget, got %v", err)
	}

	fetcher.MaxFetchFiles = 0
	fetcher.MaxFetchBytes = 64 << 10
	snapshot, err = f.fetch(fetcher, "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = snapshot.Close() }()
	if _, err := snapshot.Tree.ReadFile("agents/noise.md", DefaultMaxBlobBytes); !errors.Is(
		err, ErrFetchBudget) {
		t.Fatalf("more bytes than MaxFetchBytes: want ErrFetchBudget, got %v", err)
	}

	fetcher.MaxFetchBytes = 0
	fetcher.MaxTreeBytes = 1
	if snapshot, err := f.fetch(fetcher, ""); !errors.Is(err, ErrFetchBudget) {
		if snapshot != nil {
			_ = snapshot.Close()
		}
		t.Fatalf("trees above MaxTreeBytes: want ErrFetchBudget, got %v", err)
	}
}

func TestGitFetchNeverRunsFiltersOrHooks(t *testing.T) {
	f := newFixture(t)
	marker := filepath.Join(f.base, "executed")
	hooks := filepath.Join(f.base, "hooks")
	if err := os.MkdirAll(hooks, 0o750); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\ntouch " + marker + "\n"
	for _, hook := range []string{"reference-transaction", "post-checkout", "post-merge"} {
		if err := os.WriteFile(filepath.Join(hooks, hook), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	f.write(".gitattributes", "* filter=evil diff=evil\n")
	f.write("agents/reviewer.md", "body\n")
	f.commit("evil")
	userConfig := "[core]\n\thooksPath = " + hooks + "\n" +
		"[filter \"evil\"]\n\tsmudge = touch " + marker + "\n\tclean = touch " + marker +
		"\n\tprocess = touch " + marker + "\n\trequired = true\n" +
		"[diff \"evil\"]\n\ttextconv = touch " + marker + "\n"
	fetcher := f.fetcher(userConfig)

	// Control: an ordinary clone with the same user configuration runs them.
	clone := exec.Command("git", "-c", "protocol.file.allow=always", "clone", "--quiet",
		"file://"+f.dir, filepath.Join(f.base, "control"))
	clone.Env = fetcher.Env.Pairs()
	_ = clone.Run()
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("control clone did not execute the configured commands: %v", err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}

	for _, fullFetchBytes := range []int64{0, 1} {
		fetcher.FullFetchBytes = fullFetchBytes
		snapshot, err := f.fetch(fetcher, "")
		if err != nil {
			t.Fatal(err)
		}
		got := read(t, snapshot.Tree, "agents/reviewer.md")
		_ = snapshot.Close()
		if got != "body\n" {
			t.Fatalf("content must be the raw blob, got %q", got)
		}
		if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("fetching executed repository-selected commands (marker: %v)", err)
		}
	}
}

func TestGitFetchRefusesUnsafeTransports(t *testing.T) {
	f := newFixture(t)
	f.write("d/f", "x")
	f.commit("one")
	fetcher := f.fetcher("")
	fetcher.allowFileProtocol = false
	if snapshot, err := f.fetch(fetcher, ""); err == nil {
		_ = snapshot.Close()
		t.Fatal("file transport must be refused")
	}
}

func TestGitFetchErrorsNeverEchoCredentials(t *testing.T) {
	requireGit(t)
	fetcher := &GitFetcher{Env: env.FromMap(map[string]string{
		"HOME":                t.TempDir(),
		"PATH":                env.FromProcess().Get("PATH"),
		"GIT_CONFIG_NOSYSTEM": "1",
		"GIT_CONFIG_GLOBAL":   os.DevNull,
	})}
	loc := Locator{
		Kind:     KindGit,
		Host:     "127.0.0.1",
		RepoPath: "x.git",
		CloneURL: "https://s3cr3t-token@127.0.0.1:1/x.git",
	}
	snapshot, err := fetcher.Fetch(context.Background(), loc)
	if err == nil {
		_ = snapshot.Close()
		t.Fatal("fetch from a closed port must fail")
	}
	if strings.Contains(err.Error(), "s3cr3t-token") {
		t.Fatalf("error leaks credentials: %v", err)
	}
}
