package install

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/heynemann/agnthub/internal/harness"
	"github.com/heynemann/agnthub/internal/skills"
	"github.com/heynemann/agnthub/internal/state"
)

// Result reports what Apply actually changed.
type Result struct {
	Applied []Item
	// Failed is the item whose write failed, when any.
	Failed *Item
}

// Apply writes every non-blocking item, then records lock and ownership receipts.
// It refuses a plan with blocking items. plan.Skills must already be installed
// and verified (skills.Applier): they are recorded, never run, here. When a write
// fails, receipts are still recorded for the artifacts already written so state
// matches disk.
func (p *Planner) Apply(plan *Plan, projectRoot string, now time.Time) (*Result, error) {
	if err := plan.Validate(); err != nil {
		return nil, err
	}
	result := &Result{}
	var writeErr error
	for i := range plan.Items {
		item := plan.Items[i]
		if item.Action == ActionCreate || item.Action == ActionReplace {
			if err := state.WriteFileAtomic(item.Path, item.Artifact.Content, 0o644); err != nil {
				result.Failed = &plan.Items[i]
				writeErr = fmt.Errorf("write %s: %w", item.Path, err)
				break
			}
		}
		result.Applied = append(result.Applied, item)
	}
	for _, skill := range plan.Skills {
		p.recordSkill(plan, skill, projectRoot, now)
	}
	for _, item := range result.Applied {
		p.record(plan, item, projectRoot, now)
	}
	if err := p.Lock.Save(p.LockPath); err != nil {
		return result, fmt.Errorf("save lock: %w", err)
	}
	if err := p.Ownership.Save(); err != nil {
		return result, fmt.Errorf("save ownership records: %w", err)
	}
	return result, writeErr
}

func (p *Planner) record(plan *Plan, item Item, projectRoot string, now time.Time) {
	digest := state.Digest(item.Artifact.Content)
	lockPath := item.Path
	if plan.Scope == harness.ScopeProject {
		if rel, err := filepath.Rel(projectRoot, item.Path); err == nil {
			lockPath = filepath.ToSlash(rel)
		}
	}
	variant := state.VariantNative
	if item.Artifact.Generated {
		variant = state.VariantGenerated
	}
	p.Lock.Upsert(item.Source, item.Agent, item.Origin, state.LockedArtifact{
		Harness:      string(item.Harness),
		Variant:      variant,
		SourcePath:   item.SourcePath,
		SourceDigest: item.SourceDigest,
		Converter:    item.Artifact.Converter,
		Path:         lockPath,
		Digest:       digest,
	})
	record := state.Record{
		Path:        item.Path,
		Digest:      digest,
		Harness:     string(item.Harness),
		Scope:       string(plan.Scope),
		Settings:    item.Settings,
		Destination: item.Destination.Label,
		Source:      item.Source.Kind + ":" + item.Source.Locator,
		Agent:       item.Agent,
		Origin:      item.Origin,
		InstalledAt: now.UTC(),
	}
	if plan.Scope == harness.ScopeProject {
		record.ProjectRoot = projectRoot
	}
	// A companion reinstall never downgrades a direct installation to dependency.
	for _, previous := range p.Ownership.ForPath(item.Path) {
		if previous.Binding() != record.Binding() {
			continue
		}
		if previous.Origin == state.OriginDirect {
			record.Origin = state.OriginDirect
		}
		if item.Action == ActionUnchanged {
			record.InstalledAt = previous.InstalledAt
		}
	}
	p.Ownership.Put(record)
}

// recordSkill locks a verified Skill and records ownership only for a directory
// agnthub's backend installed; reused Skills stay unowned.
func (p *Planner) recordSkill(plan *Plan, item skills.Item, projectRoot string, now time.Time) {
	if item.Blocking() {
		return
	}
	mode := state.SkillReused
	if item.Decision == skills.DecisionInstall || item.Decision == skills.DecisionUnchanged {
		mode = state.SkillInstalled
	}
	p.Lock.UpsertSkill(state.LockedSkill{
		Skill:   item.Skill,
		Source:  skills.LockedSource(item.Source, item.Revision),
		Version: item.Version,
		Digest:  item.Digest,
		Path:    portablePath(item.Path, projectRoot, plan.Home, plan.Scope),
		Scope:   string(item.Scope),
		Mode:    mode,
	}, string(item.Harness))
	if mode != state.SkillInstalled {
		return
	}
	record := state.Record{
		Path:        item.Path,
		Digest:      item.Digest,
		Harness:     string(item.Harness),
		Scope:       string(item.Scope),
		Settings:    item.Settings,
		Destination: item.Location,
		Source:      skills.SourceKey(item.Source),
		Skill:       item.Skill,
		Origin:      state.OriginDependency,
		InstalledAt: now.UTC(),
	}
	if plan.Scope == harness.ScopeProject {
		record.ProjectRoot = projectRoot
	}
	for _, previous := range p.Ownership.ForPath(item.Path) {
		if previous.Binding() == record.Binding() && item.Decision == skills.DecisionUnchanged {
			record.InstalledAt = previous.InstalledAt
		}
	}
	p.Ownership.Put(record)
}

// portablePath keeps a project lock shareable: project paths are relative, home
// paths start with "~/", and global locks keep absolute paths.
func portablePath(path, projectRoot, home string, scope harness.Scope) string {
	if scope == harness.ScopeGlobal {
		return path
	}
	if rel, err := filepath.Rel(projectRoot, path); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	if home != "" {
		if rel, err := filepath.Rel(home, path); err == nil && !strings.HasPrefix(rel, "..") {
			return "~/" + filepath.ToSlash(rel)
		}
	}
	return path
}
