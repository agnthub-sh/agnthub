package source

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/agnthub-sh/agnthub/platform/env"
)

// Default Git fetch budgets.
const (
	// DefaultMaxBlobBytes bounds one file; larger files are listed but unreadable.
	DefaultMaxBlobBytes int64 = 1 << 20
	// DefaultFullFetchBytes is the most a fetch of the whole tree may transfer
	// before agnthub uses its sparse fetch instead. Below it one round trip is
	// as fast as listing first; above it the sparse fetch transfers far less.
	DefaultFullFetchBytes int64 = 16 << 20
	// DefaultMaxTreeBytes bounds a sparse fetch of the commit's trees (names only).
	DefaultMaxTreeBytes int64 = 256 << 20
	// DefaultMaxFetchFiles bounds the files a sparse snapshot fetches.
	DefaultMaxFetchFiles = 10_000
	// DefaultMaxFetchBytes bounds the bytes a sparse snapshot transfers for them.
	DefaultMaxFetchBytes int64 = 64 << 20
)

// watchInterval is how often a bounded fetch measures what it has received.
const watchInterval = 20 * time.Millisecond

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

	commitPattern = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
	shortHex      = regexp.MustCompile(`^[0-9a-fA-F]{4,63}$`)
	urlUserinfo   = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://)[^/@\s]+@`)
)

// GitFetcher fetches Git sources with the system git and the user's existing
// credentials (credential helpers, SSH agent). It resolves the requested ref to
// one immutable commit and exposes that commit's tree without checking it out,
// so no checkout filters, hooks or LFS smudging ever run.
//
// A small source is fetched whole in one round trip (every file up to
// MaxBlobBytes). A source whose fetch would transfer more than FullFetchBytes
// is fetched sparsely: the commit's trees only, after which the snapshot is a
// Partial tree that fetches just the files its reader asks for (discovery
// asks for its candidate files in a few batches), within MaxFetchFiles and
// MaxFetchBytes.
type GitFetcher struct {
	// Env is passed to git so existing credential configuration applies.
	Env env.Env
	// Git is the git executable; empty looks up "git" on PATH.
	Git string
	// TempDir is the parent of temporary repositories; empty uses the system default.
	TempDir string
	// MaxBlobBytes bounds file sizes; larger files are listed but unreadable.
	MaxBlobBytes int64
	// FullFetchBytes is the most a whole-tree fetch may transfer before the
	// source is fetched sparsely instead.
	FullFetchBytes int64
	// MaxTreeBytes bounds the transfer of a sparse fetch's trees.
	MaxTreeBytes int64
	// MaxFetchFiles bounds the files a sparse snapshot fetches.
	MaxFetchFiles int
	// MaxFetchBytes bounds the bytes a sparse snapshot transfers for them.
	MaxFetchBytes int64

	// allowFileProtocol lets tests fetch from local repositories.
	allowFileProtocol bool
}

// gitLimits are a fetch's effective budgets.
type gitLimits struct {
	maxBlob, fullFetch, maxTree, maxFetchBytes int64
	maxFetchFiles                              int
}

func positive[T int | int64](value, fallback T) T {
	if value > 0 {
		return value
	}
	return fallback
}

func (f *GitFetcher) limits() gitLimits {
	return gitLimits{
		maxBlob:       positive(f.MaxBlobBytes, DefaultMaxBlobBytes),
		fullFetch:     positive(f.FullFetchBytes, DefaultFullFetchBytes),
		maxTree:       positive(f.MaxTreeBytes, DefaultMaxTreeBytes),
		maxFetchBytes: positive(f.MaxFetchBytes, DefaultMaxFetchBytes),
		maxFetchFiles: positive(f.MaxFetchFiles, DefaultMaxFetchFiles),
	}
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
	base := gitRepo{git: gitPath, dir: dir, env: f.environment(), config: f.config(), loc: loc}
	tree, err := fetchCommit(ctx, base, f.limits())
	if err != nil {
		_ = cleanup()
		return nil, err
	}
	return NewSnapshot(loc, tree.revision, tree, cleanup), nil
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
// recursion, no background maintenance, and only https/ssh transports. Fetched
// objects always stay packed, so a bounded fetch can measure what it received,
// and fetching by object id never negotiates history.
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
		"-c", "fetch.unpackLimit=1",
		"-c", "fetch.negotiationAlgorithm=noop",
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
	// A killed fetch's helpers (remote transport, index-pack) may keep its
	// output pipes open for a moment; do not wait on them indefinitely.
	cmd.WaitDelay = 5 * time.Second
	return cmd
}

// runBounded runs a fetch that may store at most budget bytes of objects. It
// reports the bytes stored and whether the fetch completed; a fetch that
// exceeds the budget is killed, with its helpers, and reported incomplete.
func (r *gitRepo) runBounded(
	ctx context.Context,
	budget int64,
	stdin io.Reader,
	args ...string,
) (int64, bool, error) {
	before, err := objectBytes(r.dir)
	if err != nil {
		return 0, false, err
	}
	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	cmd := r.command(runCtx, args...)
	isolate(cmd)
	cmd.Stdin = stdin
	stderr := &boundedBuffer{limit: 16 << 10}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return 0, false, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	ticker := time.NewTicker(watchInterval)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			if err != nil {
				if ctxErr := ctx.Err(); ctxErr != nil {
					return 0, false, ctxErr
				}
				return 0, false, &gitError{diagnostic: sanitize(stderr.String())}
			}
			after, err := objectBytes(r.dir)
			if err != nil {
				return 0, false, err
			}
			// A fetch faster than one measurement is held to the budget too.
			return after - before, after-before <= budget, nil
		case <-ticker.C:
			now, err := objectBytes(r.dir)
			if err == nil && now-before > budget {
				stop()
				<-done
				return now - before, false, nil
			}
		}
	}
}

// objectBytes is the size of the repository's object store, including packs
// that are still being received.
func objectBytes(dir string) (int64, error) {
	var total int64
	err := filepath.WalkDir(
		filepath.Join(dir, "objects"),
		func(_ string, d fs.DirEntry, err error) error {
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					return nil // a temporary pack renamed or removed while walking
				}
				return err
			}
			if !d.Type().IsRegular() {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					return nil
				}
				return err
			}
			total += info.Size()
			return nil
		},
	)
	return total, err
}

// init creates the empty temporary repository with its one remote.
func (r *gitRepo) init(ctx context.Context) error {
	display := r.loc.Display()
	if _, err := r.run(ctx, "init", "--quiet", "--bare", "--template="); err != nil {
		return fmt.Errorf("%s: initialize temporary repository: %w", display, err)
	}
	if _, err := r.run(ctx, "config", "remote.origin.url", r.loc.CloneURL); err != nil {
		return fmt.Errorf("%s: configure remote: %w", display, err)
	}
	return nil
}

// attempt is the outcome of one of fetchCommit's two fetches.
type attempt struct {
	completed bool
	err       error
}

// minGrace is the least time a whole fetch is still given once the sparse
// fetch is done; otherwise it is given as long as the sparse fetch took. A
// small or medium source's whole fetch takes up to about twice as long as its
// trees alone, and saves the sparse fetch's later round trips.
const minGrace = 250 * time.Millisecond

// fetchCommit fetches the requested ref (or the remote HEAD) shallowly, twice
// at once: whole (every file up to the per-file limit, within limits.fullFetch)
// and sparse (the commit's trees only). A whole fetch that completes before or
// soon after the sparse one is used, so a small source costs one round trip
// and later reads none. Otherwise the whole fetch is abandoned - large
// repositories spend seconds compressing it before sending anything - and the
// sparse tree fetches the files its reader asks for.
func fetchCommit(ctx context.Context, base gitRepo, limits gitLimits) (*gitTree, error) {
	ref := base.loc.Ref
	if ref == "" {
		ref = "HEAD"
	}
	whole, sparse := base, base
	whole.dir, sparse.dir = filepath.Join(base.dir, "whole"), filepath.Join(base.dir, "sparse")
	wholeCtx, cancelWhole := context.WithCancel(ctx)
	defer cancelWhole()
	sparseCtx, cancelSparse := context.WithCancel(ctx)
	defer cancelSparse()
	wholeDone, sparseDone := make(chan attempt, 1), make(chan attempt, 1)
	start := time.Now()
	go func() {
		filter := "blob:limit=" + strconv.FormatInt(limits.maxBlob, 10)
		wholeDone <- whole.attempt(wholeCtx, ref, filter, limits.fullFetch)
	}()
	go func() { sparseDone <- sparse.attempt(sparseCtx, ref, "blob:none", limits.maxTree) }()

	var wholeResult, sparseResult *attempt
	var grace <-chan time.Time
	for {
		select {
		case result := <-wholeDone:
			wholeResult = &result
		case result := <-sparseDone:
			sparseResult = &result
			if result.err == nil && result.completed {
				grace = time.After(max(time.Since(start), minGrace))
			}
		case <-grace:
			cancelWhole()
			<-wholeDone
			_ = os.RemoveAll(whole.dir)
			return sparse.tree(ctx, ref, true, limits)
		}
		switch {
		case wholeResult != nil && wholeResult.err == nil && wholeResult.completed:
			cancelSparse()
			if sparseResult == nil {
				<-sparseDone
			}
			_ = os.RemoveAll(sparse.dir)
			return whole.tree(ctx, ref, false, limits)
		case wholeResult != nil && sparseResult != nil:
			if sparseResult.err == nil && sparseResult.completed {
				_ = os.RemoveAll(whole.dir)
				return sparse.tree(ctx, ref, true, limits)
			}
			if wholeResult.err != nil {
				return nil, wholeResult.err
			}
			if sparseResult.err != nil {
				return nil, sparseResult.err
			}
			return nil, fmt.Errorf("%s: %w: its trees exceed %d bytes",
				base.loc.Display(), ErrFetchBudget, limits.maxTree)
		}
	}
}

// attempt initializes r and fetches ref with filter, within budget bytes.
func (r *gitRepo) attempt(ctx context.Context, ref, filter string, budget int64) attempt {
	if err := os.Mkdir(r.dir, 0o700); err != nil {
		return attempt{err: fmt.Errorf("create temporary repository: %w", err)}
	}
	if err := r.init(ctx); err != nil {
		return attempt{err: err}
	}
	completed, err := r.fetchRef(ctx, ref, filter, budget)
	return attempt{completed: completed, err: err}
}

// tree resolves the fetched commit of r and returns its tree.
func (r *gitRepo) tree(
	ctx context.Context,
	ref string,
	sparse bool,
	limits gitLimits,
) (*gitTree, error) {
	display := r.loc.Display()
	out, err := r.run(ctx, "rev-parse", "--verify", "--quiet", "FETCH_HEAD^{commit}")
	if err != nil {
		return nil, fmt.Errorf("%s: %q does not name a commit: %w", display, ref, err)
	}
	revision := strings.TrimSpace(string(out))
	if !commitPattern.MatchString(revision) {
		return nil, fmt.Errorf("%s: unexpected revision %q", display, revision)
	}
	out, err = r.run(ctx, "rev-parse", "--verify", "--quiet", revision+"^{tree}")
	if err != nil {
		return nil, fmt.Errorf("%s: read the tree of %s: %w", display, revision, err)
	}
	root := strings.TrimSpace(string(out))
	if !commitPattern.MatchString(root) {
		return nil, fmt.Errorf("%s: unexpected tree %q", display, root)
	}
	return newGitTree(ctx, r, revision, root, sparse, limits)
}

// fetchRef fetches ref with filter, reporting false when the fetch was
// abandoned above budget bytes.
func (r *gitRepo) fetchRef(ctx context.Context, ref, filter string, budget int64) (bool, error) {
	_, completed, err := r.runBounded(ctx, budget, nil,
		"fetch", "--quiet", "--no-tags", "--depth=1", "--no-recurse-submodules",
		"--filter="+filter, "--", "origin", ref)
	if err != nil {
		return false, r.fetchError(err)
	}
	return completed, nil
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
