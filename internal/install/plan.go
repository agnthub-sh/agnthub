// Package install plans and applies Agent installations into Harness destinations,
// refusing conflicts before any write.
package install

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/heynemann/agnthub/internal/agent"
	"github.com/heynemann/agnthub/internal/harness"
	"github.com/heynemann/agnthub/internal/skills"
	"github.com/heynemann/agnthub/internal/state"
)

// Action is what applying an item will do.
type Action string

// Plan actions.
const (
	ActionCreate       Action = "create"
	ActionReplace      Action = "replace"
	ActionUnchanged    Action = "unchanged"
	ActionConflict     Action = "conflict"
	ActionIncompatible Action = "incompatible"
	ActionInvalid      Action = "invalid"
)

// Blocking reports whether the action prevents applying the plan.
func (a Action) Blocking() bool {
	return a == ActionConflict || a == ActionIncompatible || a == ActionInvalid
}

// Item is one Agent placement for one Harness.
type Item struct {
	Agent    string
	Harness  agent.HarnessID
	Settings harness.Settings
	// Binding is the effective Settings the installation is recorded under: the
	// explicit Settings plus detected native defaults (e.g. the active omp profile),
	// so later updates keep the binding instead of following a newly active default.
	Binding     harness.Settings
	Destination harness.Destination
	// Path is the absolute artifact path.
	Path         string
	Artifact     harness.Artifact
	SourcePath   string
	SourceDigest string
	// Source is the exact source the Agent comes from; companions keep their own.
	Source state.LockedSource
	Origin state.Origin
	// RequiredBy names the dependents of a dependency Item.
	RequiredBy []string
	// Requires is recorded as the locked artifact's state.LockedArtifact.Requires;
	// nil records unknown requirements.
	Requires []string
	Action   Action
	Reason   string
}

// Plan is a fully resolved installation, computed before any write.
type Plan struct {
	Scope    harness.Scope
	Source   state.LockedSource
	LockPath string
	Items    []Item
	// Skills are the preflighted Skill Dependencies of Items, per Harness. They
	// install and verify before any Agent artifact is written.
	Skills []skills.Item
	// Home is the user's home directory, used to keep locked Skill paths portable.
	Home string
}

// Blocking returns the items that prevent applying the plan.
func (p *Plan) Blocking() []Item {
	var blocking []Item
	for _, item := range p.Items {
		if item.Action.Blocking() {
			blocking = append(blocking, item)
		}
	}
	return blocking
}

// BlockingSkills returns the Skill items that prevent applying the plan.
func (p *Plan) BlockingSkills() []skills.Item {
	var blocking []skills.Item
	for _, item := range p.Skills {
		if item.Blocking() {
			blocking = append(blocking, item)
		}
	}
	return blocking
}

// Blocked reports whether any Agent or Skill item blocks the plan.
func (p *Plan) Blocked() bool {
	return len(p.Blocking()) > 0 || len(p.BlockingSkills()) > 0
}

// Reduce returns the plan without its blocking items, plus every Agent and Skill
// item it drops. Dependency closure holds per Harness: an Agent requiring a
// dropped companion or a blocked Skill is dropped too, and so is a companion or
// Skill that no kept Agent requires. Requirers are matched by Agent ID, which can
// drop more than strictly needed but never less.
func (p *Plan) Reduce() (*Plan, []Item, []skills.Item) {
	dropped := make([]bool, len(p.Items))
	reasons := make([]string, len(p.Items))
	for i, item := range p.Items {
		dropped[i] = item.Action.Blocking()
		if dropped[i] {
			continue
		}
		for _, skill := range p.Skills {
			if skill.Blocking() && skill.Harness == item.Harness &&
				requiresSkill(skill, item.Agent) {
				dropped[i] = true
				reasons[i] = fmt.Sprintf("skipped: requires skill %s, which is blocked",
					skill.Skill)
				break
			}
		}
	}
	for changed := true; changed; {
		changed = false
		for i := range p.Items {
			if dropped[i] {
				continue
			}
			if reason := p.orphaned(i, dropped); reason != "" {
				dropped[i], reasons[i], changed = true, reason, true
			}
		}
	}
	kept := &Plan{Scope: p.Scope, Source: p.Source, LockPath: p.LockPath, Home: p.Home}
	var skipped []Item
	for i, item := range p.Items {
		if !dropped[i] {
			kept.Items = append(kept.Items, item)
			continue
		}
		if reasons[i] != "" {
			item.Reason = reasons[i]
		}
		skipped = append(skipped, item)
	}
	var skippedSkills []skills.Item
	for _, skill := range p.Skills {
		if !skill.Blocking() && kept.requiredSkill(skill) {
			kept.Skills = append(kept.Skills, skill)
			continue
		}
		if !skill.Blocking() {
			skill.Reason = "skipped: every agent requiring it is skipped"
		}
		skippedSkills = append(skippedSkills, skill)
	}
	return kept, skipped, skippedSkills
}

// requiredSkill reports whether a kept Agent item requires skill.
func (p *Plan) requiredSkill(skill skills.Item) bool {
	for _, item := range p.Items {
		if item.Harness == skill.Harness && requiresSkill(skill, item.Agent) {
			return true
		}
	}
	return false
}

func requiresSkill(skill skills.Item, id string) bool {
	for _, label := range skill.RequiredBy {
		if dependent, _, _ := strings.Cut(label, " ("); dependent == id {
			return true
		}
	}
	return false
}

// NoteBlockedSkills notes, on each Agent item, the blocked Skills it requires,
// so a refused preview shows why an otherwise valid Agent cannot be written.
func (p *Plan) NoteBlockedSkills() {
	for i := range p.Items {
		item := &p.Items[i]
		for _, skill := range p.Skills {
			if skill.Blocking() && skill.Harness == item.Harness &&
				requiresSkill(skill, item.Agent) {
				item.Artifact.Notes = append(slices.Clip(item.Artifact.Notes), fmt.Sprintf(
					"waits on skill %s, which is %s for %s", skill.Skill, skill.Decision,
					skill.Harness))
			}
		}
	}
}

// orphaned explains why item i cannot be kept without the dropped items, or "".
func (p *Plan) orphaned(i int, dropped []bool) string {
	item := p.Items[i]
	requirerKept := false
	for j, other := range p.Items {
		if j == i || other.Harness != item.Harness {
			continue
		}
		if dropped[j] && requires(other, item.Agent) {
			return fmt.Sprintf("skipped: requires %s, which is skipped", other.Agent)
		}
		if !dropped[j] && requires(item, other.Agent) {
			requirerKept = true
		}
	}
	if len(item.RequiredBy) > 0 && !requirerKept {
		return "skipped: every agent requiring it is skipped"
	}
	return ""
}

// requires reports whether dependency lists id among its dependents. Dependent
// labels are an Agent ID, optionally followed by " (<source>)".
func requires(dependency Item, id string) bool {
	for _, label := range dependency.RequiredBy {
		if dependent, _, _ := strings.Cut(label, " ("); dependent == id {
			return true
		}
	}
	return false
}

// Target is one selected Agent with its origin.
type Target struct {
	Definition *agent.Definition
	Origin     state.Origin
	// Source overrides Request.Source for an Agent from another source.
	Source state.LockedSource
	// Harnesses restricts the Target to these requested Harnesses; nil means all.
	Harnesses []agent.HarnessID
	// RequiredBy names the dependents that required a dependency Target.
	RequiredBy []string
	// Requires lists, per Harness, the state.RequireAgent/RequireSkill entries of
	// what this Target needs. A nil map records unknown requirements.
	Requires map[agent.HarnessID][]string
}

// Request describes what to plan.
type Request struct {
	// Source is the selected source and the default source of every Target.
	Source    state.LockedSource
	Targets   []Target
	Harnesses []agent.HarnessID
	Settings  map[agent.HarnessID]harness.Settings
	Scope     harness.Scope
	Context   harness.Context
}

// Planner resolves requests against existing local state.
type Planner struct {
	Registry  *harness.Registry
	Lock      *state.Lock
	LockPath  string
	Ownership *state.Ownership
}

// Build resolves artifacts, destinations and actions for every Agent/Harness pair.
func (p *Planner) Build(req Request) (*Plan, error) {
	plan := &Plan{
		Scope: req.Scope, Source: req.Source, LockPath: p.LockPath, Home: req.Context.Env.Home(),
	}
	claimed := map[string]string{}
	for _, target := range req.Targets {
		for _, harnessID := range req.Harnesses {
			if target.Harnesses != nil && !slices.Contains(target.Harnesses, harnessID) {
				continue
			}
			item, err := p.item(req, target, harnessID)
			if err != nil {
				return nil, err
			}
			if item.Path != "" {
				if owner, dup := claimed[item.Path]; dup {
					item.Action = ActionConflict
					item.Reason = "also planned for " + owner
				}
				claimed[item.Path] = target.Definition.ID
			}
			plan.Items = append(plan.Items, item)
		}
	}
	return plan, nil
}

func (p *Planner) item(req Request, target Target, harnessID agent.HarnessID) (Item, error) {
	def := target.Definition
	item := Item{
		Agent:      def.ID,
		Harness:    harnessID,
		Settings:   req.Settings[harnessID],
		Source:     req.Source,
		Origin:     target.Origin,
		RequiredBy: target.RequiredBy,
	}
	if target.Requires != nil {
		item.Requires = append([]string{}, target.Requires[harnessID]...)
	}
	if target.Source != (state.LockedSource{}) {
		item.Source = target.Source
	}
	installer, err := p.Registry.Installer(harnessID)
	if err != nil {
		if errors.Is(err, harness.ErrInstallUnsupported) {
			item.Action, item.Reason = ActionIncompatible, err.Error()
			return item, nil
		}
		return item, err
	}
	artifact, sourcePath, sourceRaw, err := render(installer, def)
	if err != nil {
		var incompatible *harness.IncompatibleError
		var invalid *harness.InvalidVariantError
		switch {
		case errors.As(err, &incompatible):
			item.Action, item.Reason = ActionIncompatible, incompatible.Reason
			return item, nil
		case errors.As(err, &invalid):
			item.Action, item.Reason = ActionInvalid, invalid.Error()
			return item, nil
		default:
			item.Action, item.Reason = ActionInvalid, err.Error()
			return item, nil
		}
	}
	if advisor, ok := installer.(harness.DependencyAdvisor); ok {
		artifact.Notes = append(
			artifact.Notes,
			advisor.DependencyNotes(dependencies(def, harnessID))...)
	}
	destination, err := installer.Destination(req.Context, req.Scope, item.Settings)
	if err != nil {
		item.Action, item.Reason = ActionIncompatible, err.Error()
		return item, nil
	}
	item.Artifact = artifact
	item.Destination = destination
	item.Binding = effectiveBinding(installer, req.Context, item.Settings)
	item.SourcePath = sourcePath
	item.SourceDigest = state.Digest(sourceRaw)
	item.Path = filepath.Join(destination.Dir, artifact.FileName)
	item.Action, item.Reason, err = p.action(req, item)
	return item, err
}

// effectiveBinding returns settings plus the native defaults the installer reports.
func effectiveBinding(
	installer harness.Installer,
	ctx harness.Context,
	settings harness.Settings,
) harness.Settings {
	reporter, ok := installer.(harness.SettingsReporter)
	if !ok {
		return settings
	}
	effective := reporter.EffectiveSettings(ctx, settings)
	if len(effective) == 0 {
		return settings
	}
	binding := make(harness.Settings, len(effective))
	for _, setting := range effective {
		binding[setting.Key] = setting.Value
	}
	return binding
}

// render prefers an explicit Variant; an invalid Variant never falls back.
func render(
	installer harness.Installer,
	def *agent.Definition,
) (harness.Artifact, string, []byte, error) {
	if variant := def.Variant(installer.ID()); variant != nil {
		artifact, err := installer.Native(def.ID, variant)
		return artifact, variant.Path, variant.Raw, err
	}
	artifact, err := installer.Convert(def.ID, def.Portable)
	if err != nil {
		return artifact, "", nil, err
	}
	return artifact, def.Portable.Path, def.Portable.Raw, nil
}

// dependencies returns the Agent's common Dependencies plus its Variant's for h.
func dependencies(def *agent.Definition, h agent.HarnessID) agent.Dependencies {
	deps := def.Dependencies
	if variant := def.Variant(h); variant != nil {
		deps.Agents = append(slices.Clip(deps.Agents), variant.Dependencies.Agents...)
		deps.Skills = append(slices.Clip(deps.Skills), variant.Dependencies.Skills...)
	}
	return deps
}

// action decides whether writing item is safe given what exists on disk.
func (p *Planner) action(req Request, item Item) (Action, string, error) {
	onDisk, exists, err := state.FileDigest(item.Path)
	if err != nil {
		return ActionConflict, err.Error(), nil
	}
	if !exists {
		return ActionCreate, "", nil
	}
	recorded, owned := p.recordedDigest(req, item.Path)
	newDigest := state.Digest(item.Artifact.Content)
	switch {
	case !owned:
		return ActionConflict, "existing file is not managed by agnthub", nil
	case onDisk != recorded:
		return ActionConflict, "managed file was modified locally", nil
	case onDisk == newDigest:
		return ActionUnchanged, "", nil
	default:
		return ActionReplace, "", nil
	}
}

// recordedDigest returns the digest agnthub recorded for path, from user-local
// ownership or, for project scope, the shareable lock.
func (p *Planner) recordedDigest(req Request, path string) (string, bool) {
	if records := p.Ownership.ForPath(path); len(records) > 0 {
		return records[0].Digest, true
	}
	if req.Scope == harness.ScopeProject && p.Lock != nil {
		if rel, err := filepath.Rel(req.Context.ProjectRoot, path); err == nil {
			return p.Lock.DigestForPath(filepath.ToSlash(rel))
		}
	}
	return "", false
}

// Summary counts plan actions.
func (p *Plan) Summary() map[Action]int {
	counts := map[Action]int{}
	for _, item := range p.Items {
		counts[item.Action]++
	}
	return counts
}

// ErrBlocked reports a plan that cannot be applied.
var ErrBlocked = errors.New("plan has blocking problems")

// Validate returns ErrBlocked with details when any item blocks.
func (p *Plan) Validate() error {
	blocking := p.Blocking()
	if len(blocking) == 0 {
		if skillsBlocking := p.BlockingSkills(); len(skillsBlocking) > 0 {
			first := skillsBlocking[0]
			return fmt.Errorf(
				"%w: %d skill(s), first %s for %s: %s",
				ErrBlocked, len(skillsBlocking), first.Skill, first.Harness, first.Reason,
			)
		}
		return nil
	}
	first := blocking[0]
	return fmt.Errorf(
		"%w: %d item(s), first %s for %s: %s",
		ErrBlocked, len(blocking), first.Agent, first.Harness, first.Reason,
	)
}
