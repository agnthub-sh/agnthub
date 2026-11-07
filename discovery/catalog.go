package discovery

import (
	"errors"
	"path"
	"sort"
	"strings"

	"github.com/agnthub-sh/agnthub/agent"
	"github.com/agnthub-sh/agnthub/harness"
	"github.com/agnthub-sh/agnthub/source"
)

// Catalog layout (ADR 0017): a repository without agnthub.yaml whose other
// layouts offer no agent, but that keeps its agents as Markdown files in
// top-level "division" folders (engineering/, marketing/, …), each with
// YAML frontmatter naming and describing the agent.

const (
	// catalogMinDivision is how many agents a folder needs to make the
	// repository a catalog; catalogMinAgents is how many those folders need
	// together.
	catalogMinDivision = 3
	catalogMinAgents   = 5
	// catalogProbe is how many of a folder's files are read first: a folder
	// whose first files are not agents is not read further.
	catalogProbe = 3
	// catalogMaxFolders bounds the folders probed in one repository.
	catalogMaxFolders = 64
)

// catalogExcludedDirs are folders that hold documentation, code, tooling or
// other harness content rather than division agents (compared lowercased).
var catalogExcludedDirs = map[string]bool{
	"agents": true, "skills": true, "commands": true, "prompts": true, "rules": true,
	"hooks": true, "workflows": true, "plugins": true, "instructions": true,
	"docs": true, "doc": true, "documentation": true, "wiki": true, "guides": true,
	"blog": true, "posts": true, "content": true, "pages": true, "site": true,
	"website": true, "web": true, "www": true, "public": true, "static": true,
	"assets": true, "images": true, "img": true, "media": true,
	"examples": true, "example": true, "samples": true, "sample": true, "demo": true,
	"demos": true, "templates": true, "template": true, "scripts": true, "script": true,
	"tools": true, "bin": true, "cmd": true, "src": true, "lib": true, "pkg": true,
	"internal": true, "app": true, "apps": true, "packages": true, "test": true,
	"tests": true, "testdata": true, "testing-data": true, "fixtures": true,
	"spec": true, "specs": true, "__tests__": true, "e2e": true, "benchmarks": true,
	"node_modules": true, "vendor": true, "third_party": true, "third-party": true,
	"dist": true, "build": true, "out": true, "target": true, "integrations": true,
	"i18n": true, "l10n": true, "locales": true, "translations": true, "changelog": true,
	"changes": true, "rfcs": true, "rfc": true, "adr": true, "adrs": true,
	"archive": true, "archived": true, "deprecated": true, "legacy": true, "old": true,
	"drafts": true, "wip": true, "notes": true, "research-notes": true,
}

// catalogExcludedFiles are repository documents, never agents (lowercased).
var catalogExcludedFiles = map[string]bool{
	"readme.md": true, "contributing.md": true, "changelog.md": true, "license.md": true,
	"code_of_conduct.md": true, "security.md": true, "index.md": true, "summary.md": true,
	"agents.md": true, "claude.md": true, "gemini.md": true, "skill.md": true,
	"agent.md": true, "template.md": true, "_template.md": true, "todo.md": true,
}

// catalogFolder is one top-level folder and the Markdown files that may be
// its agents, in a stable order.
type catalogFolder struct {
	name  string
	files []string
}

// catalogCandidates returns the agents of a catalog layout, or none when the
// repository is not one. While planning, it stops at the first files not
// fetched yet; a later round reads on.
func (s *scanner) catalogCandidates() ([]candidate, error) {
	folders, err := s.catalogFolders()
	if err != nil {
		return nil, err
	}
	var found []candidate
	strong := 0
	pending := false
	for _, folder := range folders {
		agents, verdict, err := s.catalogFolder(folder)
		if err != nil {
			return nil, err
		}
		switch verdict {
		case folderPending:
			pending = true
		case folderDivision:
			if len(agents) >= catalogMinDivision {
				strong += len(agents)
			}
			found = append(found, agents...)
		}
	}
	if pending || strong < catalogMinAgents {
		return nil, nil
	}
	return found, nil
}

// catalogFolders lists the top-level folders that may be divisions and the
// Markdown files directly in them or in their own subfolders.
func (s *scanner) catalogFolders() ([]catalogFolder, error) {
	entries, err := s.tree.ReadDir(".")
	if err != nil {
		return nil, err
	}
	var folders []catalogFolder
	for _, entry := range entries {
		if err := s.countEntry(); err != nil {
			return nil, err
		}
		if entry.Kind != source.EntryDir || !catalogDivisionName(entry.Name) {
			continue
		}
		if len(folders) == catalogMaxFolders {
			break
		}
		files, err := s.catalogFiles(entry.Name, true)
		if err != nil {
			return nil, err
		}
		if len(files) > 0 {
			folders = append(folders, catalogFolder{name: entry.Name, files: files})
		}
	}
	return folders, nil
}

// catalogExcludedWords are words that mark a folder as holding something
// other than agents wherever they appear in its name (skill-guides/,
// claude-commands/, prompt_templates/).
var catalogExcludedWords = map[string]bool{
	"skill": true, "skills": true, "command": true, "commands": true, "prompt": true,
	"prompts": true, "rule": true, "rules": true, "hook": true, "hooks": true,
	"workflow": true, "workflows": true, "doc": true, "docs": true, "example": true,
	"examples": true, "template": true, "templates": true, "test": true, "tests": true,
	"script": true, "scripts": true, "sample": true, "samples": true, "snippet": true,
	"snippets": true, "recipe": true, "recipes": true, "fixture": true, "fixtures": true,
	"plugin": true, "plugins": true, "spec": true, "specs": true,
}

// catalogDivisionName reports whether a folder may be a division: not hidden,
// not underscore-prefixed, not a known non-agent folder or one whose name
// says it holds something else.
func catalogDivisionName(name string) bool {
	lower := strings.ToLower(name)
	if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || catalogExcludedDirs[lower] {
		return false
	}
	for word := range strings.FieldsFuncSeq(lower, func(r rune) bool {
		return r == '-' || r == '_' || r == '.' || r == ' '
	}) {
		if catalogExcludedWords[word] {
			return false
		}
	}
	return true
}

// catalogFiles lists dir's Markdown files that may be agents and, with
// nested, those of its subfolders one level down (game-development/unity/).
func (s *scanner) catalogFiles(dir string, nested bool) ([]string, error) {
	entries, err := s.listDir(dir)
	if err != nil {
		return nil, err
	}
	var files, deeper []string
	for _, entry := range entries {
		full := path.Join(dir, entry.Name)
		switch {
		case entry.Kind == source.EntryFile:
			if strings.HasSuffix(strings.ToLower(entry.Name), ".md") &&
				!catalogExcludedFiles[strings.ToLower(entry.Name)] {
				files = append(files, full)
			}
		case entry.Kind == source.EntryDir && nested && catalogDivisionName(entry.Name):
			sub, err := s.catalogFiles(full, false)
			if err != nil {
				return nil, err
			}
			deeper = append(deeper, sub...)
		}
	}
	sort.Strings(files)
	return append(files, deeper...), nil
}

type folderVerdict int

const (
	folderRejected folderVerdict = iota
	folderPending
	folderDivision
)

// catalogFolder reads a folder's files, first a probe, and decides whether it
// is a division: at least two thirds of its Markdown files are agents. Files
// it cannot read within the budgets are not agents.
func (s *scanner) catalogFolder(folder catalogFolder) ([]candidate, folderVerdict, error) {
	var agents []candidate
	read, pending := 0, false
	check := func(files []string) error {
		for _, name := range files {
			c, deferred, err := s.catalogFile(name, folder.name)
			if err != nil {
				return err
			}
			read++
			if deferred {
				pending = true
				continue
			}
			if c != nil {
				agents = append(agents, *c)
			}
		}
		return nil
	}
	probe := min(catalogProbe, len(folder.files))
	if err := check(folder.files[:probe]); err != nil {
		return nil, folderRejected, err
	}
	if pending {
		return nil, folderPending, nil
	}
	if 3*len(agents) < 2*probe {
		return nil, folderRejected, nil
	}
	if err := check(folder.files[probe:]); err != nil {
		return nil, folderRejected, err
	}
	switch {
	case pending:
		return nil, folderPending, nil
	case 3*len(agents) < 2*read:
		return nil, folderRejected, nil
	}
	return agents, folderDivision, nil
}

// catalogFile reads one Markdown file of a folder and returns it as a
// candidate when its frontmatter names and describes an agent, consists
// mostly of agent fields (a recipe or plugin manifest with id, secrets and
// install steps does not), and instructions follow. The file name is the
// identity; the name is a display name.
func (s *scanner) catalogFile(name, division string) (*candidate, bool, error) {
	entry, err := s.tree.Stat(name)
	if err != nil {
		return nil, false, err
	}
	if entry.Size > s.limits.MaxFileBytes {
		return nil, false, nil
	}
	if err := s.countCandidate(); err != nil {
		return nil, false, err
	}
	raw, err := s.read(name)
	switch {
	case errors.Is(err, errDeferred):
		return nil, true, nil
	case errors.Is(err, ErrBudgetExceeded):
		return nil, false, err
	case err != nil:
		return nil, false, nil
	}
	doc, err := agent.ParseMarkdownLenient(name, raw)
	if err != nil || strings.TrimSpace(doc.String("name")) == "" ||
		strings.TrimSpace(doc.String("description")) == "" ||
		strings.TrimSpace(doc.Body) == "" || !agentShaped(doc.Frontmatter) {
		return nil, false, nil
	}
	return &candidate{
		id: agent.IDFromPath(name), path: name, doc: doc,
		desc: doc.String("description"), division: division,
	}, false, nil
}

// agentShaped reports whether more of a frontmatter's keys are agent fields
// than are not.
func agentShaped(frontmatter map[string]any) bool {
	known := 0
	for key := range frontmatter {
		if harness.KnownAgentKey(key) {
			known++
		}
	}
	return 2*known > len(frontmatter)
}
