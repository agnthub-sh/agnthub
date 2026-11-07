package install

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/agnthub-sh/agnthub/harness"
)

// maxNameScanBytes bounds each existing agent file read for its name.
const maxNameScanBytes = 1 << 20

// flagSameNames refuses items a harness would load under a name another agent
// already has in the same directory: a harness that invokes agents by name
// loads only one of them. Generated files are named by their identifier, so
// this concerns authored files with display names (ADR 0017), which install
// byte-identical.
func (p *Planner) flagSameNames(plan *Plan) {
	planned := map[string]string{} // directory, harness and name → agent
	paths := map[string]bool{}
	for _, item := range plan.Items {
		if item.Path != "" {
			paths[item.Path] = true
		}
	}
	existing := map[string]map[string]string{} // directory|harness → name → file
	for i := range plan.Items {
		item := &plan.Items[i]
		if item.Path == "" || item.Action.Blocking() {
			continue
		}
		adapter, ok := p.Registry.Get(item.Harness)
		if !ok {
			continue
		}
		name := harness.InvocationName(adapter, item.Path, item.Artifact.Content)
		if name == "" {
			continue
		}
		dirKey := item.Destination.Dir + "\x00" + string(item.Harness)
		if other, dup := planned[dirKey+"\x00"+name]; dup && other != item.Agent {
			item.Action = ActionConflict
			item.Reason = fmt.Sprintf("%s would load it under the name %q, as planned agent %s",
				adapter.Name(), name, other)
			continue
		}
		planned[dirKey+"\x00"+name] = item.Agent
		names, scanned := existing[dirKey]
		if !scanned {
			names = installedNames(adapter, item.Destination.Dir, paths)
			existing[dirKey] = names
		}
		if file, dup := names[name]; dup {
			item.Action = ActionConflict
			item.Reason = fmt.Sprintf("%s already has an agent named %q (%s) and would load "+
				"only one of them", adapter.Name(), name, file)
		}
	}
}

// installedNames maps the names of the agent files in dir, other than the
// planned paths, to their files.
func installedNames(
	adapter harness.Adapter,
	dir string,
	planned map[string]bool,
) map[string]string {
	layout := adapter.Layout()
	names := map[string]string{}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return names // no directory yet: no agent to clash with
	}
	defer func() { _ = root.Close() }() // read-only: a close error loses nothing
	_ = fs.WalkDir(root.FS(), ".", func(rel string, entry fs.DirEntry, err error) error {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		switch {
		case err != nil:
			return nil //nolint:nilerr // an unreadable entry names no agent
		case entry.IsDir() && rel != "." && !layout.Recursive:
			return fs.SkipDir
		case !entry.Type().IsRegular() || planned[path] || !hasAnySuffix(rel, layout.Suffixes):
			return nil
		}
		info, err := entry.Info()
		if err != nil || info.Size() > maxNameScanBytes {
			return nil //nolint:nilerr // an unreadable entry names no agent
		}
		raw, err := root.ReadFile(rel)
		if err != nil {
			return nil //nolint:nilerr // an unreadable entry names no agent
		}
		if name := harness.InvocationName(adapter, path, raw); name != "" {
			if _, seen := names[name]; !seen {
				names[name] = path
			}
		}
		return nil
	})
	return names
}

func hasAnySuffix(name string, suffixes []string) bool {
	for _, suffix := range suffixes {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}
