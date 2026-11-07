package state

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Files makes agnthub's file changes. Disk changes the filesystem; a Recorder
// keeps the changes in memory, so a preview runs the same code as the real change
// and shows exactly the bytes it would write.
type Files interface {
	// WriteFile replaces path with content, creating parent directories.
	WriteFile(path string, content []byte, perm fs.FileMode) error
	// Remove deletes a file, a symbolic link or an empty directory.
	Remove(path string) error
	// RemoveAll deletes path and everything below it.
	RemoveAll(path string) error
}

// Disk changes the filesystem: atomic writes and plain removals.
var Disk Files = disk{}

type disk struct{}

func (disk) WriteFile(path string, content []byte, perm fs.FileMode) error {
	return WriteFileAtomic(path, content, perm)
}

func (disk) Remove(path string) error { return os.Remove(path) }

func (disk) RemoveAll(path string) error { return os.RemoveAll(path) }

// Change is one recorded change to a path: its final content, or its removal.
type Change struct {
	// Path is the absolute path the change applies to.
	Path string
	// Content and Perm are what a write leaves at Path; nil for a removal.
	Content []byte
	Perm    fs.FileMode
	// Removed reports a removal; Tree reports that everything below Path goes too.
	Removed bool
	Tree    bool
}

// Recorder records changes instead of making them. Only the last change to a
// path counts, as on disk. The zero value is ready to use.
type Recorder struct {
	changes map[string]Change
}

func (r *Recorder) put(change Change) {
	if r.changes == nil {
		r.changes = map[string]Change{}
	}
	r.changes[change.Path] = change
}

// WriteFile records content as path's new content.
func (r *Recorder) WriteFile(path string, content []byte, perm fs.FileMode) error {
	r.put(Change{Path: filepath.Clean(path), Content: slices.Clone(content), Perm: perm})
	return nil
}

// Remove records path's removal.
func (r *Recorder) Remove(path string) error {
	r.put(Change{Path: filepath.Clean(path), Removed: true})
	return nil
}

// RemoveAll records the removal of path and everything below it.
func (r *Recorder) RemoveAll(path string) error {
	r.put(Change{Path: filepath.Clean(path), Removed: true, Tree: true})
	return nil
}

// Changes returns the recorded changes ordered by path.
func (r *Recorder) Changes() []Change {
	out := make([]Change, 0, len(r.changes))
	for _, change := range r.changes {
		out = append(out, change)
	}
	slices.SortFunc(out, func(a, b Change) int { return strings.Compare(a.Path, b.Path) })
	return out
}
