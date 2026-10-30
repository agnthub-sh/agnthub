package skills

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// ErrModified reports an installed Skill whose content no longer matches the
// digest agnthub recorded, or that holds content agnthub cannot account for.
var ErrModified = errors.New("skill content changed since agnthub installed it")

// InstalledState reports whether the Skill at dir is absent, unchanged from
// digest, or changed. Links are followed, as Harnesses do when loading Skills.
func InstalledState(dir, digest string) (exists bool, err error) {
	if _, err := os.Lstat(dir); errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	content, err := readInstalledSkill(dir)
	if err != nil {
		return true, fmt.Errorf("%w: %w", ErrModified, err)
	}
	if content.Digest != digest {
		return true, ErrModified
	}
	if hasUntracked(dir) {
		return true, fmt.Errorf("%w: it contains a .git directory", ErrModified)
	}
	return true, nil
}

// RemoveInstalled deletes the Skill at dir only when its content still matches
// digest. A symbolic link is removed by itself; the directory it points to is
// left in place and returned as kept.
func RemoveInstalled(dir, digest string) (kept string, err error) {
	exists, err := InstalledState(dir, digest)
	if err != nil || !exists {
		return "", err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return "", err
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		target, err := filepath.EvalSymlinks(dir)
		if err != nil {
			return "", err
		}
		return target, os.Remove(dir)
	}
	return "", os.RemoveAll(dir)
}

// hasUntracked reports a .git directory inside dir: digests ignore it, and a
// repository there is the user's work rather than installed content.
func hasUntracked(dir string) bool {
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return false
	}
	found := false
	_ = filepath.WalkDir(root, func(_ string, entry fs.DirEntry, err error) error {
		if err == nil && entry.IsDir() && entry.Name() == ".git" {
			found = true
			return filepath.SkipAll
		}
		return err
	})
	return found
}
