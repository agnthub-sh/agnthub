package discovery

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"github.com/bytedance/sonic"

	"github.com/agnthub-sh/agnthub/agent"
	"github.com/agnthub-sh/agnthub/harness"
	"github.com/agnthub-sh/agnthub/source"
)

// Claude Code plugin manifests (https://code.claude.com/docs/en/plugins-reference
// and https://code.claude.com/docs/en/plugins/marketplace-reference).
const (
	// MarketplaceFile lists a repository's plugins; relative sources resolve
	// from the repository root.
	MarketplaceFile = ".claude-plugin/marketplace.json"
	// PluginManifestFile is a plugin's manifest, under the plugin root. At the
	// repository root it makes the repository itself a plugin.
	PluginManifestFile = ".claude-plugin/plugin.json"
	pluginAgentsDir    = "agents"
)

type marketplaceManifest struct {
	Metadata struct {
		PluginRoot string `json:"pluginRoot"`
	} `json:"metadata"`
	Plugins []marketplaceEntry `json:"plugins"`
}

// marketplaceEntry is one plugin entry. Source is a relative path string or a
// remote source object; Agents a path or an array of paths.
type marketplaceEntry struct {
	Name   string `json:"name"`
	Source any    `json:"source"`
	Strict *bool  `json:"strict"`
	Agents any    `json:"agents"`
}

type pluginManifest struct {
	Name   string `json:"name"`
	Agents any    `json:"agents"`
}

// pluginRef is one plugin of the repository, before its manifest is read.
type pluginRef struct {
	root string
	// name and agents come from the marketplace entry, when there is one.
	name   string
	agents any
	strict bool
	// origin is the file that declared the plugin, for problems.
	origin string
}

// pluginCandidates reads the Claude Code plugins a repository packages: those
// its marketplace lists with a source inside the repository, and the root
// plugin. Their agents are Claude Code native files. Remote plugin sources are
// never fetched; they are reported as problems.
func (s *scanner) pluginCandidates() ([]candidate, error) {
	claude, ok := s.registry.Get(agent.Claude)
	if !ok {
		return nil, nil
	}
	refs, err := s.marketplacePlugins()
	if err != nil {
		return nil, err
	}
	rootManifest, err := s.pluginFileKind(PluginManifestFile)
	if err != nil {
		return nil, err
	}
	if rootManifest {
		refs = append(refs, pluginRef{root: ".", strict: true, origin: PluginManifestFile})
	}
	seen := map[string]bool{}
	var found []candidate
	for _, ref := range refs {
		if seen[ref.root] {
			continue
		}
		seen[ref.root] = true
		plugin, err := s.pluginAgents(claude, ref)
		if err != nil {
			return nil, err
		}
		found = append(found, plugin...)
	}
	return found, nil
}

func (s *scanner) problem(at, format string, args ...any) {
	s.problems = append(s.problems, Problem{Path: at, Message: fmt.Sprintf(format, args...)})
}

// pluginFileKind reports whether name is a regular file, recording a skipped
// link or submodule (also for its .claude-plugin directory).
func (s *scanner) pluginFileKind(name string) (bool, error) {
	for _, at := range []string{path.Dir(name), name} {
		entry, err := s.tree.Stat(at)
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		want := source.EntryFile
		if at != name {
			want = source.EntryDir
		}
		if entry.Kind != want {
			if entry.Kind == source.EntrySymlink || entry.Kind == source.EntrySubmodule {
				s.skip(at, entry.Kind)
			} else {
				s.problem(at, "is a %s, not a %s", entry.Kind, want)
			}
			return false, nil
		}
	}
	return true, nil
}

// readPluginJSON decodes a plugin manifest into out. A missing, skipped or
// invalid manifest returns false; only budget and I/O failures are errors.
func (s *scanner) readPluginJSON(name string, out any) (bool, error) {
	ok, err := s.pluginFileKind(name)
	if err != nil || !ok {
		return false, err
	}
	raw, err := s.read(name)
	if err != nil {
		return false, err
	}
	if err := sonic.Unmarshal(raw, out); err != nil {
		s.problem(name, "invalid JSON: %v", err)
		return false, nil
	}
	return true, nil
}

func (s *scanner) marketplacePlugins() ([]pluginRef, error) {
	var market marketplaceManifest
	ok, err := s.readPluginJSON(MarketplaceFile, &market)
	if errors.Is(err, errDeferred) {
		return nil, nil // planning: its plugins are known once it is fetched
	}
	if err != nil || !ok {
		return nil, err
	}
	pluginRoot := ""
	if root := market.Metadata.PluginRoot; root != "" {
		if root != "." && !strings.HasPrefix(root, "./") {
			root = "./" + root
		}
		resolved, reason := pluginPath(".", root)
		if reason != "" {
			s.problem(MarketplaceFile, "metadata.pluginRoot %q %s", market.Metadata.PluginRoot,
				reason)
		} else {
			pluginRoot = resolved
		}
	}
	refs := make([]pluginRef, 0, len(market.Plugins))
	for i, entry := range market.Plugins {
		if err := s.countEntry(); err != nil {
			return nil, err
		}
		label := fmt.Sprintf("plugin %q", entry.Name)
		if entry.Name == "" {
			label = fmt.Sprintf("plugins[%d]", i)
			s.problem(MarketplaceFile, "%s has no name; Claude Code does not load it", label)
			continue
		}
		root, ok := s.entryRoot(label, entry.Source, pluginRoot)
		if !ok {
			continue
		}
		refs = append(refs, pluginRef{
			root:   root,
			name:   entry.Name,
			agents: entry.Agents,
			strict: entry.Strict == nil || *entry.Strict,
			origin: MarketplaceFile,
		})
	}
	return refs, nil
}

// entryRoot resolves a marketplace entry's source to a directory of this
// repository, or records why the plugin is not read.
func (s *scanner) entryRoot(label string, src any, pluginRoot string) (string, bool) {
	switch src := src.(type) {
	case string:
		if pluginRoot != "" && src != "." && !strings.HasPrefix(src, "./") &&
			!strings.Contains(src, "/") {
			src = "./" + path.Join(pluginRoot, src)
		}
		root, reason := pluginPath(".", src)
		if reason != "" {
			s.problem(MarketplaceFile, "%s: source %q %s", label, src, reason)
			return "", false
		}
		entry, err := s.tree.Stat(root)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			s.problem(MarketplaceFile, "%s: source %q not found", label, src)
			return "", false
		case err != nil:
			s.problem(MarketplaceFile, "%s: source %q: %v", label, src, err)
			return "", false
		case entry.Kind != source.EntryDir:
			s.problem(MarketplaceFile, "%s: source %q is a %s, not a directory", label, src,
				entry.Kind)
			return "", false
		}
		return root, true
	case map[string]any:
		kind, _ := src["source"].(string)
		where := ""
		for _, key := range []string{"repo", "url", "package"} {
			if value, ok := src[key].(string); ok && value != "" {
				where = " " + value
				break
			}
		}
		s.problem(MarketplaceFile,
			"%s: %s source%s is outside this repository and is not fetched; add that "+
				"source to install its agents", label, kind, where)
		return "", false
	default:
		s.problem(MarketplaceFile, "%s: source must be a relative path or a source object", label)
		return "", false
	}
}

// pluginPath resolves a manifest path ("./x", or "." for the root itself)
// against root, returning why it is refused when it does not start with "./",
// is absolute or leaves root.
func pluginPath(root, rel string) (string, string) {
	if rel != "." && !strings.HasPrefix(rel, "./") {
		return "", "must start with ./"
	}
	cleaned := path.Clean(rel)
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		if root == "." {
			return "", "escapes the repository"
		}
		return "", "escapes the plugin directory"
	}
	full, err := source.CleanPath(path.Join(root, cleaned))
	if err != nil {
		return "", "is not a valid path"
	}
	return full, ""
}

// pluginAgents reads one plugin's manifest and agents: the files its
// manifests' agents fields list, else every .md file under agents/.
func (s *scanner) pluginAgents(claude harness.Adapter, ref pluginRef) ([]candidate, error) {
	manifestPath := path.Join(ref.root, PluginManifestFile)
	var manifest pluginManifest
	hasManifest, err := s.readPluginJSON(manifestPath, &manifest)
	if errors.Is(err, errDeferred) {
		return nil, nil // planning: its agents are known once it is fetched
	}
	if err != nil {
		return nil, err
	}
	if !hasManifest && ref.name == "" {
		return nil, nil // the root manifest was invalid; already reported
	}
	name := ref.name
	var paths []string
	declared := false
	if hasManifest {
		if manifest.Name == "" {
			s.problem(manifestPath, "requires a name; Claude Code does not load the plugin")
			return nil, nil
		}
		// Components are namespaced under the manifest's name.
		name = manifest.Name
		if paths, declared, err = agentPaths(manifest.Agents); err != nil {
			s.problem(manifestPath, "agents %v", err)
			return nil, nil
		}
	}
	if ref.agents != nil {
		if hasManifest && !ref.strict {
			s.problem(ref.origin, "plugin %q: both plugin.json and its strict: false "+
				"marketplace entry declare components; Claude Code does not load it", ref.name)
			return nil, nil
		}
		entryPaths, _, err := agentPaths(ref.agents)
		if err != nil {
			s.problem(ref.origin, "plugin %q: agents %v", ref.name, err)
			return nil, nil
		}
		paths, declared = append(paths, entryPaths...), true
	}
	if !declared {
		return s.pluginAgentsDir(claude, name, path.Join(ref.root, pluginAgentsDir))
	}
	var found []candidate
	for _, rel := range paths {
		if err := s.countEntry(); err != nil {
			return nil, err
		}
		full, reason := pluginPath(ref.root, rel)
		if reason == "" && !strings.HasSuffix(full, ".md") {
			reason = "is not a .md file; Claude Code does not accept directories"
		}
		if reason != "" {
			s.problem(manifestPath, "plugin %q: agents path %q %s", name, rel, reason)
			continue
		}
		c, err := s.pluginAgentFile(claude, name, full)
		if err != nil {
			return nil, err
		}
		if c != nil {
			found = append(found, *c)
		}
	}
	return found, nil
}

// agentPaths reads an agents field: one path or an array of paths. declared
// reports whether the field was set, which replaces the default agents/ scan.
func agentPaths(value any) (paths []string, declared bool, err error) {
	switch value := value.(type) {
	case nil:
		return nil, false, nil
	case string:
		return []string{value}, true, nil
	case []any:
		paths = make([]string, 0, len(value))
		for _, item := range value {
			text, ok := item.(string)
			if !ok {
				return nil, true, errors.New("must list paths")
			}
			paths = append(paths, text)
		}
		return paths, true, nil
	default:
		return nil, true, errors.New("must be a path or an array of paths")
	}
}

// pluginAgentsDir reads every .md file under a plugin's agents directory,
// recursively, as Claude Code does.
func (s *scanner) pluginAgentsDir(
	claude harness.Adapter,
	plugin, dir string,
) ([]candidate, error) {
	entries, err := s.listDir(dir)
	if err != nil {
		return nil, err
	}
	var found []candidate
	for _, entry := range entries {
		full := path.Join(dir, entry.Name)
		switch {
		case entry.Kind == source.EntryDir:
			nested, err := s.pluginAgentsDir(claude, plugin, full)
			if err != nil {
				return nil, err
			}
			found = append(found, nested...)
		case entry.Kind == source.EntryFile && strings.HasSuffix(entry.Name, ".md"):
			c, err := s.pluginAgentFile(claude, plugin, full)
			if err != nil {
				return nil, err
			}
			if c != nil {
				found = append(found, *c)
			}
		case entry.Kind != source.EntryFile:
			s.skip(full, entry.Kind)
		}
	}
	return found, nil
}

// pluginAgentFile reads one listed or scanned plugin agent. Files that are
// missing, links or otherwise unreadable are problems, not failures.
func (s *scanner) pluginAgentFile(
	claude harness.Adapter,
	plugin, name string,
) (*candidate, error) {
	entry, err := s.tree.Stat(name)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		s.problem(name, "listed by plugin %q but not found", plugin)
		return nil, nil
	case err != nil:
		return nil, err
	case entry.Kind == source.EntrySymlink || entry.Kind == source.EntrySubmodule:
		s.skip(name, entry.Kind)
		return nil, nil
	case entry.Kind != source.EntryFile:
		s.problem(name, "listed by plugin %q but is a %s, not a file", plugin, entry.Kind)
		return nil, nil
	}
	s.claimed[name] = true
	if err := s.countCandidate(); err != nil {
		return nil, err
	}
	raw, err := s.read(name)
	if errors.Is(err, errDeferred) {
		return nil, nil
	}
	if err != nil {
		if errors.Is(err, ErrBudgetExceeded) {
			return nil, err
		}
		s.problem(name, "%v", err)
		return nil, nil
	}
	c := s.identify(claude, name, raw)
	if c != nil {
		c.plugin = plugin
	}
	return c, nil
}
