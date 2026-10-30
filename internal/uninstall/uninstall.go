// Package uninstall plans and applies the removal of agnthub-owned Agent and
// Skill artifacts. It deletes only unmodified artifacts whose ownership is proven,
// keeps an artifact while another binding still uses it, and never removes what a
// remaining Agent requires.
package uninstall

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/agnthub-sh/agnthub/skills"
	"github.com/agnthub-sh/agnthub/state"
)

// Kind distinguishes Agent artifacts from Skill directories.
type Kind string

// Unit kinds.
const (
	KindAgent Kind = "agent"
	KindSkill Kind = "skill"
)

// Unit is one locked artifact for one Harness and, when agnthub recorded it on
// this machine, one Harness Settings binding.
type Unit struct {
	Kind Kind
	// Name is the Agent ID or Skill name.
	Name string
	// Key is LockedAgent.Key() for an Agent and the Skill name for a Skill.
	Key     string
	Source  state.LockedSource
	Origin  state.Origin
	Harness string
	// Path is absolute; LockPath is the path as written in the lock.
	Path     string
	LockPath string
	// Digest is the content digest agnthub recorded.
	Digest string
	// Record is the ownership record of this binding; nil when only the lock
	// knows the artifact (another machine installed it, or a reused Skill).
	Record *state.Record
	// Requires are the locked requirements of an Agent artifact; RequiresKnown is
	// false for locks written before requirements were recorded.
	Requires      []string
	RequiresKnown bool
}

// Settings returns the recorded Harness Settings of the binding, if any.
func (u *Unit) Settings() map[string]string {
	if u.Record == nil {
		return nil
	}
	return u.Record.Settings
}

// Owned reports whether agnthub can prove it created the artifact: every locked
// Agent artifact (by its recorded digest), but only Skills its backend installed.
func (u *Unit) Owned() bool {
	return u.Kind == KindAgent || u.Record != nil
}

// Label names the unit for humans, e.g. "reviewer (omp, profile=hep)".
func (u *Unit) Label() string {
	label := u.Name + " (" + u.Harness
	if settings := state.SettingsKey(u.Settings()); settings != "" {
		label += ", " + settings
	}
	return label + ")"
}

func (u *Unit) requirement() string {
	if u.Kind == KindSkill {
		return state.RequireSkill(u.Name)
	}
	return state.RequireAgent(u.Key)
}

// sameBinding reports whether two units of one Harness can belong to the same
// binding; a unit without a record could belong to any.
func sameBinding(a, b *Unit) bool {
	if a.Record == nil || b.Record == nil {
		return true
	}
	return state.SettingsKey(a.Record.Settings) == state.SettingsKey(b.Record.Settings)
}

// Inventory is every unit of one lock, joined with this machine's ownership records.
type Inventory struct {
	Lock      *state.Lock
	Ownership *state.Ownership
	Units     []*Unit
	// ProjectRoot is the project of a project lock; empty for the global lock.
	ProjectRoot string
}

// AbsPath resolves a locked path: absolute as is, "~/" under home, anything
// else under the project root.
func AbsPath(lockPath, projectRoot, home string) string {
	switch {
	case filepath.IsAbs(lockPath):
		return lockPath
	case strings.HasPrefix(lockPath, "~/"):
		return filepath.Join(home, filepath.FromSlash(lockPath[2:]))
	default:
		return filepath.Join(projectRoot, filepath.FromSlash(lockPath))
	}
}

// NewInventory lists the units of lock. projectRoot is empty for the global lock.
func NewInventory(
	lock *state.Lock,
	ownership *state.Ownership,
	projectRoot, home string,
) *Inventory {
	inv := &Inventory{Lock: lock, Ownership: ownership, ProjectRoot: projectRoot}
	for _, locked := range lock.Agents {
		for _, artifact := range locked.Artifacts {
			base := Unit{
				Kind:          KindAgent,
				Name:          locked.Agent,
				Key:           locked.Key(),
				Source:        locked.Source,
				Origin:        locked.Origin,
				Harness:       artifact.Harness,
				Path:          AbsPath(artifact.Path, projectRoot, home),
				LockPath:      artifact.Path,
				Digest:        artifact.Digest,
				Requires:      artifact.Requires,
				RequiresKnown: artifact.Requires != nil,
			}
			inv.add(base, "")
		}
	}
	for _, locked := range lock.Skills {
		for _, harnessID := range locked.Harnesses {
			base := Unit{
				Kind:     KindSkill,
				Name:     locked.Skill,
				Key:      locked.Skill,
				Source:   locked.Source,
				Origin:   state.OriginDependency,
				Harness:  harnessID,
				Path:     AbsPath(locked.Path, projectRoot, home),
				LockPath: locked.Path,
				Digest:   locked.Digest,
			}
			inv.add(base, locked.Skill)
		}
	}
	return inv
}

// add appends one unit per matching ownership record, or one lock-only unit.
func (inv *Inventory) add(base Unit, skill string) {
	matched := false
	for i := range inv.Ownership.Records {
		record := inv.Ownership.Records[i]
		if record.Path != base.Path || record.Harness != base.Harness ||
			record.Skill != skill || record.ProjectRoot != inv.ProjectRoot {
			continue
		}
		unit := base
		unit.Record = &record
		unit.Digest = record.Digest
		inv.Units = append(inv.Units, &unit)
		matched = true
	}
	if !matched {
		unit := base
		inv.Units = append(inv.Units, &unit)
	}
}

// Action is what a plan does with one selected unit.
type Action string

// Plan actions. Refuse and Preserve change nothing.
const (
	// ActionDelete deletes the artifact and drops its lock and ownership state.
	ActionDelete Action = "delete"
	// ActionUnbind drops this binding; the artifact stays for its other users.
	ActionUnbind Action = "unbind"
	// ActionForget drops the state of an artifact that is already gone.
	ActionForget Action = "forget"
	// ActionRelease drops the lock entry of a reused Skill no locked Agent still
	// requires; agnthub did not install it, so its files stay.
	ActionRelease Action = "release"
	// ActionRefuse leaves an explicitly selected artifact that is unsafe to remove.
	ActionRefuse Action = "refuse"
	// ActionPreserve keeps a Dependency that cleanup cannot prove is safe to remove.
	ActionPreserve Action = "preserve"
)

// Changes reports whether applying the action changes disk or state.
func (a Action) Changes() bool {
	return a != ActionRefuse && a != ActionPreserve
}

// Entry is the decision for one selected unit.
type Entry struct {
	Unit   *Unit
	Action Action
	Reason string
	// KeptFor names the other users of an artifact that stays.
	KeptFor []string
	// Kept is the directory a removed Skill link pointed to, which stays.
	Kept string
	// Err reports an apply failure; nothing changed for this entry.
	Err error
}

// Plan is a removal decided before anything changes.
type Plan struct {
	Entries []*Entry
}

// Options tune planning.
type Options struct {
	// Cleanup marks Dependency cleanup: an Agent whose requirements are unknown
	// may need any Dependency, and unsafe units are preserved rather than refused.
	Cleanup bool
	// External names other locks that list a Skill directory; such a directory is
	// never deleted.
	External func(path string) []string
}

// Build decides every selected unit. Units that a remaining Agent requires, or
// whose artifact cannot be deleted safely, stay; that can keep further units
// required, so decisions repeat until stable.
func Build(inv *Inventory, selected []*Unit, opts Options) *Plan {
	removing := make(map[*Unit]bool, len(selected))
	for _, unit := range selected {
		removing[unit] = true
	}
	reasons := map[*Unit]string{}
	type pathState struct {
		exists bool
		err    error
	}
	disk := map[string]pathState{}
	for changed := true; changed; {
		changed = false
		for _, unit := range selected {
			if !removing[unit] {
				continue
			}
			if by := inv.requiredBy(unit, removing, opts.Cleanup); by != "" {
				removing[unit], reasons[unit], changed = false, "required by "+by, true
			}
		}
		for _, unit := range selected {
			if !removing[unit] || !unit.Owned() ||
				len(inv.keepers(unit.Path, removing, opts)) > 0 {
				continue
			}
			current, seen := disk[unit.Path]
			if !seen {
				current.exists, current.err = inv.check(unit.Path, removing)
				disk[unit.Path] = current
			}
			if current.err == nil {
				continue
			}
			for _, other := range selected {
				if removing[other] && other.Path == unit.Path {
					removing[other], reasons[other], changed = false, current.err.Error(), true
				}
			}
		}
	}
	plan := &Plan{}
	for _, unit := range selected {
		entry := &Entry{Unit: unit}
		switch {
		case !removing[unit] && opts.Cleanup:
			entry.Action, entry.Reason = ActionPreserve, reasons[unit]
		case !removing[unit]:
			entry.Action, entry.Reason = ActionRefuse, reasons[unit]
		case !unit.Owned():
			entry.Action = ActionRelease
			entry.Reason = "reused: agnthub did not install it, so its files stay"
		default:
			if keep := inv.keepers(unit.Path, removing, opts); len(keep) > 0 {
				entry.Action, entry.KeptFor = ActionUnbind, keep
				entry.Reason = "still used by " + strings.Join(keep, ", ")
			} else if disk[unit.Path].exists {
				entry.Action = ActionDelete
			} else {
				entry.Action, entry.Reason = ActionForget, "already absent"
			}
		}
		plan.Entries = append(plan.Entries, entry)
	}
	return plan
}

// requiredBy names a remaining Agent of the same Harness binding that requires
// unit, or returns "".
func (inv *Inventory) requiredBy(unit *Unit, removing map[*Unit]bool, cleanup bool) string {
	need := unit.requirement()
	for _, other := range inv.Units {
		if other.Kind != KindAgent || removing[other] || other.Key == unit.Key ||
			other.Harness != unit.Harness || !sameBinding(unit, other) {
			continue
		}
		if slices.Contains(other.Requires, need) {
			return other.Label()
		}
		if cleanup && !other.RequiresKnown {
			return other.Label() + ", whose lock entry predates recorded requirements"
		}
	}
	return ""
}

// keepers names everything that still uses path once the removing units are gone:
// other units of the lock, ownership records of other bindings or projects and,
// for Skills, other locks.
func (inv *Inventory) keepers(path string, removing map[*Unit]bool, opts Options) []string {
	var keep []string
	bindings := map[string]bool{}
	isSkill := false
	for _, unit := range inv.Units {
		if unit.Record != nil {
			bindings[unit.Record.Binding()] = true
		}
		if unit.Path != path {
			continue
		}
		isSkill = isSkill || unit.Kind == KindSkill
		if !removing[unit] {
			keep = append(keep, unit.Label())
		}
	}
	for _, record := range inv.Ownership.ForPath(path) {
		if bindings[record.Binding()] {
			continue
		}
		label := record.Harness
		if settings := state.SettingsKey(record.Settings); settings != "" {
			label += ", " + settings
		}
		if record.ProjectRoot != "" && record.ProjectRoot != inv.ProjectRoot {
			label += ", project " + record.ProjectRoot
		} else if record.ProjectRoot == "" && inv.ProjectRoot != "" {
			label += ", global scope"
		}
		keep = append(keep, "another installation ("+label+")")
	}
	if isSkill && opts.External != nil {
		for _, lockPath := range opts.External(path) {
			keep = append(keep, "the lock "+lockPath)
		}
	}
	return keep
}

// check reports whether path exists and, when it does, that every removing unit
// on it still matches its recorded digest.
func (inv *Inventory) check(path string, removing map[*Unit]bool) (bool, error) {
	exists := false
	for _, unit := range inv.Units {
		if unit.Path != path || !removing[unit] || !unit.Owned() {
			continue
		}
		present, err := unchanged(unit)
		if err != nil {
			return true, err
		}
		exists = exists || present
	}
	return exists, nil
}

// errModified reports a file whose content differs from what agnthub recorded.
var errModified = errors.New("modified since agnthub installed it; left untouched")

// unchanged reports whether the unit's artifact exists, failing when it was
// changed or replaced.
func unchanged(unit *Unit) (bool, error) {
	if unit.Kind == KindSkill {
		exists, err := skills.InstalledState(unit.Path, unit.Digest)
		return exists, skillError(err)
	}
	digest, exists, err := state.FileDigest(unit.Path)
	switch {
	case err != nil:
		return exists, fmt.Errorf("cannot verify: %w; left untouched", err)
	case exists && digest != unit.Digest:
		return true, errModified
	default:
		return exists, nil
	}
}

// Apply performs the plan's changes in inv's lock and ownership records, which
// the caller saves. Each artifact is verified again right before deletion; a
// failure leaves that artifact and its state untouched and is reported on its
// entries, while every other entry still applies.
func Apply(inv *Inventory, plan *Plan) error {
	failed := map[string]error{}
	kept := map[string]string{}
	for _, entry := range plan.Entries {
		path := entry.Unit.Path
		if entry.Action != ActionDelete {
			continue
		}
		if _, done := failed[path]; done {
			continue
		}
		failed[path] = nil
		var err error
		kept[path], err = remove(entry.Unit)
		failed[path] = err
	}
	applied := map[*Unit]bool{}
	var errs []error
	for _, entry := range plan.Entries {
		if !entry.Action.Changes() {
			continue
		}
		if err := failed[entry.Unit.Path]; err != nil && entry.Action == ActionDelete {
			entry.Err = err
			errs = append(errs, fmt.Errorf("%s: %w", entry.Unit.Label(), err))
			continue
		}
		entry.Kept = kept[entry.Unit.Path]
		applied[entry.Unit] = true
		if entry.Unit.Record != nil {
			inv.Ownership.Remove(entry.Unit.Record.Binding())
		}
	}
	for _, entry := range plan.Entries {
		unit := entry.Unit
		if !applied[unit] || !inv.lockEntryGone(unit, applied) {
			continue
		}
		if unit.Kind == KindSkill {
			inv.Lock.RemoveSkillHarness(unit.Name, unit.LockPath, unit.Harness)
		} else {
			inv.Lock.RemoveArtifact(unit.Key, unit.LockPath)
		}
	}
	return errors.Join(errs...)
}

// lockEntryGone reports whether every binding of unit's lock entry was removed.
func (inv *Inventory) lockEntryGone(unit *Unit, applied map[*Unit]bool) bool {
	for _, other := range inv.Units {
		if other.Kind == unit.Kind && other.Key == unit.Key && other.LockPath == unit.LockPath &&
			other.Harness == unit.Harness && !applied[other] {
			return false
		}
	}
	return true
}

// remove deletes one artifact after verifying it is still unchanged.
func remove(unit *Unit) (string, error) {
	if unit.Kind == KindSkill {
		target, err := skills.RemoveInstalled(unit.Path, unit.Digest)
		return target, skillError(err)
	}
	exists, err := unchanged(unit)
	if err != nil || !exists {
		return "", err
	}
	return "", os.Remove(unit.Path)
}

// skillError words a changed Skill like a changed Agent file, keeping the detail.
func skillError(err error) error {
	if !errors.Is(err, skills.ErrModified) {
		return err
	}
	if detail := strings.TrimPrefix(err.Error(), skills.ErrModified.Error()); detail != "" {
		return fmt.Errorf("%w%s", errModified, detail)
	}
	return errModified
}
