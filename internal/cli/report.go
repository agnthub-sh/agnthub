package cli

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/heynemann/agnthub/install"
	"github.com/heynemann/agnthub/skills"
	"github.com/heynemann/agnthub/state"
)

// Locked Agent statuses reported by check, update and restore.
const (
	agentUpToDate        = "up-to-date"
	agentUpdateAvailable = "update-available"
	agentUnavailable     = "unavailable"
	agentNotRequired     = "no-longer-required"
	agentNewDependency   = "new-dependency"
)

// Artifact changes between the lock and freshly resolved content.
const (
	changeNone      = "unchanged"
	changeContent   = "content"
	changeVariant   = "variant"
	changeConverter = "converter"
	changeRemoved   = "removed"
	changeBlocked   = "blocked"
	changeAdded     = "added"
)

// localOK is the local state of an artifact whose bytes match the lock; see
// artifactStatus for the others.
const localOK = "ok"

// lifecycleReport compares locked installations with resolved content.
type lifecycleReport struct {
	Command string         `json:"command"`
	Lock    string         `json:"lock"`
	Agents  []agentReport  `json:"agents"`
	Skills  []skillChange  `json:"skills,omitempty"`
	Summary map[string]int `json:"summary"`
}

type agentReport struct {
	Agent  string     `json:"agent"`
	Source sourceJSON `json:"source"`
	Origin string     `json:"origin"`
	Status string     `json:"status"`
	// Revision is the resolved revision of the recorded ref, when it was resolved.
	Revision   string           `json:"revision,omitempty"`
	RequiredBy []string         `json:"requiredBy,omitempty"`
	Drift      bool             `json:"drift"`
	Error      string           `json:"error,omitempty"`
	Artifacts  []artifactReport `json:"artifacts"`
}

type artifactReport struct {
	Harness    string              `json:"harness"`
	Path       string              `json:"path"`
	Variant    string              `json:"variant,omitempty"`
	NewVariant string              `json:"newVariant,omitempty"`
	Change     string              `json:"change"`
	Local      string              `json:"local,omitempty"`
	Bindings   []map[string]string `json:"bindings"`
	Reason     string              `json:"reason,omitempty"`
}

type skillChange struct {
	Skill   string `json:"skill"`
	Harness string `json:"harness"`
	Change  string `json:"change"`
}

// buildReport compares the included locked Agents, and every locked Agent the
// plan touches, with the planned items. allSelected reports Skills the plan no
// longer requires only when every locked Agent was planned.
func (l *lifecycle) buildReport(
	command string,
	lp *lifecyclePlan,
	include func(*state.LockedAgent) bool,
	allSelected bool,
) *lifecycleReport {
	report := &lifecycleReport{
		Command: command, Lock: l.displayLockPath(), Agents: []agentReport{},
		Summary: map[string]int{},
	}
	byPath := make(map[string]*install.Item, len(lp.plan.Items))
	for i := range lp.plan.Items {
		byPath[lp.plan.Items[i].Path] = &lp.plan.Items[i]
	}
	matched := map[string]bool{}
	lock := l.planner.Lock
	for i := range lock.Agents {
		locked := &lock.Agents[i]
		touched := false
		for _, artifact := range locked.Artifacts {
			_, touched = byPath[l.app.artifactPath(l.scope, artifact.Path)]
			if touched {
				break
			}
		}
		if !include(locked) && !touched {
			continue
		}
		entry := l.lockedReport(locked, lp, byPath, matched)
		report.Agents = append(report.Agents, entry)
	}
	report.Agents = append(report.Agents, newDependencies(lp.plan.Items, matched)...)
	for _, entry := range report.Agents {
		report.Summary[entry.Status]++
		if entry.Drift {
			report.Summary["drift"]++
		}
	}
	report.Skills = skillChanges(lock, lp, allSelected)
	return report
}

func (l *lifecycle) displayLockPath() string {
	if l.scope == "project" {
		return state.LockFileName
	}
	return l.lockPath
}

func (l *lifecycle) lockedReport(
	locked *state.LockedAgent,
	lp *lifecyclePlan,
	byPath map[string]*install.Item,
	matched map[string]bool,
) agentReport {
	entry := agentReport{
		Agent: locked.Agent, Source: sourceJSON(locked.Source), Origin: string(locked.Origin),
		Artifacts: []artifactReport{},
	}
	unavailable := lp.unavailable[locked.Key()]
	if unavailable == nil && locked.Origin != state.OriginDirect {
		if dependent := l.unavailableDependent(lp, locked); dependent != "" {
			unavailable = fmt.Errorf("not checked: %s requires it and could not be resolved",
				dependent)
		}
	}
	if unavailable != nil {
		entry.Error = unavailable.Error()
	}
	changed, removed := false, 0
	for _, artifact := range locked.Artifacts {
		path := l.app.artifactPath(l.scope, artifact.Path)
		row := artifactReport{
			Harness: artifact.Harness, Path: path, Variant: artifact.Variant,
			Local:    artifactStatus(path, artifact.Digest),
			Bindings: l.bindingsOf(path, artifact.Harness),
		}
		entry.Drift = entry.Drift || row.Local != localOK
		item, ok := byPath[path]
		if ok && (item.Agent != locked.Agent || string(item.Harness) != artifact.Harness) {
			ok = false
		}
		switch {
		case unavailable != nil:
			row.Change = changeNone
		case !ok:
			row.Change = changeRemoved
			removed++
		default:
			matched[path] = true
			row.Change, row.NewVariant, row.Reason = artifactChange(artifact, item)
			if item.Source.Revision != "" {
				entry.Revision = item.Source.Revision
			}
			if item.Origin == state.OriginDependency {
				entry.RequiredBy = item.RequiredBy
			}
		}
		changed = changed || row.Change != changeNone
		entry.Artifacts = append(entry.Artifacts, row)
	}
	switch {
	case unavailable != nil:
		entry.Status = agentUnavailable
	case locked.Origin != state.OriginDirect && removed == len(locked.Artifacts):
		entry.Status = agentNotRequired
	case changed:
		entry.Status = agentUpdateAvailable
	default:
		entry.Status = agentUpToDate
	}
	return entry
}

// unavailableDependent names an unresolved locked Agent that requires the
// companion locked, or may require it because its lock predates recorded
// requirements; "" when no unresolved Agent requires it.
func (l *lifecycle) unavailableDependent(lp *lifecyclePlan, locked *state.LockedAgent) string {
	edge := state.RequireAgent(locked.Key())
	for i := range l.planner.Lock.Agents {
		dependent := &l.planner.Lock.Agents[i]
		if lp.unavailable[dependent.Key()] == nil {
			continue
		}
		for _, artifact := range dependent.Artifacts {
			if artifact.Requires == nil || slices.Contains(artifact.Requires, edge) {
				return dependent.Agent
			}
		}
	}
	return ""
}

// artifactChange classifies how a planned item differs from its locked artifact.
func artifactChange(artifact state.LockedArtifact, item *install.Item) (string, string, string) {
	if item.Action.Blocking() {
		return changeBlocked, "", item.Reason
	}
	variant := state.VariantNative
	if item.Artifact.Generated {
		variant = state.VariantGenerated
	}
	switch {
	case variant != artifact.Variant || item.SourcePath != artifact.SourcePath:
		return changeVariant, variant, fmt.Sprintf("%s %s → %s %s",
			artifact.Variant, artifact.SourcePath, variant, item.SourcePath)
	case state.Digest(item.Artifact.Content) == artifact.Digest:
		return changeNone, "", ""
	case item.Artifact.Converter != artifact.Converter:
		return changeConverter, "", fmt.Sprintf("converter %s → %s",
			artifact.Converter, item.Artifact.Converter)
	default:
		return changeContent, "", ""
	}
}

// newDependencies reports planned companions the lock does not record yet.
func newDependencies(items []install.Item, matched map[string]bool) []agentReport {
	var reports []agentReport
	for i := range items {
		item := &items[i]
		if matched[item.Path] {
			continue
		}
		k := slices.IndexFunc(reports, func(r agentReport) bool {
			return r.Agent == item.Agent && r.Source.Locator == item.Source.Locator
		})
		if k < 0 {
			reports = append(reports, agentReport{
				Agent: item.Agent, Source: sourceJSON(item.Source), Origin: string(item.Origin),
				Status: agentNewDependency, Revision: item.Source.Revision,
				RequiredBy: item.RequiredBy, Artifacts: []artifactReport{},
			})
			k = len(reports) - 1
		}
		row := artifactReport{
			Harness: string(item.Harness), Path: item.Path, Change: changeAdded,
			Bindings: []map[string]string{}, Reason: item.Reason,
		}
		if item.Action.Blocking() {
			row.Change = changeBlocked
		}
		if item.Path != "" {
			row.NewVariant = state.VariantNative
			if item.Artifact.Generated {
				row.NewVariant = state.VariantGenerated
			}
		}
		if item.Origin == state.OriginDirect {
			reports[k].Status = agentUpdateAvailable
		}
		reports[k].Artifacts = append(reports[k].Artifacts, row)
	}
	return reports
}

// skillChanges lists Skills newly required by the plan and, when every locked
// Agent was planned, locked Skills it no longer requires.
func skillChanges(lock *state.Lock, lp *lifecyclePlan, allSelected bool) []skillChange {
	locked := map[string]bool{}
	for _, skill := range lock.Skills {
		for _, h := range skill.Harnesses {
			locked[skill.Skill+"|"+h] = true
		}
	}
	var changes []skillChange
	for _, key := range slices.Sorted(maps.Keys(lp.required)) {
		if !locked[key] {
			skill, h, _ := strings.Cut(key, "|")
			changes = append(changes, skillChange{Skill: skill, Harness: h, Change: changeAdded})
		}
	}
	if !allSelected || len(lp.unavailable) > 0 {
		return changes
	}
	for _, skill := range lock.Skills {
		for _, h := range skill.Harnesses {
			if !lp.required[skill.Skill+"|"+h] {
				changes = append(changes, skillChange{
					Skill: skill.Skill, Harness: h, Change: changeRemoved,
				})
			}
		}
	}
	return changes
}

func (l *lifecycle) bindingsOf(path, harnessID string) []map[string]string {
	bindings := []map[string]string{}
	for _, record := range l.planner.Ownership.ForPath(path) {
		if record.Harness == harnessID && len(record.Settings) > 0 {
			bindings = append(bindings, record.Settings)
		}
	}
	return bindings
}

// pending reports whether applying the plan would change anything.
func (r *lifecycleReport) pending(plan *install.Plan) bool {
	for _, entry := range r.Agents {
		if entry.Status == agentUpdateAvailable || entry.Status == agentNewDependency {
			return true
		}
	}
	for _, item := range plan.Items {
		if item.Action != install.ActionUnchanged {
			return true
		}
	}
	return slices.ContainsFunc(plan.Skills, func(item skills.Item) bool {
		return item.Decision != skills.DecisionReuse && item.Decision != skills.DecisionUnchanged
	})
}

// humanReport prints locked Agents with their status, changes, local drift and
// recorded bindings.
func (p *printer) humanReport(report *lifecycleReport) {
	s := p.styles
	fmt.Fprintln(p.w, s.heading.Render("  Locked agents"))
	if len(report.Agents) == 0 {
		fmt.Fprintln(p.w, "    none")
	}
	for _, entry := range report.Agents {
		origin := entry.Origin
		if len(entry.RequiredBy) > 0 {
			origin = "dependency of " + strings.Join(entry.RequiredBy, ", ")
		}
		from := sourceLabel(entry.Source)
		if entry.Revision != "" && entry.Revision != entry.Source.Revision {
			from += " → " + shortRevision(entry.Revision)
		}
		status := strings.ReplaceAll(entry.Status, "-", " ")
		switch entry.Status {
		case agentUnavailable:
			status = s.blocked.Render(status)
		case agentUpToDate:
			status = s.unchanged.Render(status)
		default:
			status = s.create.Render(status)
		}
		fmt.Fprintf(p.w, "    %s  %s  %s\n", s.title.Render(entry.Agent), status,
			s.dim.Render("from "+from+" · "+origin))
		if entry.Error != "" {
			fmt.Fprintln(p.w, "      "+s.blocked.Render(entry.Error))
		}
		for _, row := range entry.Artifacts {
			change := row.Change
			if row.NewVariant != "" && row.Change == changeVariant {
				change = "variant → " + row.NewVariant
			}
			variant := row.Variant
			if variant == "" {
				variant = row.NewVariant
			}
			line := fmt.Sprintf("      %-9s %-10s %-18s %s", row.Harness, variant, change,
				row.Path)
			if row.Local != "" && row.Local != localOK {
				line += "  " + s.blocked.Render("local: "+row.Local)
			}
			for _, binding := range row.Bindings {
				line += " [" + formatSettings(binding) + "]"
			}
			fmt.Fprintln(p.w, strings.TrimRight(line, " "))
			if row.Reason != "" {
				fmt.Fprintln(p.w, "          "+s.dim.Render(row.Reason))
			}
		}
	}
	if len(report.Skills) > 0 {
		fmt.Fprintln(p.w, s.heading.Render("  Skill dependencies"))
		for _, change := range report.Skills {
			marker := "+"
			if change.Change == changeRemoved {
				marker = "-"
			}
			fmt.Fprintf(p.w, "    %s %s for %s (%s)\n", marker, change.Skill, change.Harness,
				change.Change)
		}
	}
	order := []string{
		agentUpToDate, agentUpdateAvailable, agentNewDependency, agentNotRequired,
		agentUnavailable, "drift",
	}
	parts := make([]string, 0, len(order))
	for _, key := range order {
		if n := report.Summary[key]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, strings.ReplaceAll(key, "-", " ")))
		}
	}
	if len(parts) > 0 {
		fmt.Fprintln(p.w, "  "+strings.Join(parts, " · "))
	}
}

// lifecycleTitle is the human headline of an update or restore plan.
func lifecycleTitle(command, status string) string {
	switch status {
	case statusApplied:
		if command == "restore" {
			return "Restored"
		}
		return "Updated"
	case statusRefused:
		return "Refused before writing"
	case statusFailed:
		return "Failed: skills not verified, no agent written"
	case statusUpToDate:
		if command == "restore" {
			return "Already restored: every locked file matches"
		}
		return "Up to date: nothing to update"
	default:
		if command == "restore" {
			return "Restore plan"
		}
		return "Update plan"
	}
}
