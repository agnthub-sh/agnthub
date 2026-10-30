package source

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
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

func TestGitTreeExposesLinksAndSubmodulesWithoutContent(t *testing.T) {
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

	snapshot, err := f.fetch(f.fetcher(""), "")
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
	} {
		entry, err := tree.Stat(name)
		if err != nil || entry.Kind != want {
			t.Fatalf("%s: got %v %v, want %v", name, entry.Kind, err, want)
		}
	}
	if _, err := tree.ReadFile("agents/link.md", DefaultMaxBlobBytes); err == nil {
		t.Fatal("symlink content must not be readable")
	}
	if _, err := tree.ReadFile("agents/big.md", DefaultMaxBlobBytes); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("blob above the fetch limit: want ErrTooLarge, got %v", err)
	}
	if got := read(t, tree, "agents/real.md"); got != "real\n" {
		t.Fatalf("unexpected content %q", got)
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

	snapshot, err := f.fetch(fetcher, "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = snapshot.Close() }()
	if got := read(t, snapshot.Tree, "agents/reviewer.md"); got != "body\n" {
		t.Fatalf("content must be the raw blob, got %q", got)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("fetching executed repository-selected commands (marker: %v)", err)
	}
}

func TestGitFetchRefusesUnsafeTransportsAndOversizedTrees(t *testing.T) {
	f := newFixture(t)
	for i := range 5 {
		f.write(filepath.Join("d", strings.Repeat("f", i+1)), "x")
	}
	f.commit("many")
	fetcher := f.fetcher("")
	fetcher.allowFileProtocol = false
	if snapshot, err := f.fetch(fetcher, ""); err == nil {
		_ = snapshot.Close()
		t.Fatal("file transport must be refused")
	}
	fetcher = f.fetcher("")
	fetcher.MaxEntries = 3
	if snapshot, err := f.fetch(fetcher, ""); !errors.Is(err, ErrTreeTooLarge) {
		if snapshot != nil {
			_ = snapshot.Close()
		}
		t.Fatalf("want ErrTreeTooLarge, got %v", err)
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
