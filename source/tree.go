package source

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
)

// EntryKind classifies a tree entry without following links.
type EntryKind int

// Entry kinds.
const (
	EntryFile EntryKind = iota
	EntryDir
	EntrySymlink
	EntrySubmodule
	EntryOther
)

func (k EntryKind) String() string {
	switch k {
	case EntryFile:
		return "file"
	case EntryDir:
		return "directory"
	case EntrySymlink:
		return "symlink"
	case EntrySubmodule:
		return "submodule"
	default:
		return "special file"
	}
}

// Entry describes one tree entry.
type Entry struct {
	Name string
	Kind EntryKind
	Size int64
}

// ErrTooLarge reports a file above the requested read limit.
var ErrTooLarge = errors.New("file exceeds size limit")

// Tree is a read-only, inert view of a source snapshot. Paths are slash-separated
// and relative to the source root ("." is the root). Implementations never follow
// symbolic links, evaluate Git filters or execute anything.
type Tree interface {
	// Stat describes name without following links.
	Stat(name string) (Entry, error)
	// ReadDir lists a directory sorted by name.
	ReadDir(dir string) ([]Entry, error)
	// ReadFile returns a regular file's bytes, failing with ErrTooLarge above limit.
	ReadFile(name string, limit int64) ([]byte, error)
}

// CleanPath validates a repository-relative path and returns its clean form.
func CleanPath(name string) (string, error) {
	if name == "" || strings.Contains(name, "\\") || strings.ContainsRune(name, 0) {
		return "", fmt.Errorf("invalid repository path %q", name)
	}
	if path.IsAbs(name) || (len(name) > 1 && name[1] == ':') {
		return "", fmt.Errorf("repository path %q must be relative", name)
	}
	cleaned := path.Clean(name)
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("repository path %q escapes the source root", name)
	}
	if !fs.ValidPath(cleaned) {
		return "", fmt.Errorf("invalid repository path %q", name)
	}
	return cleaned, nil
}

// LocalTree reads a directory through an os.Root, which refuses to escape it.
type LocalTree struct {
	root *os.Root
}

// OpenLocal opens dir as a tree.
func OpenLocal(dir string) (*LocalTree, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("open local source: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("local source %s is not a directory", dir)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("open local source: %w", err)
	}
	return &LocalTree{root: root}, nil
}

// Close releases the root handle.
func (t *LocalTree) Close() error {
	return t.root.Close()
}

func kindOf(mode fs.FileMode) EntryKind {
	switch {
	case mode.IsRegular():
		return EntryFile
	case mode.IsDir():
		return EntryDir
	case mode&fs.ModeSymlink != 0:
		return EntrySymlink
	default:
		return EntryOther
	}
}

// Stat implements Tree. A path that traverses a symlink or other non-directory
// parent is reported with that parent's kind and never followed.
func (t *LocalTree) Stat(name string) (Entry, error) {
	cleaned, err := CleanPath(name)
	if err != nil {
		return Entry{}, err
	}
	if cleaned != "." {
		parts := strings.Split(cleaned, "/")
		for i := 1; i < len(parts); i++ {
			parent := strings.Join(parts[:i], "/")
			info, err := t.root.Lstat(parent)
			if err != nil {
				return Entry{}, err
			}
			if kind := kindOf(info.Mode()); kind != EntryDir {
				return Entry{Name: path.Base(cleaned), Kind: kind}, nil
			}
		}
	}
	info, err := t.root.Lstat(cleaned)
	if err != nil {
		return Entry{}, err
	}
	return Entry{Name: path.Base(cleaned), Kind: kindOf(info.Mode()), Size: info.Size()}, nil
}

// ReadDir implements Tree.
func (t *LocalTree) ReadDir(dir string) ([]Entry, error) {
	cleaned, err := CleanPath(dir)
	if err != nil {
		return nil, err
	}
	handle, err := t.root.Open(cleaned)
	if err != nil {
		return nil, err
	}
	defer func() { _ = handle.Close() }()
	dirEntries, err := handle.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(dirEntries))
	for _, dirEntry := range dirEntries {
		entry := Entry{Name: dirEntry.Name(), Kind: kindOf(dirEntry.Type())}
		if entry.Kind == EntryFile {
			if info, infoErr := dirEntry.Info(); infoErr == nil {
				entry.Size = info.Size()
			}
		}
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return entries, nil
}

// ReadFile implements Tree.
func (t *LocalTree) ReadFile(name string, limit int64) ([]byte, error) {
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
	handle, err := t.root.Open(cleaned)
	if err != nil {
		return nil, err
	}
	defer func() { _ = handle.Close() }()
	data, err := io.ReadAll(io.LimitReader(handle, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s: %w (%d bytes)", name, ErrTooLarge, limit)
	}
	return data, nil
}

// MemTree is an in-memory tree built from fetched blobs.
type MemTree struct {
	entries map[string]Entry
	content map[string][]byte
	dirs    map[string][]string
}

// NewMemTree returns an empty in-memory tree.
func NewMemTree() *MemTree {
	return &MemTree{
		entries: map[string]Entry{".": {Name: ".", Kind: EntryDir}},
		content: map[string][]byte{},
		dirs:    map[string][]string{".": nil},
	}
}

// Add records an entry; content is used only for files.
func (t *MemTree) Add(name string, kind EntryKind, content []byte) error {
	cleaned, err := t.add(name, kind, int64(len(content)))
	if err != nil || kind != EntryFile {
		return err
	}
	t.content[cleaned] = content
	return nil
}

// AddEntry records an entry of the given size without content, for trees that
// load file bytes on demand.
func (t *MemTree) AddEntry(name string, kind EntryKind, size int64) error {
	_, err := t.add(name, kind, size)
	return err
}

func (t *MemTree) add(name string, kind EntryKind, size int64) (string, error) {
	cleaned, err := CleanPath(name)
	if err != nil {
		return "", err
	}
	if cleaned == "." {
		return "", errors.New("cannot add the root")
	}
	if existing, exists := t.entries[cleaned]; exists {
		if existing.Kind == EntryDir && kind == EntryDir {
			return cleaned, nil
		}
		return "", fmt.Errorf("duplicate tree entry %s", cleaned)
	}
	if kind == EntryDir {
		t.ensureDir(cleaned)
		return cleaned, nil
	}
	t.ensureDir(path.Dir(cleaned))
	t.entries[cleaned] = Entry{Name: path.Base(cleaned), Kind: kind, Size: size}
	parent := path.Dir(cleaned)
	t.dirs[parent] = append(t.dirs[parent], cleaned)
	return cleaned, nil
}

func (t *MemTree) ensureDir(dir string) {
	if _, ok := t.entries[dir]; ok {
		return
	}
	t.ensureDir(path.Dir(dir))
	t.entries[dir] = Entry{Name: path.Base(dir), Kind: EntryDir}
	parent := path.Dir(dir)
	t.dirs[parent] = append(t.dirs[parent], dir)
	if _, ok := t.dirs[dir]; !ok {
		t.dirs[dir] = nil
	}
}

// Stat implements Tree.
func (t *MemTree) Stat(name string) (Entry, error) {
	cleaned, err := CleanPath(name)
	if err != nil {
		return Entry{}, err
	}
	for parent := path.Dir(cleaned); parent != "."; parent = path.Dir(parent) {
		if entry, ok := t.entries[parent]; ok && entry.Kind != EntryDir {
			return Entry{Name: path.Base(cleaned), Kind: entry.Kind}, nil
		}
	}
	entry, ok := t.entries[cleaned]
	if !ok {
		return Entry{}, &fs.PathError{Op: "stat", Path: name, Err: fs.ErrNotExist}
	}
	return entry, nil
}

// ReadDir implements Tree.
func (t *MemTree) ReadDir(dir string) ([]Entry, error) {
	cleaned, err := CleanPath(dir)
	if err != nil {
		return nil, err
	}
	entry, ok := t.entries[cleaned]
	if !ok {
		return nil, &fs.PathError{Op: "readdir", Path: dir, Err: fs.ErrNotExist}
	}
	if entry.Kind != EntryDir {
		return nil, fmt.Errorf("%s is not a directory", dir)
	}
	children := t.dirs[cleaned]
	entries := make([]Entry, 0, len(children))
	for _, child := range children {
		entries = append(entries, t.entries[child])
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return entries, nil
}

// ReadFile implements Tree.
func (t *MemTree) ReadFile(name string, limit int64) ([]byte, error) {
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
	data, ok := t.content[cleaned]
	if !ok {
		return nil, fmt.Errorf("%s: content not available", name)
	}
	return data, nil
}
