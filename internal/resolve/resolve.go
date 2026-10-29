// Package resolve computes the transitive Agent Dependency closure of a selection
// across pinned sources, before anything is planned or written.
//
// Dependencies are presence requirements: resolving one installs a companion Agent,
// it never promises that the dependent delegates to it at runtime.
package resolve

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/heynemann/agnthub/internal/agent"
	"github.com/heynemann/agnthub/internal/discovery"
	"github.com/heynemann/agnthub/internal/harness"
	"github.com/heynemann/agnthub/internal/source"
)

// Resolution failures. Every one stops the plan before any write.
var (
	// ErrCycle reports Agents that require each other.
	ErrCycle = errors.New("agent dependency cycle")
	// ErrMissing reports a required Agent that is absent, ambiguous or invalid in its source.
	ErrMissing = errors.New("required agent cannot be resolved")
	// ErrConflict reports one Dependency identity required at different revisions.
	ErrConflict = errors.New("conflicting revisions of one agent dependency")
	// ErrSource reports an unusable external Dependency source.
	ErrSource = errors.New("invalid agent dependency source")
	// ErrSkillsUnsupported reports Skill Dependencies, which cannot be installed yet.
	ErrSkillsUnsupported = errors.New("skill dependencies are not supported yet")
)

// Opener resolves locators into snapshots; source.Opener satisfies it.
type Opener interface {
	Open(ctx context.Context, loc source.Locator) (*source.Snapshot, error)
}

// Resolver opens external Dependency sources and walks the Dependency graph.
type Resolver struct {
	Opener   Opener
	Registry *harness.Registry
	Limits   discovery.Limits
}

// Root is the already-open snapshot the user selected Agents from.
type Root struct {
	Snapshot  *source.Snapshot
	Inventory *discovery.Inventory
}

// Member is one Agent of the closure and the Harnesses it installs for.
type Member struct {
	Snapshot   *source.Snapshot
	Definition *agent.Definition
	// Direct reports a directly selected Agent; it stays direct even when also required.
	Direct bool
	// Harnesses lists where the Member installs, in selection order.
	Harnesses []agent.HarnessID
	// RequiredBy names the dependents that required a dependency Member, sorted.
	RequiredBy []string
}

// Closure is the resolved selection plus every required companion Agent.
type Closure struct {
	// Members lists direct Agents in selection order, then companions in discovery order.
	Members []Member
	opened  []*source.Snapshot
}

// Close releases the external snapshots opened during resolution; the root
// snapshot remains owned by the caller.
func (c *Closure) Close() error {
	if c == nil {
		return nil
	}
	return closeAll(c.opened)
}

func closeAll(snapshots []*source.Snapshot) error {
	var errs []error
	for _, snapshot := range snapshots {
		errs = append(errs, snapshot.Close())
	}
	return errors.Join(errs...)
}

type entry struct {
	snapshot  *source.Snapshot
	inventory *discovery.Inventory
	root      bool
}

type node struct {
	entry  *entry
	def    *agent.Definition
	label  string
	direct bool
	// requiredBy is the first dependent that reached the node, for conflict reports.
	requiredBy string
	// needs records, per Harness, the dependents that required the node.
	needs map[agent.HarnessID]map[string]bool
}

type edgeKey struct {
	from   *node
	source string
	agent  string
}

type visitState uint8

const (
	unvisited visitState = iota
	visiting
	visited
)

type run struct {
	resolver  *Resolver
	ctx       context.Context
	snapshots map[string]*entry
	nodes     map[string]*node
	order     []*node
	edges     map[edgeKey]*node
	opened    []*source.Snapshot
}

// Resolve returns the transitive closure of selected for harnesses. Each Harness
// follows the Agent's common Dependencies plus those of its Variant for that Harness;
// Variants add requirements and never remove common ones. Same-snapshot references
// resolve in the snapshot that declared them; each external (source, ref) is opened
// once and pinned to one revision.
func (r *Resolver) Resolve(
	ctx context.Context,
	root Root,
	selected []*agent.Definition,
	harnesses []agent.HarnessID,
) (*Closure, error) {
	rootEntry := &entry{snapshot: root.Snapshot, inventory: root.Inventory, root: true}
	s := &run{
		resolver:  r,
		ctx:       ctx,
		snapshots: map[string]*entry{snapshotKey(root.Snapshot.Locator): rootEntry},
		nodes:     map[string]*node{},
		edges:     map[edgeKey]*node{},
	}
	direct := make([]*node, 0, len(selected))
	for _, def := range selected {
		n, err := s.node(rootEntry, def, "the direct selection")
		if err != nil {
			return nil, errors.Join(err, closeAll(s.opened))
		}
		n.direct = true
		direct = append(direct, n)
	}
	for _, harnessID := range harnesses {
		states := map[*node]visitState{}
		for _, n := range direct {
			if err := s.visit(n, harnessID, states, nil); err != nil {
				return nil, errors.Join(err, closeAll(s.opened))
			}
		}
	}
	return &Closure{Members: s.members(harnesses), opened: s.opened}, nil
}

func (s *run) visit(
	n *node,
	harnessID agent.HarnessID,
	states map[*node]visitState,
	path []*node,
) error {
	switch states[n] {
	case visited:
		return nil
	case visiting:
		return cycleError(path, n)
	case unvisited:
	}
	states[n] = visiting
	path = append(path, n)
	common := n.def.Dependencies
	var added agent.Dependencies
	if variant := n.def.Variant(harnessID); variant != nil {
		added = variant.Dependencies
	}
	for _, skills := range [2][]agent.SkillDependency{common.Skills, added.Skills} {
		if len(skills) > 0 {
			return skillError(n, harnessID, skills[0])
		}
	}
	for _, deps := range [2][]agent.AgentDependency{common.Agents, added.Agents} {
		for _, dep := range deps {
			child, err := s.dependency(n, dep)
			if err != nil {
				return err
			}
			if !child.direct {
				dependents := child.needs[harnessID]
				if dependents == nil {
					dependents = map[string]bool{}
					child.needs[harnessID] = dependents
				}
				dependents[n.label] = true
			}
			if err := s.visit(child, harnessID, states, path); err != nil {
				return err
			}
		}
	}
	states[n] = visited
	return nil
}

// dependency resolves one declared Dependency of from to a closure node.
func (s *run) dependency(from *node, dep agent.AgentDependency) (*node, error) {
	key := edgeKey{from: from, source: dep.Source, agent: dep.Agent}
	if n, ok := s.edges[key]; ok {
		return n, nil
	}
	target := from.entry
	if dep.Source != "" {
		loc, err := source.ParseRemote(dep.Source)
		if err != nil {
			return nil, fmt.Errorf(
				"%w: %s requires %s from %w",
				ErrSource,
				from.label,
				dep.Agent,
				err,
			)
		}
		target, err = s.open(loc)
		if err != nil {
			return nil, fmt.Errorf(
				"%s requires %s from %s: %w",
				from.label, dep.Agent, loc.Display(), err,
			)
		}
	}
	def := target.inventory.Find(dep.Agent)
	if def == nil {
		return nil, missingError(from, dep.Agent, target)
	}
	n, err := s.node(target, def, from.label)
	if err != nil {
		return nil, err
	}
	s.edges[key] = n
	return n, nil
}

// open returns the snapshot for loc, opening each (source, ref) at most once.
func (s *run) open(loc source.Locator) (*entry, error) {
	key := snapshotKey(loc)
	if e, ok := s.snapshots[key]; ok {
		return e, nil
	}
	snapshot, err := s.resolver.Opener.Open(s.ctx, loc)
	if err != nil {
		return nil, err
	}
	s.opened = append(s.opened, snapshot)
	inventory, err := discovery.Discover(snapshot.Tree, s.resolver.Registry, s.resolver.Limits)
	if err != nil {
		return nil, err
	}
	e := &entry{snapshot: snapshot, inventory: inventory}
	s.snapshots[key] = e
	return e, nil
}

// node returns the closure node for def, rejecting a second revision of one identity.
func (s *run) node(e *entry, def *agent.Definition, requiredBy string) (*node, error) {
	identity := e.snapshot.Locator.Key() + "::" + def.ID
	if existing, ok := s.nodes[identity]; ok {
		if existing.entry == e || existing.entry.snapshot.Revision == e.snapshot.Revision {
			return existing, nil
		}
		return nil, fmt.Errorf(
			"%w: %s from %s is required at %s by %s and at %s by %s",
			ErrConflict, def.ID, e.snapshot.Locator.Key(),
			revisionLabel(existing.entry.snapshot), existing.requiredBy,
			revisionLabel(e.snapshot), requiredBy,
		)
	}
	n := &node{
		entry:      e,
		def:        def,
		label:      nodeLabel(e, def),
		requiredBy: requiredBy,
		needs:      map[agent.HarnessID]map[string]bool{},
	}
	s.nodes[identity] = n
	s.order = append(s.order, n)
	return n, nil
}

// members groups each companion's Harnesses by identical dependents.
func (s *run) members(harnesses []agent.HarnessID) []Member {
	members := make([]Member, 0, len(s.order))
	for _, n := range s.order {
		if n.direct {
			members = append(members, Member{
				Snapshot: n.entry.snapshot, Definition: n.def, Direct: true, Harnesses: harnesses,
			})
			continue
		}
		groups := map[string]int{}
		for _, harnessID := range harnesses {
			dependents := n.needs[harnessID]
			if len(dependents) == 0 {
				continue
			}
			requiredBy := sortedKeys(dependents)
			key := strings.Join(requiredBy, "\x00")
			if i, ok := groups[key]; ok {
				members[i].Harnesses = append(members[i].Harnesses, harnessID)
				continue
			}
			groups[key] = len(members)
			members = append(members, Member{
				Snapshot:   n.entry.snapshot,
				Definition: n.def,
				Harnesses:  []agent.HarnessID{harnessID},
				RequiredBy: requiredBy,
			})
		}
	}
	return members
}

func snapshotKey(loc source.Locator) string {
	return loc.Key() + "#" + loc.Ref
}

func nodeLabel(e *entry, def *agent.Definition) string {
	if e.root {
		return def.ID
	}
	return def.ID + " (" + e.snapshot.Locator.Display() + ")"
}

func revisionLabel(snapshot *source.Snapshot) string {
	label := snapshot.Locator.Display()
	if snapshot.Revision != "" {
		label += " @ " + snapshot.Revision
	}
	return label
}

func sortedKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func cycleError(path []*node, back *node) error {
	start := 0
	for i, n := range path {
		if n == back {
			start = i
			break
		}
	}
	labels := make([]string, 0, len(path)-start+1)
	for _, n := range path[start:] {
		labels = append(labels, n.label)
	}
	labels = append(labels, back.label)
	return fmt.Errorf("%w: %s", ErrCycle, strings.Join(labels, " → "))
}

func missingError(from *node, id string, target *entry) error {
	where := "the same source"
	if target != from.entry {
		where = target.snapshot.Locator.Display()
	}
	if problems := target.inventory.ProblemsFor(id); len(problems) > 0 {
		lines := make([]string, 0, len(problems))
		for _, problem := range problems {
			lines = append(lines, problem.String())
		}
		return fmt.Errorf(
			"%w: %s requires %s from %s:\n  %s",
			ErrMissing, from.label, id, where, strings.Join(lines, "\n  "),
		)
	}
	available := make([]string, 0, len(target.inventory.Agents))
	for _, def := range target.inventory.Agents {
		available = append(available, def.ID)
	}
	if len(available) == 0 {
		available = append(available, "none")
	}
	return fmt.Errorf(
		"%w: %s requires %s, which is not in %s (available: %s)",
		ErrMissing, from.label, id, where, strings.Join(available, ", "),
	)
}

func skillError(n *node, harnessID agent.HarnessID, skill agent.SkillDependency) error {
	return fmt.Errorf(
		"%w: %s requires skill %s from %s for %s",
		ErrSkillsUnsupported, n.label, skill.Skill, skill.Source, harnessID,
	)
}
