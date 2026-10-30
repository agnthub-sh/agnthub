package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/heynemann/agnthub/agent"
	"github.com/heynemann/agnthub/discovery"
	"github.com/heynemann/agnthub/harness"
	"github.com/heynemann/agnthub/install"
	"github.com/heynemann/agnthub/platform/env"
	"github.com/heynemann/agnthub/resolve"
	"github.com/heynemann/agnthub/skills"
	"github.com/heynemann/agnthub/source"
	"github.com/heynemann/agnthub/state"
)

// ErrRevisionUnavailable reports locked source bytes that can no longer be fetched.
var ErrRevisionUnavailable = errors.New("locked revision is unavailable")

// lifecycle holds what check, update and restore share: the scope's lock and
// ownership records and one cache of opened sources.
type lifecycle struct {
	app      *App
	scope    harness.Scope
	lockPath string
	planner  *install.Planner
	cache    *sourceCache
}

func (a *App) newLifecycle(global, frozen bool) (*lifecycle, error) {
	scope := harness.ScopeProject
	if global {
		scope = harness.ScopeGlobal
	}
	planner, err := a.planner(scope)
	if err != nil {
		return nil, err
	}
	cache := &sourceCache{
		opener: a.Opener, registry: a.Registry, entries: map[string]*cachedSource{},
	}
	if frozen {
		cache.frozen = true
		if cache.pins, err = a.skillPins(planner.Lock); err != nil {
			return nil, err
		}
	}
	return &lifecycle{
		app: a, scope: scope, lockPath: planner.LockPath, planner: planner, cache: cache,
	}, nil
}

func (l *lifecycle) Close() error {
	return l.cache.Close()
}

// lockedLocator turns a credential-free locked source back into a locator at
// its recorded ref. Git sources are refetched over their recorded transport: the
// recorded SSH clone URL, or https when none is recorded (older locks).
func (a *App) lockedLocator(locked state.LockedSource) (source.Locator, error) {
	var (
		loc source.Locator
		err error
	)
	switch {
	case locked.Transport == source.TransportSSH && source.Kind(locked.Kind) != source.KindLocal:
		loc, err = source.ParseRemote(locked.URL)
		if err == nil && loc.SSHURL() == "" {
			err = fmt.Errorf("%q is not an SSH clone URL", locked.URL)
		}
	case locked.Transport != "":
		err = fmt.Errorf("unsupported transport %q", locked.Transport)
	case source.Kind(locked.Kind) == source.KindLocal:
		loc, err = source.Parse(locked.Locator, a.WorkDir, a.Env)
	case source.Kind(locked.Kind) == source.KindGitHub:
		loc, err = source.Parse(locked.Locator, "", env.Env{})
	case source.Kind(locked.Kind) == source.KindGit:
		loc, err = source.Parse("https://"+locked.Locator, "", env.Env{})
	default:
		err = fmt.Errorf("unknown source kind %q", locked.Kind)
	}
	if err != nil {
		return source.Locator{}, fmt.Errorf("locked source %s: %w", locked.Locator, err)
	}
	if loc.Kind != source.Kind(locked.Kind) ||
		(loc.IsRemote() && skills.LockedSource(loc, "").Locator != locked.Locator) {
		return source.Locator{}, fmt.Errorf(
			"locked source %s does not match its recorded %s source %s",
			locked.Locator, locked.Kind, cmp.Or(locked.URL, locked.Locator),
		)
	}
	loc.Ref = locked.Ref
	return loc, nil
}

// skillPins maps every locked Skill source and ref to its locked revision.
func (a *App) skillPins(lock *state.Lock) (map[string]string, error) {
	pins := map[string]string{}
	for _, skill := range lock.Skills {
		if skill.Source.Revision == "" {
			continue
		}
		loc, err := a.lockedLocator(skill.Source)
		if err != nil {
			return nil, err
		}
		key := pinKey(loc)
		if pinned, ok := pins[key]; ok && pinned != skill.Source.Revision {
			return nil, fmt.Errorf(
				"the lock records %s at both %s and %s; run `agnthub update` to settle one revision",
				loc.Display(),
				shortRevision(pinned),
				shortRevision(skill.Source.Revision),
			)
		}
		pins[key] = skill.Source.Revision
	}
	return pins, nil
}

func pinKey(loc source.Locator) string {
	return loc.Key() + "#" + loc.Ref
}

// sourceCache opens each source revision once per command and discovers its
// Agents. Frozen caches only ever open locked revisions.
type sourceCache struct {
	opener   source.Opener
	registry *harness.Registry
	frozen   bool
	// pins maps a Skill source (pinKey) to its locked revision.
	pins    map[string]string
	entries map[string]*cachedSource
}

type cachedSource struct {
	snapshot  *source.Snapshot
	inventory *discovery.Inventory
	err       error
}

// Open implements resolve.Opener and skills.Opener. The returned snapshot shares
// the cached tree; the cache closes it.
func (c *sourceCache) Open(ctx context.Context, loc source.Locator) (*source.Snapshot, error) {
	entry := c.open(ctx, loc, "")
	if entry.err != nil {
		return nil, entry.err
	}
	return source.NewSnapshot(loc, entry.snapshot.Revision, entry.snapshot.Tree, nil), nil
}

// open returns loc at revision; an empty revision selects the pinned revision of
// a frozen cache, or the current revision of loc's ref.
func (c *sourceCache) open(ctx context.Context, loc source.Locator, revision string) *cachedSource {
	if revision == "" && c.frozen && loc.IsRemote() {
		pinned, ok := c.pins[pinKey(loc)]
		if !ok {
			return &cachedSource{err: fmt.Errorf(
				"%s is not recorded in the lock, and restore only fetches locked revisions; "+
					"run `agnthub update` to resolve it", loc.Display(),
			)}
		}
		revision = pinned
	}
	key := pinKey(loc) + "@" + revision
	if entry, ok := c.entries[key]; ok {
		return entry
	}
	entry := &cachedSource{}
	c.entries[key] = entry
	fetch := loc
	if revision != "" && loc.IsRemote() {
		fetch.Ref = revision
	}
	snapshot, err := c.opener.Open(ctx, fetch)
	if err != nil {
		entry.err = err
		if revision != "" && loc.IsRemote() {
			entry.err = fmt.Errorf(
				"%w: %s @ %s cannot be fetched (%w); agnthub never substitutes other "+
					"content: make that commit reachable again, or run `agnthub update` "+
					"to adopt what the source provides now",
				ErrRevisionUnavailable, loc.Display(), revision, err,
			)
		}
		return entry
	}
	entry.snapshot = source.NewSnapshot(loc, snapshot.Revision, snapshot.Tree, snapshot.Close)
	if revision != "" && loc.IsRemote() && snapshot.Revision != revision {
		entry.err = fmt.Errorf(
			"%w: fetching %s @ %s returned %s", ErrRevisionUnavailable, loc.Display(), revision,
			snapshot.Revision,
		)
		return entry
	}
	entry.inventory, entry.err = discovery.Discover(
		snapshot.Tree, c.registry, discovery.DefaultLimits(),
	)
	return entry
}

func (c *sourceCache) Close() error {
	var errs []error
	for _, entry := range c.entries {
		if entry.snapshot != nil {
			errs = append(errs, entry.snapshot.Close())
		}
	}
	return errors.Join(errs...)
}

// lifecyclePlan is the merged plan of every binding a lifecycle command handles.
type lifecyclePlan struct {
	plan  *install.Plan
	views []harnessView
	// unavailable explains, per locked Agent key, why its source could not be planned.
	unavailable map[string]error
	// required lists the "skill|harness" pairs the planned Agents require.
	required map[string]bool
}

func (l *lifecycle) newPlan() *lifecyclePlan {
	return &lifecyclePlan{
		plan: &install.Plan{
			Scope: l.scope, LockPath: l.lockPath, Home: l.app.Env.Home(),
		},
		unavailable: map[string]error{},
		required:    map[string]bool{},
	}
}

// add merges one binding's plan, keeping the first item for a shared path.
func (lp *lifecyclePlan) add(plan *install.Plan) {
	paths := make(map[string]bool, len(lp.plan.Items))
	for _, item := range lp.plan.Items {
		paths[item.Path] = true
	}
	for _, item := range plan.Items {
		if item.Path != "" && paths[item.Path] {
			continue
		}
		paths[item.Path] = true
		lp.plan.Items = append(lp.plan.Items, item)
	}
	for _, skill := range plan.Skills {
		duplicate := slices.ContainsFunc(lp.plan.Skills, func(other skills.Item) bool {
			return other.Skill == skill.Skill && other.Harness == skill.Harness &&
				other.Path == skill.Path
		})
		if !duplicate {
			lp.plan.Skills = append(lp.plan.Skills, skill)
		}
	}
}

func (l *lifecycle) view(c *bindingContext) harnessView {
	views := l.app.harnessViews(
		[]agent.HarnessID{c.harness}, map[agent.HarnessID]harness.Settings{c.harness: c.settings},
	)
	view := views[0]
	if c.origin != "" && c.origin != bindingDefault {
		for i := range view.Settings {
			view.Settings[i].Source = c.origin
		}
	}
	return view
}

// planLatest resolves every binding's direct Agents at the current revision of
// their recorded refs, with their complete current Dependency closure. Sources
// that cannot be resolved are reported per Agent, not planned.
func (l *lifecycle) planLatest(
	ctx context.Context,
	contexts []*bindingContext,
	withSkills bool,
) (*lifecyclePlan, error) {
	lp := l.newPlan()
	resolver := &resolve.Resolver{
		Opener: l.cache, Registry: l.app.Registry, Limits: discovery.DefaultLimits(),
	}
	for _, c := range contexts {
		harnesses := []agent.HarnessID{c.harness}
		set := newClosureSet()
		var rootSource state.LockedSource
		for _, root := range directRoots(c) {
			loc, err := l.app.lockedLocator(root.source)
			if err != nil {
				lp.markUnavailable(root, err)
				continue
			}
			entry := l.cache.open(ctx, loc, "")
			if entry.err != nil {
				lp.markUnavailable(root, entry.err)
				continue
			}
			defs := make([]*agent.Definition, 0, len(root.agents))
			for _, locked := range root.agents {
				def := entry.inventory.Find(locked.Agent)
				if def == nil {
					lp.unavailable[locked.Key()] = fmt.Errorf(
						"%s is no longer an installable agent of %s @ %s (remove it with "+
							"`agnthub remove %s`)",
						locked.Agent, loc.Display(), shortRevision(entry.snapshot.Revision),
						locked.Agent,
					)
					continue
				}
				defs = append(defs, def)
			}
			if len(defs) == 0 {
				continue
			}
			closure, err := resolver.Resolve(ctx, resolve.Root{
				Snapshot: entry.snapshot, Inventory: entry.inventory,
			}, defs, harnesses)
			if err != nil {
				lp.markUnavailable(root, err)
				continue
			}
			mergeErr := set.add(closure)
			_ = closure.Close()
			if mergeErr != nil {
				return nil, mergeErr
			}
			if rootSource == (state.LockedSource{}) {
				rootSource = l.app.lockedSource(entry.snapshot)
			}
		}
		if len(set.members) == 0 {
			continue
		}
		closure := set.closure()
		plan, err := l.planner.Build(install.Request{
			Source:    rootSource,
			Targets:   l.app.targets(closure),
			Harnesses: harnesses,
			Settings:  map[agent.HarnessID]harness.Settings{c.harness: c.settings},
			Scope:     l.scope,
			Context:   l.app.harnessContext(),
		})
		if err != nil {
			return nil, err
		}
		for _, requirement := range closure.Skills {
			lp.required[requirement.Skill+"|"+string(c.harness)] = true
		}
		if withSkills {
			plan.Skills, err = l.app.planSkills(ctx, l.planner, l.cache, closure.Skills, harnesses,
				map[agent.HarnessID]harness.Settings{c.harness: c.settings}, l.scope)
			if err != nil {
				return nil, err
			}
		}
		lp.add(plan)
		lp.views = append(lp.views, l.view(c))
	}
	lp.plan.NoteBlockedSkills()
	return lp, nil
}

// rootGroup is the direct Agents of one locked source and ref in one binding.
type rootGroup struct {
	source state.LockedSource
	agents []*state.LockedAgent
}

func directRoots(c *bindingContext) []*rootGroup {
	var roots []*rootGroup
	byKey := map[string]*rootGroup{}
	for _, bound := range c.artifacts {
		if bound.agent.Origin != state.OriginDirect {
			continue
		}
		src := bound.agent.Source
		key := src.Kind + ":" + src.Locator + "#" + src.Ref
		root, ok := byKey[key]
		if !ok {
			root = &rootGroup{source: src}
			byKey[key] = root
			roots = append(roots, root)
		}
		if !slices.Contains(root.agents, bound.agent) {
			root.agents = append(root.agents, bound.agent)
		}
	}
	return roots
}

func (lp *lifecyclePlan) markUnavailable(root *rootGroup, err error) {
	for _, locked := range root.agents {
		lp.unavailable[locked.Key()] = err
	}
}

// closureSet merges the closures of several roots planned in one binding: one
// Member per Agent identity and one requirement per Skill name.
type closureSet struct {
	members []resolve.Member
	index   map[string]int
	skills  []skills.Requirement
	// aliases maps a later closure's label of a merged Member to the kept label.
	aliases map[string]string
}

func newClosureSet() *closureSet {
	return &closureSet{index: map[string]int{}, aliases: map[string]string{}}
}

func (s *closureSet) add(closure *resolve.Closure) error {
	for _, member := range closure.Members {
		key := member.Snapshot.Locator.Key() + "::" + member.Definition.ID
		i, ok := s.index[key]
		if !ok {
			s.index[key] = len(s.members)
			member.Requires = maps.Clone(member.Requires)
			s.members = append(s.members, member)
			continue
		}
		kept := &s.members[i]
		if kept.Snapshot.Revision != member.Snapshot.Revision {
			return fmt.Errorf(
				"%w: %s from %s is required at %s and at %s",
				resolve.ErrConflict, member.Definition.ID, member.Snapshot.Locator.Display(),
				shortRevision(kept.Snapshot.Revision), shortRevision(member.Snapshot.Revision),
			)
		}
		if member.Label != kept.Label {
			s.aliases[member.Label] = kept.Label
		}
		kept.Direct = kept.Direct || member.Direct
		for _, label := range member.RequiredBy {
			if !slices.Contains(kept.RequiredBy, label) {
				kept.RequiredBy = append(kept.RequiredBy, label)
			}
		}
		if kept.Requires == nil {
			kept.Requires = map[agent.HarnessID][]string{}
		}
		for harnessID, labels := range member.Requires {
			for _, label := range labels {
				if !slices.Contains(kept.Requires[harnessID], label) {
					kept.Requires[harnessID] = append(kept.Requires[harnessID], label)
				}
			}
		}
	}
	for _, requirement := range closure.Skills {
		i := slices.IndexFunc(s.skills, func(r skills.Requirement) bool {
			return r.Skill == requirement.Skill
		})
		if i < 0 {
			s.skills = append(s.skills, requirement)
			continue
		}
		kept := &s.skills[i]
		if kept.Source.Key() != requirement.Source.Key() ||
			kept.Source.Ref != requirement.Source.Ref {
			return fmt.Errorf(
				"%w: skill %s is required from %s and from %s",
				resolve.ErrConflict, requirement.Skill, kept.Source.Display(),
				requirement.Source.Display(),
			)
		}
		kept.Needs = append(kept.Needs, requirement.Needs...)
	}
	return nil
}

// closure returns the merged closure with every label naming a kept Member.
func (s *closureSet) closure() *resolve.Closure {
	alias := func(label string) string {
		if kept, ok := s.aliases[label]; ok {
			return kept
		}
		return label
	}
	for i := range s.members {
		member := &s.members[i]
		for j, label := range member.RequiredBy {
			member.RequiredBy[j] = alias(label)
		}
		slices.Sort(member.RequiredBy)
		member.RequiredBy = slices.Compact(member.RequiredBy)
		for harnessID, labels := range member.Requires {
			for j, label := range labels {
				labels[j] = alias(label)
			}
			member.Requires[harnessID] = labels
		}
	}
	for i := range s.skills {
		for j := range s.skills[i].Needs {
			s.skills[i].Needs[j].RequiredBy = alias(s.skills[i].Needs[j].RequiredBy)
		}
	}
	return &resolve.Closure{Members: s.members, Skills: s.skills}
}

// frozenTarget is one locked Agent reopened at its locked revision.
type frozenTarget struct {
	locked *state.LockedAgent
	loc    source.Locator
	def    *agent.Definition
	label  string
}

// planFrozen rebuilds every binding exactly from the lock: each locked Agent from
// its locked revision, Dependencies from the definitions at those revisions, and
// Skills pinned to their locked revisions. Any unavailable source fails the plan.
func (l *lifecycle) planFrozen(
	ctx context.Context,
	contexts []*bindingContext,
) (*lifecyclePlan, error) {
	lp := l.newPlan()
	for _, c := range contexts {
		targets, err := l.frozenTargets(ctx, c)
		if err != nil {
			return nil, err
		}
		installTargets, requirements, err := frozenRequirements(c.harness, targets)
		if err != nil {
			return nil, err
		}
		harnesses := []agent.HarnessID{c.harness}
		settings := map[agent.HarnessID]harness.Settings{c.harness: c.settings}
		plan, err := l.planner.Build(install.Request{
			Source:    targets[0].locked.Source,
			Targets:   installTargets,
			Harnesses: harnesses,
			Settings:  settings,
			Scope:     l.scope,
			Context:   l.app.harnessContext(),
		})
		if err != nil {
			return nil, err
		}
		for _, requirement := range requirements {
			lp.required[requirement.Skill+"|"+string(c.harness)] = true
		}
		plan.Skills, err = l.app.planSkills(ctx, l.planner, l.cache, requirements, harnesses,
			settings, l.scope)
		if err != nil {
			return nil, err
		}
		lp.add(plan)
		lp.views = append(lp.views, l.view(c))
	}
	lp.plan.NoteBlockedSkills()
	return lp, nil
}

func (l *lifecycle) frozenTargets(ctx context.Context, c *bindingContext) ([]frozenTarget, error) {
	var targets []frozenTarget
	for _, bound := range c.artifacts {
		if slices.ContainsFunc(
			targets,
			func(t frozenTarget) bool { return t.locked == bound.agent },
		) {
			continue
		}
		locked := bound.agent
		loc, err := l.app.lockedLocator(locked.Source)
		if err != nil {
			return nil, err
		}
		if loc.IsRemote() && locked.Source.Revision == "" {
			return nil, fmt.Errorf(
				"the lock records no revision for %s; run `agnthub update` to pin one",
				loc.Display(),
			)
		}
		entry := l.cache.open(ctx, loc, locked.Source.Revision)
		if entry.err != nil {
			return nil, entry.err
		}
		def := entry.inventory.Find(locked.Agent)
		if def == nil {
			return nil, fmt.Errorf(
				"%s is not an installable agent of %s @ %s; restore never substitutes "+
					"other content",
				locked.Agent, loc.Display(), shortRevision(locked.Source.Revision),
			)
		}
		label := def.ID
		if locked.Origin != state.OriginDirect {
			label += " (" + loc.Display() + ")"
		}
		targets = append(targets, frozenTarget{locked: locked, loc: loc, def: def, label: label})
	}
	return targets, nil
}

// frozenRequirements derives plan targets and Skill requirements from the locked
// definitions. Every Agent Dependency must itself be locked for the binding.
func frozenRequirements(
	harnessID agent.HarnessID,
	targets []frozenTarget,
) ([]install.Target, []skills.Requirement, error) {
	installTargets := make([]install.Target, len(targets))
	for i, target := range targets {
		installTargets[i] = install.Target{
			Definition: target.def,
			Origin:     target.locked.Origin,
			Source:     target.locked.Source,
			Harnesses:  []agent.HarnessID{harnessID},
			Requires:   map[agent.HarnessID][]string{harnessID: {}},
		}
	}
	var requirements []skills.Requirement
	for i, target := range targets {
		deps := agentDependencies(target.def, harnessID)
		requires := installTargets[i].Requires[harnessID]
		for _, dep := range deps.Agents {
			key := target.loc.Key()
			if dep.Source != "" {
				loc, err := source.ParseRemote(dep.Source)
				if err != nil {
					return nil, nil, fmt.Errorf("%s requires %s: %w", target.label, dep.Agent, err)
				}
				key = loc.Key()
			}
			j := slices.IndexFunc(targets, func(t frozenTarget) bool {
				return t.loc.Key() == key && t.def.ID == dep.Agent
			})
			if j < 0 {
				return nil, nil, fmt.Errorf(
					"the lock is incomplete: %s requires %s for %s, which the lock does not "+
						"record; run `agnthub update` to resolve it",
					target.label, dep.Agent, harnessID,
				)
			}
			requires = appendUnique(requires, state.RequireAgent(targets[j].locked.Key()))
			if targets[j].locked.Origin != state.OriginDirect {
				installTargets[j].RequiredBy = appendUnique(
					installTargets[j].RequiredBy,
					target.label,
				)
			}
		}
		for _, dep := range deps.Skills {
			loc, err := source.ParseRemote(dep.Source)
			if err != nil {
				return nil, nil, fmt.Errorf(
					"%s requires skill %s: %w",
					target.label,
					dep.Skill,
					err,
				)
			}
			bounds, err := skills.ParseBounds(dep.MinVersion, dep.MaxVersion)
			if err != nil {
				return nil, nil, fmt.Errorf(
					"%s requires skill %s: %w",
					target.label,
					dep.Skill,
					err,
				)
			}
			requires = appendUnique(requires, state.RequireSkill(dep.Skill))
			need := skills.Need{Harness: harnessID, Bounds: bounds, RequiredBy: target.label}
			k := slices.IndexFunc(requirements, func(r skills.Requirement) bool {
				return r.Skill == dep.Skill
			})
			if k < 0 {
				requirements = append(requirements, skills.Requirement{
					Skill: dep.Skill, Source: loc, Needs: []skills.Need{need},
				})
				continue
			}
			requirements[k].Needs = append(requirements[k].Needs, need)
		}
		installTargets[i].Requires[harnessID] = requires
	}
	for i := range installTargets {
		slices.Sort(installTargets[i].RequiredBy)
	}
	return installTargets, requirements, nil
}

// agentDependencies returns the Agent's common Dependencies plus its Variant's for h.
func agentDependencies(def *agent.Definition, h agent.HarnessID) agent.Dependencies {
	deps := def.Dependencies
	if variant := def.Variant(h); variant != nil {
		deps.Agents = append(slices.Clip(deps.Agents), variant.Dependencies.Agents...)
		deps.Skills = append(slices.Clip(deps.Skills), variant.Dependencies.Skills...)
	}
	return deps
}

func appendUnique(values []string, value string) []string {
	if slices.Contains(values, value) {
		return values
	}
	return append(values, value)
}

// selectLocked returns a filter for the locked Agents named by ids; no ids selects
// every Agent. With directOnly, naming a companion is an error that names its dependents.
func selectLocked(
	lock *state.Lock,
	ids []string,
	directOnly bool,
) (func(*state.LockedAgent) bool, error) {
	if len(ids) == 0 {
		return func(*state.LockedAgent) bool { return true }, nil
	}
	selected := map[string]bool{}
	for _, id := range ids {
		found, direct := false, false
		for i := range lock.Agents {
			if lock.Agents[i].Agent == id {
				found = true
				direct = direct || lock.Agents[i].Origin == state.OriginDirect
			}
		}
		switch {
		case !found:
			return nil, fmt.Errorf(
				"%s is not installed by agnthub here (installed: %s)", id, lockedIDs(lock),
			)
		case directOnly && !direct:
			return nil, fmt.Errorf(
				"%s is installed as a dependency; update the agents that require it instead", id,
			)
		}
		selected[id] = true
	}
	return func(locked *state.LockedAgent) bool {
		return selected[locked.Agent] && (!directOnly || locked.Origin == state.OriginDirect)
	}, nil
}

func lockedIDs(lock *state.Lock) string {
	ids := make([]string, 0, len(lock.Agents))
	for _, locked := range lock.Agents {
		if !slices.Contains(ids, locked.Agent) {
			ids = append(ids, locked.Agent)
		}
	}
	if len(ids) == 0 {
		return "none"
	}
	return strings.Join(ids, ", ")
}

// lockedHarnesses lists the Harnesses of the included locked artifacts.
func lockedHarnesses(lock *state.Lock, include func(*state.LockedAgent) bool) []agent.HarnessID {
	var ids []agent.HarnessID
	for i := range lock.Agents {
		if !include(&lock.Agents[i]) {
			continue
		}
		for _, artifact := range lock.Agents[i].Artifacts {
			id := agent.HarnessID(artifact.Harness)
			if !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
	}
	return ids
}
