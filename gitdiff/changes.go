package gitdiff

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/agnthub-sh/agnthub/state"
)

// Options place recorded changes in a patch.
type Options struct {
	// Base is the directory paths are shown relative to; paths outside it stay absolute.
	Base string
	// Exclude lists directories whose changes are left out.
	Exclude []string
}

// FromChanges turns recorded changes into file changes against what is on disk
// now: a write over an absent file creates it, over a different file updates it,
// and a write that changes nothing is left out; a removal deletes every file and
// link it reaches, without following links.
func FromChanges(changes []state.Change, opts Options) ([]File, error) {
	var files []File
	for _, change := range changes {
		if excluded(change.Path, opts.Exclude) {
			continue
		}
		var err error
		if change.Removed {
			files, err = appendRemoval(files, change, opts.Base)
		} else {
			files, err = appendWrite(files, change, opts.Base)
		}
		if err != nil {
			return nil, err
		}
	}
	slices.SortStableFunc(files, func(a, b File) int { return strings.Compare(a.Path, b.Path) })
	return files, nil
}

func excluded(path string, dirs []string) bool {
	for _, dir := range dirs {
		if within(dir, path) {
			return true
		}
	}
	return false
}

// within reports whether path is dir or below it.
func within(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) &&
		!filepath.IsAbs(rel)
}

// display is path relative to base when it is below base, else absolute.
func display(base, path string) string {
	if base != "" && within(base, path) {
		if rel, err := filepath.Rel(base, path); err == nil && rel != "." {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.ToSlash(path)
}

// current is what is at a path on disk now; mode 0 means absent.
type current struct {
	mode    uint32
	content []byte
	dir     bool
}

func read(path string) (current, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return current{}, nil
	}
	if err != nil {
		return current{}, err
	}
	switch {
	case info.Mode()&fs.ModeSymlink != 0:
		target, err := os.Readlink(path)
		return current{mode: ModeSymlink, content: []byte(target)}, err
	case info.IsDir():
		return current{dir: true}, nil
	case info.Mode().IsRegular():
		content, err := os.ReadFile(path)
		return current{mode: fileMode(info.Mode()), content: content}, err
	default:
		return current{}, fmt.Errorf("%s is not a regular file", path)
	}
}

func fileMode(m fs.FileMode) uint32 {
	if m.Perm()&0o111 != 0 {
		return ModeExecutable
	}
	return ModeRegular
}

func appendWrite(files []File, change state.Change, base string) ([]File, error) {
	now, err := read(change.Path)
	if err != nil {
		return nil, err
	}
	if now.dir {
		return nil, fmt.Errorf("%s is a directory", change.Path)
	}
	path := display(base, change.Path)
	mode := fileMode(change.Perm)
	switch now.mode {
	case 0:
		return append(files, File{
			Path: path, Action: Create, New: change.Content, NewMode: mode,
		}), nil
	case ModeSymlink:
		// The write replaces the link itself, as git shows a type change.
		return append(files,
			File{Path: path, Action: Delete, Old: now.content, OldMode: ModeSymlink},
			File{Path: path, Action: Create, New: change.Content, NewMode: mode},
		), nil
	}
	if now.mode == mode && bytes.Equal(now.content, change.Content) {
		return files, nil
	}
	return append(files, File{
		Path: path, Action: Update,
		Old: now.content, OldMode: now.mode, New: change.Content, NewMode: mode,
	}), nil
}

func appendRemoval(files []File, change state.Change, base string) ([]File, error) {
	now, err := read(change.Path)
	if err != nil {
		return nil, err
	}
	if !now.dir {
		if now.mode == 0 {
			return files, nil
		}
		return append(files, File{
			Path: display(base, change.Path), Action: Delete, Old: now.content, OldMode: now.mode,
		}), nil
	}
	if !change.Tree {
		// Only an empty directory is removed by itself, and git has no directories.
		return files, nil
	}
	err = filepath.WalkDir(change.Path, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		got, err := read(path)
		if err != nil || got.mode == 0 {
			return err
		}
		files = append(files, File{
			Path: display(base, path), Action: Delete, Old: got.content, OldMode: got.mode,
		})
		return nil
	})
	return files, err
}
