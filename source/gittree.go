package source

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/puzpuzpuz/xsync/v4"
)

// gitTree is the inert tree of one fetched commit. Directories are listed on
// demand from the local tree objects, so a commit with millions of files costs
// only the directories that are read. File bytes come from the local object
// store; a sparse tree fetches missing blobs by id within its budgets.
type gitTree struct {
	// ctx is the fetch's context; it also bounds the fetches of later reads.
	ctx      context.Context
	repo     *gitRepo
	revision string
	root     string
	sparse   bool
	limits   gitLimits
	// dirs caches directory listings by tree id, blobs loaded content by blob id.
	dirs  *xsync.Map[string, []gitEntry]
	blobs *xsync.Map[string, gitBlob]
	// local holds the sizes of the blobs the object store holds. It is kept
	// here, never asked of git by id: some git versions answer a question
	// about a blob a partial clone lacks by fetching it, or by failing.
	local *xsync.Map[string, int64]
	// files and bytes count what a sparse tree fetched against its budgets.
	files atomic.Int64
	bytes atomic.Int64
}

type gitEntry struct {
	name string
	kind EntryKind
	oid  string
	// size is the blob size, or UnknownSize when the blob is not local.
	size int64
}

// gitBlob is loaded content; data is nil for a blob above the per-file limit.
type gitBlob struct {
	data []byte
	size int64
}

func newGitTree(
	ctx context.Context,
	repo *gitRepo,
	revision, root string,
	sparse bool,
	limits gitLimits,
) (*gitTree, error) {
	t := &gitTree{
		ctx:      ctx,
		repo:     repo,
		revision: revision,
		root:     root,
		sparse:   sparse,
		limits:   limits,
		dirs:     xsync.NewMap[string, []gitEntry](),
		blobs:    xsync.NewMap[string, gitBlob](),
		local:    xsync.NewMap[string, int64](),
	}
	if sparse {
		return t, nil // a sparse fetch holds no blobs yet
	}
	out, err := repo.run(ctx, "cat-file", "--batch-all-objects", "--unordered",
		"--batch-check=%(objecttype) %(objectname) %(objectsize)")
	if err != nil {
		return nil, fmt.Errorf("read objects: %w", err)
	}
	for line := range strings.SplitSeq(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 || fields[0] != "blob" {
			continue
		}
		size, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil || size < 0 {
			return nil, errors.New("read objects: malformed object size")
		}
		t.local.Store(fields[1], size)
	}
	return t, nil
}

// list reads the entries of one tree object, sorted by name. Entries whose
// names cannot be addressed safely are omitted. Sizes come from the local
// object store (cat-file reports missing blobs instead of fetching them;
// "ls-tree -l" may fail on a blob a partial clone does not hold).
func (t *gitTree) list(oid string) ([]gitEntry, error) {
	if cached, ok := t.dirs.Load(oid); ok {
		return cached, nil
	}
	out, err := t.repo.run(t.ctx, "ls-tree", "-z", oid)
	if err != nil {
		return nil, fmt.Errorf("list tree %s: %w", oid, err)
	}
	var entries []gitEntry
	var blobs []string
	for record := range bytes.SplitSeq(out, []byte{0}) {
		if len(record) == 0 {
			continue
		}
		entry, ok, err := parseTreeRecord(record)
		if err != nil {
			return nil, err
		}
		if ok {
			entries = append(entries, entry)
			if entry.kind == EntryFile {
				blobs = append(blobs, entry.oid)
			}
		}
	}
	if len(blobs) > 0 {
		sizes, err := t.sizes(blobs)
		if err != nil {
			return nil, fmt.Errorf("list tree %s: %w", oid, err)
		}
		for i := range entries {
			if size, ok := sizes[entries[i].oid]; ok && entries[i].kind == EntryFile {
				entries[i].size = size
			}
		}
	}
	slices.SortFunc(entries, func(a, b gitEntry) int { return strings.Compare(a.name, b.name) })
	t.dirs.Store(oid, entries)
	return entries, nil
}

// parseTreeRecord reads one "ls-tree" record: "<mode> <type> <oid>\t<name>".
// A file's size is unknown until the object store reports it.
func parseTreeRecord(record []byte) (gitEntry, bool, error) {
	meta, name, ok := bytes.Cut(record, []byte{'\t'})
	if !ok {
		return gitEntry{}, false, errors.New("malformed tree listing")
	}
	fields := strings.Fields(string(meta))
	if len(fields) != 3 || !commitPattern.MatchString(fields[2]) {
		return gitEntry{}, false, errors.New("malformed tree listing")
	}
	base := string(name)
	if base == "" || base == "." || base == ".." || strings.ContainsAny(base, "/\\") ||
		strings.ContainsFunc(base, isControl) {
		return gitEntry{}, false, nil
	}
	entry := gitEntry{name: base, kind: gitKind(fields[0]), oid: fields[2]}
	if entry.kind == EntryFile {
		entry.size = UnknownSize
	}
	return entry, true, nil
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

// resolve finds name's entry. A path below a link or submodule reports that
// entry's kind, as MemTree does, and is never followed.
func (t *gitTree) resolve(op, name string) (gitEntry, error) {
	cleaned, err := CleanPath(name)
	if err != nil {
		return gitEntry{}, err
	}
	current := gitEntry{name: ".", kind: EntryDir, oid: t.root}
	if cleaned == "." {
		return current, nil
	}
	parts := strings.Split(cleaned, "/")
	for _, part := range parts {
		if current.kind != EntryDir {
			return gitEntry{name: parts[len(parts)-1], kind: current.kind}, nil
		}
		children, err := t.list(current.oid)
		if err != nil {
			return gitEntry{}, err
		}
		i, found := slices.BinarySearchFunc(children, part, func(e gitEntry, target string) int {
			return strings.Compare(e.name, target)
		})
		if !found {
			return gitEntry{}, &fs.PathError{Op: op, Path: name, Err: fs.ErrNotExist}
		}
		current = children[i]
	}
	return current, nil
}

// describe reports e with its best known size: loaded content, the local
// blob, too large for a whole fetch (which omits blobs above the per-file
// limit), or unknown until a sparse tree fetches it.
func (t *gitTree) describe(e gitEntry) Entry {
	entry := Entry{Name: e.name, Kind: e.kind}
	if e.kind != EntryFile {
		return entry
	}
	switch blob, loaded := t.blobs.Load(e.oid); {
	case loaded:
		entry.Size = blob.size
	case e.size >= 0:
		entry.Size = e.size
	case !t.sparse:
		entry.Size = math.MaxInt64
	default:
		entry.Size = UnknownSize
	}
	return entry
}

// Stat implements Tree.
func (t *gitTree) Stat(name string) (Entry, error) {
	e, err := t.resolve("stat", name)
	if err != nil {
		return Entry{}, err
	}
	return t.describe(e), nil
}

// ReadDir implements Tree.
func (t *gitTree) ReadDir(dir string) ([]Entry, error) {
	e, err := t.resolve("readdir", dir)
	if err != nil {
		return nil, err
	}
	if e.kind != EntryDir || e.oid == "" {
		return nil, fmt.Errorf("%s is not a directory", dir)
	}
	children, err := t.list(e.oid)
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(children))
	for _, child := range children {
		entries = append(entries, t.describe(child))
	}
	return entries, nil
}

// Fetched implements Partial.
func (t *gitTree) Fetched(name string) bool {
	e, err := t.resolve("stat", name)
	if err != nil || e.kind != EntryFile {
		return true
	}
	if _, loaded := t.blobs.Load(e.oid); loaded {
		return true
	}
	// A whole fetch omitted only blobs above the per-file limit.
	return !t.sparse && e.size < 0
}

// Fetch implements Partial: it fetches the missing blobs of names in one
// request, then loads them all from the local object store.
func (t *gitTree) Fetch(names []string) error {
	seen := make(map[string]bool, len(names))
	oids := make([]string, 0, len(names))
	for _, name := range names {
		e, err := t.resolve("stat", name)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if e.kind != EntryFile || seen[e.oid] {
			continue
		}
		seen[e.oid] = true
		if _, loaded := t.blobs.Load(e.oid); loaded {
			continue
		}
		if !t.sparse && e.size < 0 {
			t.blobs.Store(e.oid, gitBlob{size: math.MaxInt64})
			continue
		}
		oids = append(oids, e.oid)
	}
	if len(oids) == 0 {
		return nil
	}
	if t.sparse {
		if err := t.fetchMissing(oids); err != nil {
			return err
		}
	}
	return t.load(oids)
}

// fetchMissing fetches the blobs among oids that are not local yet, by id,
// within the tree's file and byte budgets, and records their sizes.
func (t *gitTree) fetchMissing(oids []string) error {
	var missing []string
	for _, oid := range oids {
		if _, ok := t.local.Load(oid); !ok {
			missing = append(missing, oid)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	display := t.repo.loc.Display()
	if t.files.Load()+int64(len(missing)) > int64(t.limits.maxFetchFiles) {
		return fmt.Errorf("%s: %w: more than %d files", display, ErrFetchBudget,
			t.limits.maxFetchFiles)
	}
	t.files.Add(int64(len(missing)))
	remaining := t.limits.maxFetchBytes - t.bytes.Load()
	transferred, completed, err := t.repo.runBounded(t.ctx, remaining,
		strings.NewReader(strings.Join(missing, "\n")+"\n"),
		"fetch", "--quiet", "--no-tags", "--no-write-fetch-head", "--no-recurse-submodules",
		"--filter=blob:none", "--stdin", "origin")
	t.bytes.Add(transferred)
	if err != nil {
		return t.repo.fetchError(err)
	}
	if !completed {
		return fmt.Errorf("%s: %w: files exceed %d bytes", display, ErrFetchBudget,
			t.limits.maxFetchBytes)
	}
	// The fetch delivered them: asking git about them now fetches nothing.
	return t.catFile("--batch-check=%(objectname) %(objecttype) %(objectsize)", missing,
		func(header []string, _ *bufio.Reader) error {
			if len(header) == 3 && header[1] == "blob" {
				size, err := strconv.ParseInt(header[2], 10, 64)
				if err != nil || size < 0 {
					return errors.New("malformed object size")
				}
				t.local.Store(header[0], size)
			}
			return nil
		})
}

// sizes reports the sizes of the local blobs among oids.
func (t *gitTree) sizes(oids []string) (map[string]int64, error) {
	sizes := make(map[string]int64, len(oids))
	for _, oid := range oids {
		if size, ok := t.local.Load(oid); ok {
			sizes[oid] = size
		}
	}
	return sizes, nil
}

// load reads the local blobs among oids: content up to the per-file limit,
// only the size above it.
func (t *gitTree) load(oids []string) error {
	sizes, err := t.sizes(oids)
	if err != nil {
		return err
	}
	small := make([]string, 0, len(sizes))
	for _, oid := range oids {
		size, ok := sizes[oid]
		switch {
		case !ok:
			// Not local: a sparse fetch the server did not answer completely.
		case size > t.limits.maxBlob:
			t.blobs.Store(oid, gitBlob{size: size})
		default:
			small = append(small, oid)
		}
	}
	if len(small) == 0 {
		return nil
	}
	return t.catFile("--batch", small, func(header []string, out *bufio.Reader) error {
		if len(header) != 3 || header[1] != "blob" {
			return nil
		}
		size, err := strconv.ParseInt(header[2], 10, 64)
		if err != nil || size < 0 || size > t.limits.maxBlob {
			return errors.New("malformed object size")
		}
		data := make([]byte, size)
		if _, err := io.ReadFull(out, data); err != nil {
			return err
		}
		if _, err := out.Discard(1); err != nil { // the newline after the content
			return err
		}
		t.blobs.Store(header[0], gitBlob{data: data, size: size})
		return nil
	})
}

// catFile runs one cat-file batch over oids. handle receives each response
// header's fields and, for --batch, the reader positioned at its content.
func (t *gitTree) catFile(
	mode string,
	oids []string,
	handle func(header []string, out *bufio.Reader) error,
) error {
	cmd := t.repo.command(t.ctx, "cat-file", mode)
	cmd.Stdin = strings.NewReader(strings.Join(oids, "\n") + "\n")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr := &boundedBuffer{limit: 16 << 10}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	out := bufio.NewReaderSize(stdout, 64<<10)
	var handleErr error
	for range oids {
		line, err := out.ReadString('\n')
		if err != nil {
			handleErr = fmt.Errorf("read objects: %w", err)
			break
		}
		if handleErr = handle(strings.Fields(line), out); handleErr != nil {
			break
		}
	}
	if handleErr != nil {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	switch {
	case handleErr != nil:
		return handleErr
	case waitErr != nil:
		return fmt.Errorf("read objects: %w", &gitError{diagnostic: sanitize(stderr.String())})
	}
	return nil
}

// ReadFile implements Tree by reading the raw blob; no filters are applied. A
// sparse tree fetches a blob it does not hold yet.
func (t *gitTree) ReadFile(name string, limit int64) ([]byte, error) {
	e, err := t.resolve("open", name)
	if err != nil {
		return nil, err
	}
	if e.kind != EntryFile {
		return nil, fmt.Errorf("%s is a %s, not a regular file", name, e.kind)
	}
	if !t.Fetched(name) {
		if err := t.Fetch([]string{name}); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
	}
	if size := t.describe(e).Size; size > limit {
		return nil, fmt.Errorf("%s: %w (%d bytes)", name, ErrTooLarge, limit)
	}
	blob, loaded := t.blobs.Load(e.oid)
	switch {
	case !loaded:
		return nil, fmt.Errorf("%s: content not available", name)
	case blob.data == nil:
		return nil, fmt.Errorf("%s: %w (%d bytes)", name, ErrTooLarge, t.limits.maxBlob)
	}
	return blob.data, nil
}
