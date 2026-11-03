// Package discovery finds logical Agents in a source snapshot by inert, bounded
// parsing of portable layouts, native Harness directories, the Claude Code
// plugins the repository packages, and agnthub.yaml.
//
// A snapshot that fetches file content on demand (source.Partial) is scanned
// in planning rounds first: each round runs the scan itself over what is
// already fetched, collects the files it would read next, and fetches them in
// one batch (manifests, then the plugin manifests they list, then agent
// files). The candidate files are therefore exactly the files the scan reads,
// for every layout, and nothing else of a large repository is downloaded.
package discovery

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/agnthub-sh/agnthub/agent"
	"github.com/agnthub-sh/agnthub/harness"
	"github.com/agnthub-sh/agnthub/source"
)

// ManifestFile is the optional authoritative manifest at the source root.
const ManifestFile = "agnthub.yaml"

// Limits bounds one discovery scan.
type Limits struct {
	MaxFileBytes  int64
	MaxTotalBytes int64
	MaxCandidates int
	MaxEntries    int
}

// DefaultLimits are the accepted per-scan ceilings.
func DefaultLimits() Limits {
	return Limits{
		MaxFileBytes:  1 << 20,
		MaxTotalBytes: 10 << 20,
		MaxCandidates: 1000,
		MaxEntries:    100_000,
	}
}

// ErrBudgetExceeded reports a scan that hit a ceiling; the inventory is incomplete.
var ErrBudgetExceeded = errors.New("discovery budget exceeded")

// Problem is a non-fatal finding scoped to an Agent or path.
type Problem struct {
	// Agent is the affected logical identifier, when known.
	Agent   string
	Path    string
	Message string
}

func (p Problem) String() string {
	switch {
	case p.Agent != "" && p.Path != "":
		return fmt.Sprintf("%s (%s): %s", p.Agent, p.Path, p.Message)
	case p.Agent != "":
		return p.Agent + ": " + p.Message
	default:
		return p.Path + ": " + p.Message
	}
}

// Inventory is the result of discovery.
type Inventory struct {
	// Agents holds unambiguous Agents sorted by identifier.
	Agents []*agent.Definition
	// Problems lists ambiguous identities, invalid files and skipped entries.
	Problems []Problem
	// Manifest reports whether agnthub.yaml defined the inventory.
	Manifest bool
	// Categories lists the repository-level category slugs agnthub.yaml
	// declares, deduplicated in declaration order. Slugs are slug-shaped but
	// may be newer than this module's taxonomy (see package category).
	Categories []string
}

// CategoriesFor returns the categories that describe def: its own declared
// categories when it has any, else the repository-level ones.
func (inv *Inventory) CategoriesFor(def *agent.Definition) []string {
	if len(def.Categories) > 0 {
		return def.Categories
	}
	return inv.Categories
}

// Find returns the Agent with id, or nil.
func (inv *Inventory) Find(id string) *agent.Definition {
	for _, def := range inv.Agents {
		if def.ID == id {
			return def
		}
	}
	return nil
}

// ProblemsFor returns problems recorded for id.
func (inv *Inventory) ProblemsFor(id string) []Problem {
	var problems []Problem
	for _, problem := range inv.Problems {
		if problem.Agent == id {
			problems = append(problems, problem)
		}
	}
	return problems
}

type scanner struct {
	tree     source.Tree
	registry *harness.Registry
	limits   Limits

	entries    int
	bytes      int64
	candidates int
	problems   []Problem
	// claimed are the files plugins provide as Claude Code agents.
	claimed map[string]bool
	// partial and pending are set while planning: reading a file partial has
	// not fetched records it in pending instead (errDeferred).
	partial source.Partial
	pending map[string]bool
}

// errDeferred is a planning read of a file that is not fetched yet. The scan
// goes on without it; only manifests stop what depends on their content.
var errDeferred = errors.New("file not fetched yet")

// maxPlanRounds bounds planning: agnthub.yaml or the marketplace, the plugin
// manifests it lists, their agent files, and a final check.
const maxPlanRounds = 5

// Discover scans tree and returns its Agent inventory.
func Discover(
	tree source.Tree,
	registry *harness.Registry,
	limits Limits,
) (*Inventory, error) {
	if partial, ok := tree.(source.Partial); ok {
		if err := prefetch(partial, registry, limits); err != nil {
			return nil, err
		}
	}
	s := &scanner{tree: tree, registry: registry, limits: limits}
	return s.scan()
}

// prefetch fetches the files a scan of tree reads, round by round, until a
// round needs nothing new. A budget the plan already exceeds is the scan's
// verdict too (the plan counts no more than the scan); other planning
// failures are left to the scan to report.
func prefetch(tree source.Partial, registry *harness.Registry, limits Limits) error {
	requested := map[string]bool{}
	for range maxPlanRounds {
		s := &scanner{
			tree: tree, registry: registry, limits: limits,
			partial: tree, pending: map[string]bool{},
		}
		if _, err := s.scan(); err != nil && !errors.Is(err, errDeferred) {
			if errors.Is(err, ErrBudgetExceeded) {
				return err
			}
			return nil
		}
		names := make([]string, 0, len(s.pending))
		for name := range s.pending {
			if !requested[name] {
				requested[name] = true
				names = append(names, name)
			}
		}
		if len(names) == 0 {
			return nil
		}
		sort.Strings(names)
		if err := tree.Fetch(names); err != nil {
			if errors.Is(err, source.ErrFetchBudget) {
				return fmt.Errorf("%w: %w", ErrBudgetExceeded, err)
			}
			return err
		}
	}
	return nil
}

func (s *scanner) scan() (*Inventory, error) {
	manifest, err := s.readOptional(ManifestFile)
	if err != nil {
		return nil, err
	}
	var inv *Inventory
	switch {
	case manifest != nil && s.pending != nil:
		return nil, s.planManifest(manifest)
	case manifest != nil:
		inv, err = s.fromManifest(manifest)
	default:
		inv, err = s.fromLayouts()
	}
	if err != nil {
		return nil, err
	}
	inv.Problems = append(inv.Problems, s.problems...)
	sort.SliceStable(inv.Problems, func(i, j int) bool {
		if inv.Problems[i].Agent != inv.Problems[j].Agent {
			return inv.Problems[i].Agent < inv.Problems[j].Agent
		}
		return inv.Problems[i].Path < inv.Problems[j].Path
	})
	return inv, nil
}

func (s *scanner) countEntry() error {
	s.entries++
	if s.entries > s.limits.MaxEntries {
		return fmt.Errorf("%w: more than %d tree entries", ErrBudgetExceeded, s.limits.MaxEntries)
	}
	return nil
}

func (s *scanner) countCandidate() error {
	s.candidates++
	if s.candidates > s.limits.MaxCandidates {
		return fmt.Errorf(
			"%w: more than %d candidate definitions",
			ErrBudgetExceeded,
			s.limits.MaxCandidates,
		)
	}
	return nil
}

// read loads a regular file within the per-file and aggregate budgets. While
// planning, a file that is not fetched yet is recorded and errDeferred.
func (s *scanner) read(name string) ([]byte, error) {
	entry, err := s.tree.Stat(name)
	if err != nil {
		return nil, err
	}
	if entry.Kind != source.EntryFile {
		return nil, fmt.Errorf("%s is a %s, not a regular file", name, entry.Kind)
	}
	if entry.Size > s.limits.MaxFileBytes {
		return nil, fmt.Errorf(
			"%w: %s exceeds %d bytes",
			ErrBudgetExceeded,
			name,
			s.limits.MaxFileBytes,
		)
	}
	if entry.Size > 0 && s.bytes+entry.Size > s.limits.MaxTotalBytes {
		return nil, fmt.Errorf(
			"%w: definitions exceed %d bytes in total",
			ErrBudgetExceeded,
			s.limits.MaxTotalBytes,
		)
	}
	if s.pending != nil && !s.partial.Fetched(name) {
		s.pending[name] = true
		if entry.Size > 0 {
			s.bytes += entry.Size
		}
		return nil, errDeferred
	}
	data, err := s.tree.ReadFile(name, s.limits.MaxFileBytes)
	if err != nil {
		if errors.Is(err, source.ErrTooLarge) || errors.Is(err, source.ErrFetchBudget) {
			return nil, fmt.Errorf("%w: %w", ErrBudgetExceeded, err)
		}
		return nil, err
	}
	s.bytes += int64(len(data))
	return data, nil
}

// readOptional reads name when it exists, rejecting non-regular entries.
func (s *scanner) readOptional(name string) ([]byte, error) {
	if _, err := s.tree.Stat(name); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return s.read(name)
}

// listDir lists dir when it exists, recording skipped links/submodules.
func (s *scanner) listDir(dir string) ([]source.Entry, error) {
	entry, err := s.tree.Stat(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if entry.Kind != source.EntryDir {
		s.skip(dir, entry.Kind)
		return nil, nil
	}
	entries, err := s.tree.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for range entries {
		if err := s.countEntry(); err != nil {
			return nil, err
		}
	}
	return entries, nil
}

func (s *scanner) skip(name string, kind source.EntryKind) {
	s.problems = append(s.problems, Problem{
		Path:    name,
		Message: fmt.Sprintf("skipped %s: only regular files and directories are read", kind),
	})
}

type candidate struct {
	id      string
	path    string
	harness agent.HarnessID // empty for portable definitions
	doc     *agent.Document
	raw     []byte
	desc    string
	// plugin names the Claude Code plugin that provides a native file.
	plugin string
}

func (s *scanner) fromLayouts() (*Inventory, error) {
	s.claimed = map[string]bool{}
	plugins, err := s.pluginCandidates()
	if err != nil {
		return nil, err
	}
	candidates, err := s.portableCandidates()
	if err != nil {
		return nil, err
	}
	natives := map[string]bool{}
	for _, adapter := range s.registry.All() {
		found, err := s.nativeCandidates(adapter)
		if err != nil {
			return nil, err
		}
		for _, c := range found {
			natives[c.path] = true
		}
		candidates = append(candidates, found...)
	}
	for _, c := range plugins {
		// A plugin whose agents are a native directory adds nothing to it.
		if !natives[c.path] {
			candidates = append(candidates, c)
		}
	}
	return s.group(candidates), nil
}

func (s *scanner) portableCandidates() ([]candidate, error) {
	var found []candidate
	root, err := s.portableFile("AGENT.md", "")
	if err != nil {
		return nil, err
	}
	if root != nil {
		found = append(found, *root)
	}
	entries, err := s.listDir("agents")
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		full := path.Join("agents", entry.Name)
		switch {
		case entry.Kind == source.EntryFile && strings.HasSuffix(entry.Name, ".md"):
			found, err = s.appendPortable(found, full, strings.TrimSuffix(entry.Name, ".md"))
		case entry.Kind == source.EntryDir:
			nested := path.Join(full, "AGENT.md")
			if _, statErr := s.tree.Stat(nested); statErr == nil {
				found, err = s.appendPortable(found, nested, entry.Name)
			}
		case entry.Kind == source.EntrySymlink || entry.Kind == source.EntrySubmodule:
			s.skip(full, entry.Kind)
		}
		if err != nil {
			return nil, err
		}
	}
	return found, nil
}

func (s *scanner) appendPortable(found []candidate, name, id string) ([]candidate, error) {
	c, err := s.portableFile(name, id)
	if err != nil || c == nil {
		return found, err
	}
	return append(found, *c), nil
}

// portableFile parses a portable definition; impliedID is empty for root AGENT.md.
func (s *scanner) portableFile(name, impliedID string) (*candidate, error) {
	if s.claimed[name] {
		return nil, nil // a plugin's Claude Code agent, not a portable definition
	}
	entry, err := s.tree.Stat(name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if entry.Kind != source.EntryFile {
		s.skip(name, entry.Kind)
		return nil, nil
	}
	if err := s.countCandidate(); err != nil {
		return nil, err
	}
	raw, err := s.read(name)
	if errors.Is(err, errDeferred) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	doc, err := agent.ParseMarkdownLenient(name, raw)
	if err != nil {
		s.problems = append(s.problems, Problem{Agent: impliedID, Path: name, Message: err.Error()})
		return nil, nil
	}
	declared := doc.String("name")
	id := impliedID
	switch {
	case impliedID == "" && declared == "":
		s.problems = append(
			s.problems,
			Problem{Path: name, Message: "root AGENT.md requires a name"},
		)
		return nil, nil
	case impliedID == "":
		id = declared
	case declared != "" && declared != impliedID:
		s.problems = append(s.problems, Problem{
			Agent: impliedID, Path: name,
			Message: fmt.Sprintf("declared name %q does not match its location", declared),
		})
		return nil, nil
	}
	return &candidate{id: id, path: name, doc: doc, desc: doc.String("description")}, nil
}

func (s *scanner) nativeCandidates(adapter harness.Adapter) ([]candidate, error) {
	layout := adapter.Layout()
	var found []candidate
	var walk func(dir string) error
	walk = func(dir string) error {
		entries, err := s.listDir(dir)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			full := path.Join(dir, entry.Name)
			switch entry.Kind {
			case source.EntryDir:
				if layout.Recursive {
					if err := walk(full); err != nil {
						return err
					}
				}
			case source.EntryFile:
				if !hasSuffix(entry.Name, layout.Suffixes) {
					continue
				}
				c, err := s.nativeFile(adapter, full)
				if err != nil {
					return err
				}
				if c != nil {
					found = append(found, *c)
				}
			default:
				s.skip(full, entry.Kind)
			}
		}
		return nil
	}
	if err := walk(layout.Dir); err != nil {
		return nil, err
	}
	return found, nil
}

func hasSuffix(name string, suffixes []string) bool {
	for _, suffix := range suffixes {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

func (s *scanner) nativeFile(adapter harness.Adapter, name string) (*candidate, error) {
	if err := s.countCandidate(); err != nil {
		return nil, err
	}
	raw, err := s.read(name)
	if errors.Is(err, errDeferred) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s.identify(adapter, name, raw), nil
}

// identify names a native file's agent, or records why it names none.
func (s *scanner) identify(adapter harness.Adapter, name string, raw []byte) *candidate {
	identity, err := adapter.Identify(name, raw)
	if err != nil {
		s.problems = append(s.problems, Problem{Path: name, Message: err.Error()})
		if identity.ID == "" {
			return nil
		}
		// The file names its agent: it stays that agent's Variant so the plan
		// refuses it for its Harness rather than installing a conversion instead.
	}
	return &candidate{
		id:      identity.ID,
		path:    name,
		harness: adapter.ID(),
		raw:     raw,
		desc:    identity.Description,
	}
}

// group merges candidates by identity, rejecting ambiguous identities.
func (s *scanner) group(candidates []candidate) *Inventory {
	byID := map[string][]candidate{}
	for _, c := range candidates {
		byID[c.id] = append(byID[c.id], c)
	}
	inv := &Inventory{}
	for id, group := range byID {
		if !agent.ValidID(id) {
			for _, c := range group {
				s.problems = append(s.problems, Problem{
					Agent: id, Path: c.path,
					Message: "invalid agent identifier: use lowercase letters, digits, '-' or '_'",
				})
			}
			continue
		}
		if def := s.merge(id, group); def != nil {
			inv.Agents = append(inv.Agents, def)
		}
	}
	sort.Slice(inv.Agents, func(i, j int) bool { return inv.Agents[i].ID < inv.Agents[j].ID })
	return inv
}

func (s *scanner) merge(id string, group []candidate) *agent.Definition {
	def := &agent.Definition{ID: id, Variants: map[agent.HarnessID]*agent.VariantFile{}}
	seen := map[agent.HarnessID]string{}
	portablePath := ""
	ambiguous := false
	for _, c := range group {
		if c.harness == "" {
			if portablePath != "" {
				s.ambiguous(id, portablePath, c.path)
				ambiguous = true
				continue
			}
			portablePath = c.path
			def.Portable = c.doc
			def.Description = c.desc
			continue
		}
		if previous, dup := seen[c.harness]; dup {
			// Plugins often ship the same file twice (in several plugins, or
			// also in .claude/agents); identical copies are one agent.
			kept := def.Variants[c.harness]
			if (c.plugin != "" || kept.Plugin != "") && bytes.Equal(kept.Raw, c.raw) {
				continue
			}
			s.ambiguous(id, previous, c.path)
			ambiguous = true
			continue
		}
		seen[c.harness] = c.path
		def.Variants[c.harness] = &agent.VariantFile{
			Harness: c.harness, Path: c.path, Raw: c.raw, Plugin: c.plugin,
		}
		if def.Description == "" {
			def.Description = c.desc
		}
	}
	if ambiguous {
		return nil
	}
	return def
}

func (s *scanner) ambiguous(id, first, second string) {
	s.problems = append(s.problems, Problem{
		Agent: id,
		Path:  second,
		Message: fmt.Sprintf(
			"ambiguous identity: also defined by %s; add agnthub.yaml to choose",
			first,
		),
	})
}
