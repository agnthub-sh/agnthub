package skills

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/agnthub-sh/agnthub/agent"
	"github.com/agnthub-sh/agnthub/harness"
	"github.com/agnthub-sh/agnthub/source"
	"github.com/agnthub-sh/agnthub/state"
)

// Need is one dependent's requirement on a Skill for one Harness.
type Need struct {
	Harness agent.HarnessID
	Bounds  Bounds
	// RequiredBy is the dependent Agent's label.
	RequiredBy string
}

// Requirement is one required Skill identity: a name from one source revision,
// with every dependent's bounds per Harness.
type Requirement struct {
	Skill  string
	Source source.Locator
	Needs  []Need
}

// Decision is what the plan does about one Skill for one Harness.
type Decision string

// Skill decisions. Reuse, unchanged and install are applicable; the rest block.
const (
	// DecisionReuse uses a compatible visible Skill agnthub does not own.
	DecisionReuse Decision = "reuse"
	// DecisionUnchanged keeps a compatible Skill an earlier agnthub run installed.
	DecisionUnchanged Decision = "unchanged"
	// DecisionInstall runs the Skill installer backend, then verifies the result.
	DecisionInstall Decision = "install"
	// DecisionShadowed reports a compatible Skill hidden by an incompatible higher-priority copy.
	DecisionShadowed Decision = "shadowed"
	// DecisionConflict reports incompatible existing content agnthub will not replace.
	DecisionConflict Decision = "conflict"
	// DecisionUnsatisfied reports a source revision that cannot satisfy the requirement.
	DecisionUnsatisfied Decision = "unsatisfied"
	// DecisionDisabled reports a Skill the Harness configuration hides.
	DecisionDisabled Decision = "disabled"
	// DecisionUnsupported reports a Harness without Skill support or placement.
	DecisionUnsupported Decision = "unsupported"
)

// Blocking reports whether the decision prevents installing dependent Agents.
func (d Decision) Blocking() bool {
	switch d {
	case DecisionReuse, DecisionUnchanged, DecisionInstall:
		return false
	default:
		return true
	}
}

// Item is one required Skill for one Harness binding.
type Item struct {
	Skill    string
	Source   source.Locator
	Harness  agent.HarnessID
	Settings harness.Settings
	// Requirement describes the combined bounds, e.g. ">=1.0.0 <=1.9.9".
	Requirement string
	bounds      []Bounds
	RequiredBy  []string
	Decision    Decision
	Reason      string
	// Path is the reused Skill directory or the directory the backend installs.
	Path string
	// Location labels the discovery location of Path.
	Location string
	// Scope is the scope of Path's discovery location.
	Scope harness.Scope
	// Version is the declared metadata.version of Path or of the source Skill.
	Version string
	// Digest is the content digest of Path, or of the source Skill for an install.
	Digest string
	// Revision is the source revision validated for an install or matched by content.
	Revision string
	// Target is the backend target identifier for an install.
	Target string
	// Command is the backend invocation (executable first) for an install.
	Command []string
	// Shadowed is the compatible copy that an incompatible higher-priority copy hides.
	Shadowed string
}

// Blocking reports whether the item prevents installing its dependents.
func (i *Item) Blocking() bool {
	return i.Decision.Blocking()
}

// Opener resolves source locators into inert snapshots; source.Opener satisfies it.
type Opener interface {
	Open(ctx context.Context, loc source.Locator) (*source.Snapshot, error)
}

// Planner preflights Skill requirements against what each Harness already sees.
type Planner struct {
	Registry  *harness.Registry
	Opener    Opener
	Installer Installer
	Ownership *state.Ownership
	Lock      *state.Lock
}

// Request describes the Skills to preflight for one plan.
type Request struct {
	Requirements []Requirement
	// Harnesses orders the items of each requirement.
	Harnesses []agent.HarnessID
	Settings  map[agent.HarnessID]harness.Settings
	Scope     harness.Scope
	Context   harness.Context
}

// candidate is one existing directory a Harness may load as the required Skill.
type candidate struct {
	path     string
	location harness.SkillLocation
	content  Content
	err      error
}

// fetched is one source revision's copy of a required Skill.
type fetched struct {
	revision string
	content  Content
	err      error
}

type planRun struct {
	planner *Planner
	ctx     context.Context
	req     Request
	sources map[string]*fetched
	opened  []*source.Snapshot
}

// Plan preflights every requirement for every Harness that needs it. It never
// writes: it decides reuse, install or a blocking problem for each item.
func (p *Planner) Plan(ctx context.Context, req Request) []Item {
	run := &planRun{planner: p, ctx: ctx, req: req, sources: map[string]*fetched{}}
	defer func() {
		for _, snapshot := range run.opened {
			_ = snapshot.Close()
		}
	}()
	var items []Item
	for _, requirement := range req.Requirements {
		for _, harnessID := range req.Harnesses {
			needs := needsFor(requirement, harnessID)
			if len(needs) == 0 {
				continue
			}
			items = append(items, run.item(requirement, harnessID, needs))
		}
	}
	return items
}

func needsFor(requirement Requirement, harnessID agent.HarnessID) []Need {
	var needs []Need
	for _, need := range requirement.Needs {
		if need.Harness == harnessID {
			needs = append(needs, need)
		}
	}
	return needs
}

func (r *planRun) item(requirement Requirement, harnessID agent.HarnessID, needs []Need) Item {
	item := Item{
		Skill:    requirement.Skill,
		Source:   requirement.Source,
		Harness:  harnessID,
		Settings: r.req.Settings[harnessID],
	}
	seen := map[string]bool{}
	var described []string
	for _, need := range needs {
		if !slices.Contains(item.RequiredBy, need.RequiredBy) {
			item.RequiredBy = append(item.RequiredBy, need.RequiredBy)
		}
		item.bounds = append(item.bounds, need.Bounds)
		if text := need.Bounds.String(); !seen[text] && need.Bounds.Bounded() {
			seen[text] = true
			described = append(described, text)
		}
	}
	slices.Sort(item.RequiredBy)
	item.Requirement = "any version"
	if len(described) > 0 {
		item.Requirement = strings.Join(described, ", ")
	}
	installer, err := r.planner.Registry.Installer(harnessID)
	if err != nil {
		item.Decision, item.Reason = DecisionUnsupported, err.Error()
		return item
	}
	skillHarness, ok := installer.(harness.SkillHarness)
	if !ok {
		item.Decision = DecisionUnsupported
		item.Reason = installer.Name() + " does not support skill dependencies"
		return item
	}
	visibility, err := skillHarness.SkillVisibility(r.req.Context, r.req.Scope, item.Settings)
	if err != nil {
		item.Decision, item.Reason = DecisionUnsupported, err.Error()
		return item
	}
	if reason := visibility.HiddenReason(item.Skill, ""); reason != "" {
		item.Decision, item.Reason = DecisionDisabled, reason
		return item
	}
	candidates := scan(visibility, item.Skill)
	placement, err := skillHarness.SkillPlacement(r.req.Context, r.req.Scope, item.Settings)
	if err != nil {
		item.Decision, item.Reason = DecisionUnsupported, err.Error()
		return item
	}
	decided := len(candidates) > 0 && r.decideExisting(&item, candidates, visibility, placement)
	if !decided {
		r.planInstall(&item, visibility, placement)
	}
	if !item.Blocking() {
		if reason := visibility.HiddenReason(item.Skill, item.Path); reason != "" {
			item.Decision, item.Reason = DecisionDisabled, reason
		}
	}
	return item
}

// uniqueNameConflict reports the copies of a Harness that cannot select a Skill
// by name once two copies are visible.
func uniqueNameConflict(item *Item, candidates []candidate) {
	paths := make([]string, 0, len(candidates))
	for _, c := range candidates {
		paths = append(paths, c.path)
	}
	item.Decision = DecisionConflict
	item.Path, item.Location, item.Scope = candidates[0].path, candidates[0].location.Label,
		candidates[0].location.Scope
	item.Version = candidates[0].content.Version
	item.Reason = fmt.Sprintf(
		"%s loads every copy of %s and then cannot select it by name; remove all but one of %s",
		item.Harness, item.Skill, strings.Join(paths, ", "),
	)
}

// decideExisting settles an item from visible copies. It returns false when the
// only visible copies are incompatible and the install location takes precedence
// over all of them, so a fresh install would be the copy the Harness loads.
func (r *planRun) decideExisting(
	item *Item,
	candidates []candidate,
	visibility harness.SkillVisibility,
	placement harness.SkillPlacement,
) bool {
	if visibility.UniqueName && len(candidates) > 1 {
		uniqueNameConflict(item, candidates)
		return true
	}
	effective := candidates[0]
	ok, why := r.compatible(item, effective)
	if ok && visibility.EveryCopyLoads {
		for _, other := range candidates[1:] {
			if compatible, otherWhy := r.compatible(item, other); !compatible {
				item.Decision = DecisionConflict
				item.Path, item.Location, item.Scope = other.path, other.location.Label,
					other.location.Scope
				item.Version = other.content.Version
				item.Reason = fmt.Sprintf(
					"%s loads every copy of %s, and %s (%s) is incompatible: %s",
					item.Harness, item.Skill, other.path, other.location.Label, otherWhy,
				)
				return true
			}
		}
	}
	if ok {
		item.Decision = DecisionReuse
		if r.owned(item, effective) {
			item.Decision = DecisionUnchanged
		}
		item.Path, item.Location, item.Scope = effective.path, effective.location.Label,
			effective.location.Scope
		item.Version, item.Digest = effective.content.Version, effective.content.Digest
		item.Reason = why
		return true
	}
	for _, other := range candidates[1:] {
		if compatible, _ := r.compatible(item, other); compatible {
			item.Decision = DecisionShadowed
			item.Path, item.Location, item.Scope = effective.path, effective.location.Label,
				effective.location.Scope
			item.Version, item.Shadowed = effective.content.Version, other.path
			item.Reason = fmt.Sprintf(
				"%s (%s) shadows the compatible copy at %s: %s",
				effective.path, effective.location.Label, other.path, why,
			)
			return true
		}
	}
	if !visibility.EveryCopyLoads && !visibility.UniqueName &&
		precedes(visibility, placement.Dir, effective.location.Dir) {
		item.Reason = fmt.Sprintf(
			"installs ahead of the incompatible %s (%s), which stays untouched: %s",
			effective.path, effective.location.Label, why,
		)
		return false
	}
	item.Decision = DecisionConflict
	item.Path, item.Location, item.Scope = effective.path, effective.location.Label,
		effective.location.Scope
	item.Version = effective.content.Version
	item.Reason = fmt.Sprintf(
		"existing %s (%s) is incompatible and agnthub never replaces it: %s",
		effective.path, effective.location.Label, why,
	)
	return true
}

// precedes reports whether dir comes strictly before other in precedence order.
func precedes(visibility harness.SkillVisibility, dir, other string) bool {
	for _, location := range visibility.Locations {
		switch filepath.Clean(location.Dir) {
		case filepath.Clean(other):
			return false
		case filepath.Clean(dir):
			return true
		}
	}
	return false
}

func (r *planRun) planInstall(
	item *Item,
	visibility harness.SkillVisibility,
	placement harness.SkillPlacement,
) {
	location, ok := locationFor(visibility, placement.Dir)
	if !ok {
		item.Decision = DecisionUnsupported
		item.Reason = fmt.Sprintf(
			"the %s backend target installs into %s, which %s does not discover",
			placement.Target, placement.Dir, item.Harness,
		)
		if visibility.Note != "" {
			item.Reason += ": " + visibility.Note
		}
		return
	}
	item.Path = filepath.Join(placement.Dir, item.Skill)
	item.Location, item.Scope, item.Target = location.Label, location.Scope, placement.Target
	if location.Scope != r.req.Scope {
		item.Decision = DecisionUnsupported
		item.Reason = fmt.Sprintf("%s is not a %s location", placement.Dir, r.req.Scope)
		return
	}
	if _, err := os.Lstat(item.Path); err == nil {
		item.Decision = DecisionConflict
		item.Reason = fmt.Sprintf(
			"%s already exists and is not the required skill; the backend would replace it",
			item.Path,
		)
		return
	} else if !errors.Is(err, fs.ErrNotExist) {
		item.Decision, item.Reason = DecisionConflict, err.Error()
		return
	}
	got := r.fetch(item.Source, item.Skill)
	item.Revision = got.revision
	if got.err != nil {
		item.Decision = DecisionUnsatisfied
		item.Reason = fmt.Sprintf("%s: %v", revisionLabel(item.Source, got.revision), got.err)
		return
	}
	item.Version, item.Digest = got.content.Version, got.content.Digest
	for _, bounds := range item.bounds {
		if why := bounds.Check(got.content.Version, got.content.HasVersion); why != "" {
			item.Decision = DecisionUnsatisfied
			item.Reason = fmt.Sprintf(
				"%s provides %s: %s", revisionLabel(item.Source, got.revision), item.Skill, why,
			)
			return
		}
	}
	item.Decision = DecisionInstall
	args := r.planner.Installer.Command(Values{
		Source: SourceArgument(item.Source, got.revision),
		Skill:  item.Skill,
		Target: placement.Target,
	}, r.req.Scope)
	item.Command = append([]string{r.planner.Installer.Executable}, args...)
}

func locationFor(visibility harness.SkillVisibility, dir string) (harness.SkillLocation, bool) {
	for _, location := range visibility.Locations {
		if filepath.Clean(location.Dir) == filepath.Clean(dir) {
			return location, true
		}
	}
	return harness.SkillLocation{}, false
}

// compatible decides whether an existing copy satisfies the item. Bounded
// requirements need a strict metadata.version inside every bound; unbounded ones
// need evidence it comes from the required source: an agnthub record of that
// source with the same content, or content identical to the requested revision.
func (r *planRun) compatible(item *Item, c candidate) (bool, string) {
	if c.err != nil {
		return false, "its SKILL.md cannot be read: " + c.err.Error()
	}
	if c.content.Name != item.Skill {
		return false, fmt.Sprintf("its SKILL.md is named %q", c.content.Name)
	}
	bounded := false
	for _, bounds := range item.bounds {
		if !bounds.Bounded() {
			continue
		}
		bounded = true
		if why := bounds.Check(c.content.Version, c.content.HasVersion); why != "" {
			return false, why
		}
	}
	if bounded {
		return true, fmt.Sprintf("version %s satisfies %s", c.content.Version, item.Requirement)
	}
	if r.recorded(item, c) {
		return true, "agnthub recorded this content from " + item.Source.Display()
	}
	got := r.fetch(item.Source, item.Skill)
	if got.err != nil {
		return false, fmt.Sprintf(
			"no record of its source, and %s cannot be checked: %v",
			revisionLabel(item.Source, got.revision), got.err,
		)
	}
	if got.content.Digest == c.content.Digest {
		item.Revision = got.revision
		return true, "identical to " + revisionLabel(item.Source, got.revision)
	}
	return false, fmt.Sprintf(
		"its content differs from %s and nothing records it came from that source",
		revisionLabel(item.Source, got.revision),
	)
}

// owned reports an unmodified copy that agnthub's backend run created.
func (r *planRun) owned(item *Item, c candidate) bool {
	if r.planner.Ownership == nil {
		return false
	}
	for _, record := range r.planner.Ownership.ForPath(c.path) {
		if record.Skill == item.Skill && record.Digest == c.content.Digest &&
			record.Source == SourceKey(item.Source) {
			return true
		}
	}
	return false
}

// recorded reports agnthub evidence that c holds item's Skill from item's source.
func (r *planRun) recorded(item *Item, c candidate) bool {
	if r.owned(item, c) {
		return true
	}
	if r.planner.Lock == nil {
		return false
	}
	want := LockedSource(item.Source, "")
	for _, locked := range r.planner.Lock.Skills {
		if locked.Skill == item.Skill && locked.Digest == c.content.Digest &&
			locked.Source.Kind == want.Kind && locked.Source.Locator == want.Locator {
			return true
		}
	}
	return false
}

// fetch opens the requested revision once and reads the required Skill from it.
func (r *planRun) fetch(loc source.Locator, skill string) *fetched {
	key := loc.Key() + "#" + loc.Ref + "::" + skill
	if got, ok := r.sources[key]; ok {
		return got
	}
	got := &fetched{}
	r.sources[key] = got
	snapshot, err := r.planner.Opener.Open(r.ctx, loc)
	if err != nil {
		got.err = err
		return got
	}
	r.opened = append(r.opened, snapshot)
	got.revision = snapshot.Revision
	dir, err := findSourceSkill(snapshot.Tree, skill)
	if err != nil {
		got.err = err
		return got
	}
	got.content, got.err = readSourceSkill(snapshot.Tree, dir)
	return got
}

// scan lists every visible directory that may hold the named Skill, in
// precedence order: a directory named after the Skill or a SKILL.md naming it.
func scan(visibility harness.SkillVisibility, skill string) []candidate {
	var candidates []candidate
	for _, location := range visibility.Locations {
		entries, err := os.ReadDir(location.Dir)
		if err != nil {
			continue
		}
		if len(entries) > maxLocationSkills {
			entries = entries[:maxLocationSkills]
		}
		// The directory named after the Skill comes first within one location.
		slices.SortStableFunc(entries, func(a, b os.DirEntry) int {
			switch {
			case a.Name() == skill && b.Name() != skill:
				return -1
			case b.Name() == skill && a.Name() != skill:
				return 1
			default:
				return 0
			}
		})
		for _, entry := range entries {
			dir := filepath.Join(location.Dir, entry.Name())
			if info, err := os.Stat(dir); err != nil || !info.IsDir() {
				continue
			}
			if entry.Name() != skill && skillName(dir) != skill {
				continue
			}
			content, err := readInstalledSkill(dir)
			candidates = append(candidates, candidate{
				path: dir, location: location, content: content, err: err,
			})
		}
	}
	return candidates
}

// SourceKey is the credential-free source identity recorded in ownership records.
func SourceKey(loc source.Locator) string {
	locked := LockedSource(loc, "")
	return locked.Kind + ":" + locked.Locator
}

// LockedSource is the credential-free lock form of a remote source, including
// the SSH clone URL of a source fetched over SSH.
func LockedSource(loc source.Locator, revision string) state.LockedSource {
	locked := state.LockedSource{Kind: string(loc.Kind), Ref: loc.Ref, Revision: revision}
	switch loc.Kind {
	case source.KindGitHub:
		locked.Locator = loc.Owner + "/" + loc.Repo
	case source.KindGit:
		locked.Locator = loc.Host + "/" + loc.RepoPath
	default:
		locked.Locator = loc.Path
	}
	if url := loc.SSHURL(); url != "" {
		locked.Transport, locked.URL = source.TransportSSH, url
	}
	return locked
}

// SourceArgument is the {source} value: the source pinned to the validated
// revision, so the backend installs the bytes agnthub checked.
func SourceArgument(loc source.Locator, revision string) string {
	ref := revision
	if ref == "" {
		ref = loc.Ref
	}
	var base string
	switch loc.Kind {
	case source.KindGitHub:
		base = loc.Owner + "/" + loc.Repo
	case source.KindGit:
		base = loc.CloneURL
	default:
		return loc.Path
	}
	if ref == "" {
		return base
	}
	return base + "#" + ref
}

func revisionLabel(loc source.Locator, revision string) string {
	label := loc.Display()
	if revision != "" {
		short := revision
		if len(short) > 12 {
			short = short[:12]
		}
		label += " @ " + short
	}
	return label
}
