package discovery

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/agnthub-sh/agnthub/source"
)

// IgnoreFile is the optional root file listing the paths agnthub ignores, one
// per line. A file that lists no paths ignores the whole source.
const IgnoreFile = ".agnthub-ignore"

// Ignore is what a source asks agnthub to ignore: IgnoreFile and the ignore key
// of agnthub.yaml together.
type Ignore struct {
	// All ignores the whole source.
	All bool
	// Paths are ignored files and directories, clean and relative to the root,
	// sorted and deduplicated; a directory covers everything under it. Empty
	// when All is set.
	Paths []string
}

// Covers reports whether the slash-separated, root-relative name is ignored.
func (ig Ignore) Covers(name string) bool {
	if ig.All {
		return true
	}
	for _, p := range ig.Paths {
		if name == p || (strings.HasPrefix(name, p) && name[len(p)] == '/') {
			return true
		}
	}
	return false
}

// Set reports whether the source declares any ignore rule.
func (ig Ignore) Set() bool {
	return ig.All || len(ig.Paths) > 0
}

// IgnoreError reports an invalid IgnoreFile; the whole source is rejected.
type IgnoreError struct {
	Reason string
}

func (e *IgnoreError) Error() string {
	return IgnoreFile + ": " + e.Reason
}

// Option adjusts one discovery scan.
type Option func(*options)

type options struct {
	includeIgnored bool
}

// IncludeIgnored scans the paths a source asks agnthub to ignore as if it set
// no rules. Inventory.Ignore still reports the rules.
func IncludeIgnored() Option {
	return func(o *options) { o.includeIgnored = true }
}

// manifestIgnore is agnthub.yaml's ignore key: "all", or a list of paths.
type manifestIgnore struct {
	set   bool
	all   bool
	paths []string
}

func (m *manifestIgnore) UnmarshalYAML(node *yaml.Node) error {
	m.set = true
	switch {
	case node.Kind == yaml.ScalarNode && node.Tag == "!!str" && node.Value == "all":
		m.all = true
		return nil
	case node.Kind == yaml.SequenceNode:
		var paths []string
		if err := node.Decode(&paths); err != nil {
			return errors.New(`ignore must be "all" or a list of paths`)
		}
		if len(paths) == 0 {
			return errors.New(
				"ignore lists no paths; use ignore: all to ignore the whole repository",
			)
		}
		m.paths = paths
		return nil
	default:
		return errors.New(`ignore must be "all" or a list of paths`)
	}
}

// ignorePath validates one ignored path and returns its clean form; root
// reports a path naming the source root itself, which ignores everything.
func ignorePath(raw string) (clean string, root bool, err error) {
	name := strings.TrimPrefix(raw, "/")
	if name == "" {
		return "", true, nil
	}
	if strings.ContainsAny(name, "*?[") {
		return "", false, fmt.Errorf("%q: patterns are not supported; list paths", raw)
	}
	clean, err = source.CleanPath(name)
	if err != nil {
		return "", false, err
	}
	return clean, clean == ".", nil
}

// parseIgnoreFile reads IgnoreFile: one path per line; blank lines and lines
// starting with # are skipped. A file that lists no paths ignores everything.
func parseIgnoreFile(raw []byte) (Ignore, error) {
	var ig Ignore
	listed := false
	for i, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		listed = true
		clean, root, err := ignorePath(line)
		if err != nil {
			return Ignore{}, &IgnoreError{Reason: fmt.Sprintf("line %d: %v", i+1, err)}
		}
		if root {
			ig.All = true
			continue
		}
		ig.Paths = append(ig.Paths, clean)
	}
	if !listed {
		ig.All = true
	}
	return ig.normalized(), nil
}

// manifestRules validates agnthub.yaml's ignore key.
func manifestRules(m manifestIgnore) (Ignore, error) {
	ig := Ignore{All: m.all}
	for _, raw := range m.paths {
		clean, root, err := ignorePath(raw)
		if err != nil {
			return Ignore{}, manifestErr("ignore: %v", err)
		}
		if root {
			ig.All = true
			continue
		}
		ig.Paths = append(ig.Paths, clean)
	}
	return ig.normalized(), nil
}

// merge joins two rule sets: ignoring is the union of both.
func (ig Ignore) merge(other Ignore) Ignore {
	return Ignore{
		All:   ig.All || other.All,
		Paths: slices.Concat(ig.Paths, other.Paths),
	}.normalized()
}

func (ig Ignore) normalized() Ignore {
	if ig.All {
		return Ignore{All: true}
	}
	slices.Sort(ig.Paths)
	ig.Paths = slices.Compact(ig.Paths)
	return ig
}

// ignoredTree hides ignored paths: they do not exist and are never listed.
type ignoredTree struct {
	source.Tree
	ignore Ignore
}

func (t ignoredTree) Stat(name string) (source.Entry, error) {
	if t.ignore.Covers(name) {
		return source.Entry{}, &fs.PathError{Op: "stat", Path: name, Err: fs.ErrNotExist}
	}
	return t.Tree.Stat(name)
}

func (t ignoredTree) ReadDir(dir string) ([]source.Entry, error) {
	entries, err := t.Tree.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var kept []source.Entry
	for i, entry := range entries {
		if !t.ignore.Covers(path.Join(dir, entry.Name)) {
			if kept != nil {
				kept = append(kept, entry)
			}
			continue
		}
		if kept == nil {
			// The tree may share its listing: copy before dropping anything.
			kept = make([]source.Entry, i, len(entries)-1)
			copy(kept, entries[:i])
		}
	}
	if kept == nil {
		return entries, nil
	}
	return kept, nil
}

func (t ignoredTree) ReadFile(name string, limit int64) ([]byte, error) {
	if t.ignore.Covers(name) {
		return nil, &fs.PathError{Op: "read", Path: name, Err: fs.ErrNotExist}
	}
	return t.Tree.ReadFile(name, limit)
}
