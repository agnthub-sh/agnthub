// Package state persists the portable project lock and user-local ownership
// records that prove which installed artifacts agnthub owns.
package state

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/bytedance/sonic"
)

// Digest returns the canonical content digest used in locks and records.
func Digest(content []byte) string {
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// FileDigest returns the digest of a regular file, or ok=false when it is absent.
func FileDigest(path string) (digest string, ok bool, err error) {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	if !info.Mode().IsRegular() {
		return "", true, fmt.Errorf("%s is not a regular file", path)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return "", true, err
	}
	return Digest(content), true, nil
}

var jsonAPI = sonic.Config{SortMapKeys: true, EscapeHTML: false}.Froze()

// readJSON decodes path into target, leaving target untouched when absent.
func readJSON(path string, target any) (bool, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if err := jsonAPI.Unmarshal(content, target); err != nil {
		return true, fmt.Errorf("parse %s: %w", path, err)
	}
	return true, nil
}

// writeJSON atomically writes value as indented JSON with a trailing newline.
func writeJSON(path string, value any, perm fs.FileMode) error {
	content, err := jsonAPI.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	return WriteFileAtomic(path, append(content, '\n'), perm)
}

// WriteFileAtomic writes content through a temporary file and rename, creating
// parent directories as needed.
func WriteFileAtomic(path string, content []byte, perm fs.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	cleanup := func() { _ = os.Remove(tempName) }
	if _, err := temp.Write(content); err != nil {
		_ = temp.Close()
		cleanup()
		return err
	}
	if err := temp.Chmod(perm); err != nil {
		_ = temp.Close()
		cleanup()
		return err
	}
	if err := temp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tempName, path); err != nil {
		cleanup()
		return err
	}
	return nil
}
