package source

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/agnthub-sh/agnthub/platform/env"
)

// Default Git fetch budgets.
const (
	DefaultMaxBlobBytes int64 = 1 << 20
	DefaultMaxEntries         = 100_000
)

var (
	// ErrGitUnavailable reports that no git executable could be found.
	ErrGitUnavailable = errors.New("git is required for Git sources but was not found")
	// ErrGitAuth reports a fetch refused for lack of usable credentials.
	ErrGitAuth = errors.New(
		"cannot access the repository with your existing Git credentials " +
			"(configure a credential helper or SSH agent, or check the repository exists)",
	)
	// ErrRefNotFound reports a ref the remote does not have.
	ErrRefNotFound = errors.New("ref not found")
	// ErrTreeTooLarge reports a source tree above the entry budget.
	ErrTreeTooLarge = errors.New("source tree exceeds the entry limit")

	commitPattern = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
	shortHex      = regexp.MustCompile(`^[0-9a-fA-F]{4,63}$`)
	urlUserinfo   = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://)[^/@\s]+@`)
)

// GitFetcher fetches Git sources with the system git and the user's existing
// credentials (credential helpers, SSH agent). It resolves the requested ref to
// one immutable commit and exposes that commit's tree without checking it out,
// so no checkout filters, hooks or LFS smudging ever run.
type GitFetcher struct {
	// Env is passed to git so existing credential configuration applies.
	Env env.Env
	// Git is the git executable; empty looks up "git" on PATH.
	Git string
	// TempDir is the parent of temporary repositories; empty uses the system default.
	TempDir string
	// MaxBlobBytes bounds fetched file sizes; larger files are listed but unreadable.
	MaxBlobBytes int64
	// MaxEntries bounds the number of tree entries.
	MaxEntries int

	// allowFileProtocol lets tests fetch from local repositories.
	allowFileProtocol bool
}

// environmentDenylist are repository-location and tracing variables that must not
// redirect or log the isolated fetch.
var environmentDenylist = []string{
	"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY",
	"GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_COMMON_DIR", "GIT_NAMESPACE",
	"GIT_SHALLOW_FILE", "GIT_QUARANTINE_PATH", "GIT_REPLACE_REF_BASE", "GIT_GRAFT_FILE",
	"GIT_CEILING_DIRECTORIES", "GIT_DISCOVERY_ACROSS_FILESYSTEM",
}

// Fetch implements Fetcher.
func (f *GitFetcher) Fetch(ctx context.Context, loc Locator) (*Snapshot, error) {
	if loc.Kind != KindGitHub && loc.Kind != KindGit {
		return nil, fmt.Errorf("%s is not a Git source", loc.Display())
	}
	if loc.CloneURL == "" || strings.HasPrefix(loc.CloneURL, "-") ||
		strings.HasPrefix(loc.Ref, "-") {
		return nil, fmt.Errorf("invalid Git source %s", loc.Display())
	}
	gitPath := f.Git
	if gitPath == "" {
		found, err := exec.LookPath("git")
		if err != nil {
			return nil, ErrGitUnavailable
		}
		gitPath = found
	}
	dir, err := os.MkdirTemp(f.TempDir, "agnthub-git-")
	if err != nil {
		return nil, fmt.Errorf("create temporary repository: %w", err)
	}
	cleanup := func() error { return os.RemoveAll(dir) }
	repo := &gitRepo{git: gitPath, dir: dir, env: f.environment(), config: f.config(), loc: loc}
	revision, err := repo.fetch(ctx, f.maxBlobBytes())
	if err != nil {
		_ = cleanup()
		return nil, err
	}
	tree, err := repo.tree(ctx, revision, f.maxEntries())
	if err != nil {
		_ = cleanup()
		return nil, err
	}
	return NewSnapshot(loc, revision, tree, cleanup), nil
}

func (f *GitFetcher) maxBlobBytes() int64 {
	if f.MaxBlobBytes > 0 {
		return f.MaxBlobBytes
	}
	return DefaultMaxBlobBytes
}

func (f *GitFetcher) maxEntries() int {
	if f.MaxEntries > 0 {
		return f.MaxEntries
	}
	return DefaultMaxEntries
}

func (f *GitFetcher) environment() []string {
	pairs := f.Env.Pairs()
	kept := pairs[:0]
	for _, pair := range pairs {
		key, _, _ := strings.Cut(pair, "=")
		if !denied(key) {
			kept = append(kept, pair)
		}
	}
	return append(kept,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_NO_LAZY_FETCH=1",
		"GIT_LFS_SKIP_SMUDGE=1",
		"GIT_ADVICE=0",
		"LC_ALL=C",
	)
}

func denied(key string) bool {
	if strings.HasPrefix(key, "GIT_TRACE") {
		return true
	}
	for _, name := range environmentDenylist {
		if key == name {
			return true
		}
	}
	return false
}

// config hardens every git invocation: no hooks, no fsmonitor, no submodule
// recursion, no background maintenance, and only https/ssh transports.
func (f *GitFetcher) config() []string {
	file := "never"
	if f.allowFileProtocol {
		file = "always"
	}
	return []string{
		"-c", "core.hooksPath=" + os.DevNull,
		"-c", "core.fsmonitor=false",
		"-c", "protocol.allow=never",
		"-c", "protocol.https.allow=always",
		"-c", "protocol.ssh.allow=always",
		"-c", "protocol.file.allow=" + file,
		"-c", "protocol.ext.allow=never",
		"-c", "fetch.recurseSubmodules=false",
		"-c", "submodule.recurse=false",
		"-c", "gc.auto=0",
		"-c", "maintenance.auto=false",
		"-c", "fetch.writeCommitGraph=false",
	}
}

type gitRepo struct {
	git    string
	dir    string
	env    []string
	config []string
	loc    Locator
}

// run executes git in the temporary repository and returns stdout. Errors carry
// only sanitized git diagnostics.
func (r *gitRepo) run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := r.command(ctx, args...)
	var stdout bytes.Buffer
	stderr := &boundedBuffer{limit: 16 << 10}
	cmd.Stdout = &stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, &gitError{diagnostic: sanitize(stderr.String())}
	}
	return stdout.Bytes(), nil
}

func (r *gitRepo) command(ctx context.Context, args ...string) *exec.Cmd {
	full := make([]string, 0, len(r.config)+1+len(args))
	full = append(full, r.config...)
	full = append(full, "--git-dir="+r.dir)
	full = append(full, args...)
	//nolint:gosec // G204: fixed git subcommands; user values are validated and follow "--"
	cmd := exec.CommandContext(ctx, r.git, full...)
	cmd.Dir = r.dir
	cmd.Env = r.env
	return cmd
}

// fetch fetches exactly the requested ref (or the remote HEAD) shallowly and
// returns the commit it resolves to.
func (r *gitRepo) fetch(ctx context.Context, maxBlobBytes int64) (string, error) {
	display := r.loc.Display()
	if _, err := r.run(ctx, "init", "--quiet", "--bare", "--template="); err != nil {
		return "", fmt.Errorf("%s: initialize temporary repository: %w", display, err)
	}
	if _, err := r.run(ctx, "config", "remote.origin.url", r.loc.CloneURL); err != nil {
		return "", fmt.Errorf("%s: configure remote: %w", display, err)
	}
	ref := r.loc.Ref
	if ref == "" {
		ref = "HEAD"
	}
	_, err := r.run(ctx, "fetch", "--quiet", "--no-tags", "--depth=1", "--no-recurse-submodules",
		"--filter=blob:limit="+strconv.FormatInt(maxBlobBytes, 10),
		"--", "origin", ref)
	if err != nil {
		return "", r.fetchError(err)
	}
	out, err := r.run(ctx, "rev-parse", "--verify", "--quiet", "FETCH_HEAD^{commit}")
	if err != nil {
		return "", fmt.Errorf("%s: %q does not name a commit: %w", display, ref, err)
	}
	revision := strings.TrimSpace(string(out))
	if !commitPattern.MatchString(revision) {
		return "", fmt.Errorf("%s: unexpected revision %q", display, revision)
	}
	return revision, nil
}

func (r *gitRepo) fetchError(err error) error {
	display := r.loc.Display()
	var gitErr *gitError
	if !errors.As(err, &gitErr) {
		return fmt.Errorf("%s: fetch: %w", display, err)
	}
	lower := strings.ToLower(gitErr.diagnostic)
	switch {
	case strings.Contains(lower, "couldn't find remote ref"),
		strings.Contains(lower, "not our ref"),
		strings.Contains(lower, "no such remote ref"):
		if r.loc.Ref != "" && shortHex.MatchString(r.loc.Ref) &&
			!commitPattern.MatchString(r.loc.Ref) {
			return fmt.Errorf(
				"%s: %w: %q (commits must be given as a full SHA)",
				display, ErrRefNotFound, r.loc.Ref,
			)
		}
		return fmt.Errorf("%s: %w: %q", display, ErrRefNotFound, r.loc.Ref)
	case strings.Contains(lower, "could not read username"),
		strings.Contains(lower, "could not read password"),
		strings.Contains(lower, "authentication failed"),
		strings.Contains(lower, "permission denied"),
		strings.Contains(lower, "repository not found"),
		strings.Contains(lower, "could not read from remote repository"),
		strings.Contains(lower, "terminal prompts disabled"):
		return fmt.Errorf("%s: %w: %w", display, ErrGitAuth, err)
	default:
		return fmt.Errorf("%s: fetch: %w", display, err)
	}
}

// tree lists revision's tree without reading blobs; file bytes are read on demand
// from the object store so discovery budgets bound what is loaded.
func (r *gitRepo) tree(ctx context.Context, revision string, maxEntries int) (*gitTree, error) {
	display := r.loc.Display()
	cmd := r.command(ctx, "ls-tree", "-r", "-t", "-l", "-z", "--full-tree", revision)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr := &boundedBuffer{limit: 16 << 10}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("%s: list tree: %w", display, err)
	}
	tree := &gitTree{MemTree: NewMemTree(), repo: r, blobs: map[string]string{}}
	parseErr := tree.parse(bufio.NewReaderSize(stdout, 64<<10), maxEntries)
	if parseErr != nil {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	switch {
	case parseErr != nil:
		return nil, fmt.Errorf("%s: %w", display, parseErr)
	case waitErr != nil:
		return nil, fmt.Errorf(
			"%s: list tree: %w",
			display,
			&gitError{diagnostic: sanitize(stderr.String())},
		)
	}
	return tree, nil
}

// sanitize reduces git stderr to its last meaningful lines with URL credentials
// redacted and control characters removed.
func sanitize(diagnostic string) string {
	diagnostic = urlUserinfo.ReplaceAllString(diagnostic, "${1}***@")
	lines := strings.Split(diagnostic, "\n")
	kept := make([]string, 0, 3)
	for i := len(lines) - 1; i >= 0 && len(kept) < 3; i-- {
		line := strings.TrimSpace(strings.Map(printable, lines[i]))
		if line != "" {
			kept = append(kept, line)
		}
	}
	for i, j := 0, len(kept)-1; i < j; i, j = i+1, j-1 {
		kept[i], kept[j] = kept[j], kept[i]
	}
	return strings.Join(kept, "; ")
}

func printable(r rune) rune {
	if r == '\t' {
		return ' '
	}
	if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
		return -1
	}
	return r
}

type gitError struct {
	diagnostic string
}

func (e *gitError) Error() string {
	if e.diagnostic == "" {
		return "git failed"
	}
	return "git: " + e.diagnostic
}

// boundedBuffer keeps at most limit bytes of the most recent output.
type boundedBuffer struct {
	buf   []byte
	limit int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.buf = append(b.buf, p...)
	if over := len(b.buf) - b.limit; over > 0 {
		b.buf = append(b.buf[:0], b.buf[over:]...)
	}
	return len(p), nil
}

func (b *boundedBuffer) String() string {
	return string(b.buf)
}

// gitTree is the inert tree of one fetched commit.
type gitTree struct {
	*MemTree
	repo  *gitRepo
	blobs map[string]string
}

// parse reads "ls-tree -r -t -l -z" records. Paths that cannot be addressed
// safely are omitted; malformed records abort the fetch.
func (t *gitTree) parse(reader *bufio.Reader, maxEntries int) error {
	entries := 0
	for {
		record, err := reader.ReadSlice(0)
		if errors.Is(err, io.EOF) {
			if len(record) != 0 {
				return errors.New("truncated tree listing")
			}
			return nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			return errors.New("tree entry path is too long")
		}
		if err != nil {
			return err
		}
		entries++
		if entries > maxEntries {
			return fmt.Errorf("%w (%d)", ErrTreeTooLarge, maxEntries)
		}
		if err := t.addRecord(record[:len(record)-1]); err != nil {
			return err
		}
	}
}

func (t *gitTree) addRecord(record []byte) error {
	meta, name, ok := bytes.Cut(record, []byte{'\t'})
	if !ok {
		return errors.New("malformed tree listing")
	}
	fields := strings.Fields(string(meta))
	if len(fields) != 4 {
		return errors.New("malformed tree listing")
	}
	mode, oid, sizeField := fields[0], fields[2], fields[3]
	cleaned, err := CleanPath(string(name))
	if err != nil || cleaned != string(name) || strings.ContainsFunc(cleaned, isControl) {
		return nil
	}
	kind := gitKind(mode)
	size, err := strconv.ParseInt(sizeField, 10, 64)
	if err != nil || size < 0 {
		// Filtered-out blobs report no size: they exceed the fetch limit.
		size = math.MaxInt64
	}
	if kind != EntryFile {
		size = 0
	}
	if err := t.AddEntry(cleaned, kind, size); err != nil {
		return err
	}
	if kind == EntryFile {
		t.blobs[cleaned] = oid
	}
	return nil
}

func isControl(r rune) bool {
	return r < 0x20 || r == 0x7f
}

func gitKind(mode string) EntryKind {
	switch mode {
	case "040000":
		return EntryDir
	case "100644", "100755", "100664":
		return EntryFile
	case "120000":
		return EntrySymlink
	case "160000":
		return EntrySubmodule
	default:
		return EntryOther
	}
}

// ReadFile implements Tree by reading the raw blob; no filters are applied.
func (t *gitTree) ReadFile(name string, limit int64) ([]byte, error) {
	entry, err := t.Stat(name)
	if err != nil {
		return nil, err
	}
	if entry.Kind != EntryFile {
		return nil, fmt.Errorf("%s is a %s, not a regular file", name, entry.Kind)
	}
	if entry.Size > limit {
		return nil, fmt.Errorf("%s: %w (%d bytes)", name, ErrTooLarge, limit)
	}
	cleaned, _ := CleanPath(name)
	oid, ok := t.blobs[cleaned]
	if !ok || !commitPattern.MatchString(oid) {
		return nil, fmt.Errorf("%s: content not available", name)
	}
	data, err := t.repo.run(context.Background(), "cat-file", "blob", oid)
	if err != nil {
		return nil, fmt.Errorf("%s: read: %w", name, err)
	}
	if int64(len(data)) != entry.Size {
		return nil, fmt.Errorf("%s: blob size mismatch", name)
	}
	return data, nil
}
