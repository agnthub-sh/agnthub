package cli

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/agnthub-sh/agnthub/install"
	"github.com/agnthub-sh/agnthub/skills"
	"github.com/agnthub-sh/agnthub/state"
)

// Plans print as a header (status, source, scope, Harnesses), a table of
// Agent | Harness | Action | Path rows sorted by Agent then Harness, the notes
// under it, refusals in a section of their own and a totals line. Only rows that
// change something are listed; a sentence counts the Agent × Harness pairs that
// were already correct. A note that singles out one Agent among several gets a
// [n] marker on that Agent's rows; any other note prints once with the rows it
// applies to. After a plan the user approved, the result is only the status line
// and the totals: the table was just shown.

// Row actions. Internal install actions map to these words.
const (
	rowCreate    = "create"
	rowUpdate    = "update"
	rowDelete    = "delete"
	rowUnchanged = "unchanged"
)

// notedRow is a table row whose notes are collected under the table.
type notedRow struct {
	cells []cell
	// name is the Agent or Skill the row installs; markers attach to cells[0].
	name    string
	harness string
	notes   []string
	// idle rows need no action: they are counted, not listed.
	idle bool
}

// refusal is one item that cannot be applied, with why.
type refusal struct {
	name, harness, reason string
}

// Sentences for the Agent × Harness or Skill × Harness pairs a table leaves out.
const (
	idleAgents = "agents were already correct in their harnesses, so no action was needed."
	idleAgent  = "agent was already correct in its harness, so no action was needed."
	idleSkills = "skills were already in place in their harnesses, so no action was needed."
	idleSkill  = "skill was already in place in its harness, so no action was needed."
)

// actionCell is an action's symbol and style in the Action column.
func (p *printer) actionCell(action string) cell {
	s := p.styles
	switch action {
	case rowCreate:
		return cell{text: "+ create", style: s.create}
	case rowUpdate:
		return cell{text: "~ update", style: s.update}
	case rowDelete:
		return cell{text: "- delete", style: s.remove}
	default:
		return cell{text: "= " + action, style: s.dim}
	}
}

// installRow maps a planned install item to a row action; "" for a blocking item.
func installRow(action install.Action) string {
	switch action {
	case install.ActionCreate:
		return rowCreate
	case install.ActionReplace:
		return rowUpdate
	case install.ActionUnchanged:
		return rowUnchanged
	default:
		return ""
	}
}

func (p *printer) humanPlan(payload *planJSON) {
	s := p.styles
	if payload.previewed && (payload.Status == statusApplied || payload.Status == statusFailed) {
		p.statusLine(payload)
		p.humanSkillRuns(payload)
		p.totals(payload, 0)
		return
	}
	p.planHeader(payload)
	p.skillTables(payload)
	p.humanSkillRuns(payload)
	rows, refused, kept := p.itemRows(payload)
	visible := slices.ContainsFunc(rows, func(row notedRow) bool { return !row.idle })
	if payload.Status == statusFailed {
		fmt.Fprintln(p.w, s.heading.Render("Agents (not written)"))
	} else if len(payload.Skills) > 0 && visible {
		fmt.Fprintln(p.w, s.heading.Render("Agents"))
	}
	if len(rows) == 0 && len(refused) == 0 {
		fmt.Fprintln(p.w, "  nothing to install")
	}
	p.notedTable([]string{"AGENT", "HARNESS", "ACTION", "PATH"}, rows, kept, idleAgent, idleAgents)
	p.refusals("Refused", "AGENT", refused)
	skipped := make([]refusal, 0, len(payload.Skipped))
	for _, item := range payload.Skipped {
		skipped = append(skipped, refusal{item.Agent, item.Harness, item.Reason})
	}
	p.refusals("Skipped (not written)", "AGENT", skipped)
	if payload.Changes != nil {
		p.skillChanges(payload.Changes.Skills)
	}
	p.totals(payload, len(refused))
}

// statusLine is the first header line: status, source or lock, and scope.
func (p *printer) statusLine(payload *planJSON) {
	s := p.styles
	icon := p.emoji("📋")
	switch payload.Status {
	case statusApplied, statusUpToDate:
		icon = p.emoji("✅")
	case statusRefused, statusFailed:
		icon = p.emoji("❌")
	}
	if payload.Changes != nil {
		fmt.Fprintf(p.w, "%s%s · %s scope · lock %s\n", icon,
			s.title.Render(lifecycleTitle(payload.Changes.Command, payload.Status)),
			payload.Scope, p.path(payload.Changes.Lock))
		return
	}
	fmt.Fprintf(p.w, "%s%s · %s · %s scope\n", icon,
		s.title.Render(titleStatus(payload.Status)), sourceLabel(payload.Source),
		payload.Scope)
}

// planHeader prints the status line, the sources and the selected Harnesses with
// their detection, Settings and destination notes.
func (p *printer) planHeader(payload *planJSON) {
	s := p.styles
	p.statusLine(payload)
	if payload.Changes != nil {
		p.sources(payload.Changes)
	}
	if len(payload.Harnesses) > 0 {
		parts := make([]string, 0, len(payload.Harnesses))
		for _, h := range payload.Harnesses {
			part := h.ID
			for _, setting := range h.Settings {
				part += " " + setting.Key + "=" + setting.Value +
					s.dim.Render(" ("+setting.Source+")")
			}
			if !h.Detected {
				part += s.dim.Render(" (not detected)")
			}
			parts = append(parts, part)
		}
		p.paragraph(2, 2, s.dim.Render("harnesses:")+" "+strings.Join(parts, ", "))
	}
	// Destination labels describe a whole Harness (e.g. omp's shared project
	// directory); the plain scope word repeats the header and is dropped.
	var seen []string
	for _, item := range payload.Items {
		label := destinationNote(item.Destination, payload.Scope)
		if label == "" || slices.Contains(seen, item.Harness) {
			continue
		}
		seen = append(seen, item.Harness)
		p.paragraph(2, 2, s.dim.Render(item.Harness+": "+label))
	}
}

// destinationNote is a destination label without the scope it repeats.
func destinationNote(label, scope string) string {
	for _, word := range []string{scope, "project", "user"} {
		if label == word {
			return ""
		}
		label = strings.TrimPrefix(label, word+", ")
	}
	return label
}

// sources lists the sources of an update, check or restore and the revisions
// their refs resolve to now.
func (p *printer) sources(report *lifecycleReport) {
	var labels []string
	for _, entry := range report.Agents {
		label := sourceLabel(entry.Source)
		if entry.Revision != "" && entry.Revision != entry.Source.Revision {
			label += " → " + shortRevision(entry.Revision)
		}
		if !slices.Contains(labels, label) {
			labels = append(labels, label)
		}
	}
	if len(labels) > 0 {
		p.paragraph(2, 2, p.styles.dim.Render("sources:")+" "+strings.Join(labels, ", "))
	}
}

// itemRows turns planned items into rows and refusals. For an update or restore
// it adds what the lock comparison knows: local drift, why content changes, and
// lines for locked files the plan no longer produces (which stay on disk).
func (p *printer) itemRows(payload *planJSON) ([]notedRow, []refusal, []string) {
	artifacts := map[string]*artifactReport{}
	if payload.Changes != nil {
		for i := range payload.Changes.Agents {
			entry := &payload.Changes.Agents[i]
			for j := range entry.Artifacts {
				artifacts[entry.Agent+"\x00"+entry.Artifacts[j].Path] = &entry.Artifacts[j]
			}
		}
	}
	rows := make([]notedRow, 0, len(payload.Items))
	var refused []refusal
	for i := range payload.Items {
		item := &payload.Items[i]
		action := installRow(install.Action(item.Action))
		if action == "" {
			refused = append(refused, refusal{item.Agent, item.Harness, item.Reason})
			continue
		}
		row := p.row(action, item.Agent, item.Harness, item.Path)
		row.idle = action == rowUnchanged
		if item.Origin == string(state.OriginDependency) {
			row.notes = append(row.notes, fmt.Sprintf("dependency of %s, from %s",
				strings.Join(item.RequiredBy, ", "), sourceLabel(item.Source)))
		}
		row.notes = append(row.notes, item.Notes...)
		if art := artifacts[item.Agent+"\x00"+item.Path]; art != nil {
			row.notes = append(row.notes, artifactNotes(art)...)
		}
		rows = append(rows, row)
	}
	var kept []string
	if payload.Changes != nil {
		for _, entry := range payload.Changes.Agents {
			for _, art := range entry.Artifacts {
				if art.Change == changeRemoved {
					kept = append(kept, keptLine(entry, art.Harness, p.path(art.Path)))
				}
			}
		}
	}
	return rows, refused, kept
}

// keptLine says why a locked file the plan no longer produces stays installed.
func keptLine(entry agentReport, harnessID, path string) string {
	why := "the source no longer provides it"
	if entry.Status == agentNotRequired {
		why = "no installed agent requires it any more"
	}
	return fmt.Sprintf("%s for %s: %s; %s stays installed", entry.Agent, harnessID, why, path)
}

// row is a plan row for one Agent artifact.
func (p *printer) row(action, name, harness, path string) notedRow {
	return notedRow{
		cells: []cell{
			plainCell(name), plainCell(harness), p.actionCell(action), plainCell(p.path(path)),
		},
		name: name, harness: harness,
	}
}

// artifactNotes explain a locked artifact's change and local state.
func artifactNotes(art *artifactReport) []string {
	var notes []string
	switch art.Change {
	case changeContent:
		notes = append(notes, "content changed")
	case changeConverter, changeVariant:
		if art.Reason != "" {
			notes = append(notes, art.Reason)
		}
	}
	if art.Local != "" && art.Local != localOK {
		notes = append(notes, "local: "+art.Local)
	}
	for _, binding := range art.Bindings {
		notes = append(notes, "bound to "+formatSettings(binding))
	}
	return notes
}

// byNameAndHarness orders rows by Agent (or Skill), then Harness.
func byNameAndHarness(a, b notedRow) int {
	return cmp.Or(strings.Compare(a.name, b.name), strings.Compare(a.harness, b.harness))
}

// notedTable prints the rows that change something, sorted by name then Harness
// (the name is the first column, the path the fourth), with their notes collapsed
// under the table and the extra note lines, then a sentence counting the idle
// rows it left out: one, or many. Notes of idle rows are dropped.
func (p *printer) notedTable(headers []string, rows []notedRow, extra []string, one, many string) {
	idle := 0
	shown := make([]notedRow, 0, len(rows))
	for _, row := range rows {
		if row.idle {
			idle++
			continue
		}
		shown = append(shown, row)
	}
	slices.SortStableFunc(shown, byNameAndHarness)
	if len(shown) > 0 {
		p.renderNoted(headers, shown)
	}
	for _, line := range extra {
		p.paragraph(2, 2, p.styles.dim.Render("· "+line))
	}
	switch {
	case idle == 1:
		p.paragraph(2, 2, p.styles.dim.Render("1 "+one))
	case idle > 1:
		p.paragraph(2, 2, p.styles.dim.Render(strconv.Itoa(idle)+" "+many))
	}
}

// renderNoted prints rows with markers and the collapsed notes.
func (p *printer) renderNoted(headers []string, rows []notedRow) {
	type noteUse struct {
		text  string
		rows  []int
		names []string
		hs    []string
	}
	var uses []*noteUse
	var names, harnesses []string
	for i := range rows {
		row := &rows[i]
		if !slices.Contains(names, row.name) {
			names = append(names, row.name)
		}
		if !slices.Contains(harnesses, row.harness) {
			harnesses = append(harnesses, row.harness)
		}
		for _, note := range row.notes {
			k := slices.IndexFunc(uses, func(u *noteUse) bool { return u.text == note })
			if k < 0 {
				uses = append(uses, &noteUse{text: note})
				k = len(uses) - 1
			}
			use := uses[k]
			if !slices.Contains(use.rows, i) {
				use.rows = append(use.rows, i)
			}
			if !slices.Contains(use.names, row.name) {
				use.names = append(use.names, row.name)
			}
			if !slices.Contains(use.hs, row.harness) {
				use.hs = append(use.hs, row.harness)
			}
		}
	}
	marks := make([][]string, len(rows))
	var footnotes, general []string
	for _, use := range uses {
		if len(names) > 1 && len(use.names) == 1 {
			n := strconv.Itoa(len(footnotes) + 1)
			for _, i := range use.rows {
				marks[i] = append(marks[i], n)
			}
			footnotes = append(footnotes, "["+n+"] "+use.names[0]+": "+use.text)
			continue
		}
		line := use.text
		if len(use.rows) < len(rows) {
			line += fmt.Sprintf(" (%d of %d rows: %s)", len(use.rows), len(rows),
				appliesTo(use.names, names, use.hs, harnesses))
		}
		general = append(general, line)
	}
	t := &table{headers: headers, shrink: 3, wrap: -1}
	for i := range rows {
		cells := slices.Clone(rows[i].cells)
		if len(marks[i]) > 0 {
			cells[0].text += " [" + strings.Join(marks[i], ",") + "]"
		}
		t.rows = append(t.rows, cells)
	}
	p.render(t)
	for _, line := range general {
		p.paragraph(2, 2, p.styles.dim.Render("· "+line))
	}
	for _, line := range footnotes {
		p.paragraph(2, 4, p.styles.dim.Render(line))
	}
}

// appliesTo names the Agents and Harnesses a note covers, leaving out a side it
// covers entirely.
func appliesTo(names, allNames, hs, allHarnesses []string) string {
	switch {
	case len(names) == len(allNames):
		return limited(hs)
	case len(hs) == len(allHarnesses):
		return limited(names)
	default:
		return limited(names) + " on " + limited(hs)
	}
}

// limited joins up to seven names and counts the rest.
func limited(names []string) string {
	const most = 7
	if len(names) <= most {
		return strings.Join(names, ", ")
	}
	return strings.Join(names[:most], ", ") + fmt.Sprintf(" and %d more", len(names)-most)
}

// refusals prints items that cannot be applied, and why, in a section of their
// own, sorted by name then Harness.
func (p *printer) refusals(title, nameHeader string, items []refusal) {
	if len(items) == 0 {
		return
	}
	s := p.styles
	items = slices.Clone(items)
	slices.SortStableFunc(items, func(a, b refusal) int {
		return cmp.Or(strings.Compare(a.name, b.name), strings.Compare(a.harness, b.harness))
	})
	fmt.Fprintf(p.w, "%s%s\n", p.emoji("❌"), s.blocked.Render(fmt.Sprintf("%s (%d)", title,
		len(items))))
	t := &table{headers: []string{nameHeader, "HARNESS", "REASON"}, shrink: -1, wrap: 2}
	for _, item := range items {
		t.rows = append(t.rows, []cell{
			{text: item.name, style: s.title},
			plainCell(item.harness),
			{text: item.reason, style: s.reason},
		})
	}
	p.render(t)
}

// totals prints the counts of the plan's Agent and Skill changes; unchanged
// items are counted by the sentence under the table instead.
func (p *printer) totals(payload *planJSON, refused int) {
	s := p.styles
	count := map[string]int{}
	for _, item := range payload.Items {
		count[installRow(install.Action(item.Action))]++
	}
	applied := payload.Status == statusApplied
	parts := []string{
		p.total(count[rowCreate], "to create", "created", applied, s.create),
		p.total(count[rowUpdate], "to update", "updated", applied, s.update),
		p.total(0, "to delete", "deleted", applied, s.remove),
	}
	if refused > 0 {
		parts = append(parts, s.blocked.Render(fmt.Sprintf("%d refused", refused)))
	}
	if n := len(payload.Skipped); n > 0 {
		parts = append(parts, s.blocked.Render(fmt.Sprintf("%d skipped", n)))
	}
	line := strings.Join(parts, " · ")
	if skillsLine := skillSummaryLine(payload.SkillSummary); skillsLine != "" {
		line += " · skills: " + skillsLine
	}
	fmt.Fprintln(p.w, line)
}

// total is one count of the totals line: dim when zero.
func (p *printer) total(n int, planned, done string, applied bool, style lipgloss.Style) string {
	word := planned
	if applied {
		word = done
	}
	if n == 0 {
		style = p.styles.dim
	}
	return style.Render(fmt.Sprintf("%d %s", n, word))
}

// skillTables prints the Skills to install as a table, refused ones as
// refusals, and the Skills an interactive user left out. Reused and unchanged
// Skills are counted, not listed.
func (p *printer) skillTables(payload *planJSON) {
	if len(payload.Skills) == 0 && len(payload.SkippedSkills) == 0 {
		return
	}
	s := p.styles
	rows := make([]notedRow, 0, len(payload.Skills))
	var refused []refusal
	for i := range payload.Skills {
		item := &payload.Skills[i]
		decision := skills.Decision(item.Decision)
		if decision.Blocking() {
			refused = append(refused, refusal{item.Skill, item.Harness, item.Reason})
			continue
		}
		row := notedRow{
			cells: []cell{
				plainCell(item.Skill),
				plainCell(item.Harness),
				{text: "+ install", style: s.create},
				plainCell(p.path(item.Path)),
			},
			name: item.Skill, harness: item.Harness,
			idle: decision != skills.DecisionInstall,
		}
		row.notes = append(row.notes, fmt.Sprintf("%s from %s, required by %s",
			item.Requirement, item.Source, strings.Join(item.RequiredBy, ", ")))
		if item.Version != "" {
			row.notes = append(row.notes, "version "+item.Version)
		}
		if item.Location != "" {
			row.notes = append(row.notes, item.Location)
		}
		if len(item.Command) > 0 {
			row.notes = append(row.notes, "runs "+skills.Display(item.Command[0], item.Command[1:]))
		}
		if item.Reason != "" {
			row.notes = append(row.notes, item.Reason)
		}
		rows = append(rows, row)
	}
	fmt.Fprintln(p.w, s.heading.Render("Skills"))
	p.notedTable([]string{"SKILL", "HARNESS", "ACTION", "PATH"}, rows, nil, idleSkill, idleSkills)
	p.refusals("Refused skills", "SKILL", refused)
	skipped := make([]refusal, 0, len(payload.SkippedSkills))
	for _, item := range payload.SkippedSkills {
		skipped = append(skipped, refusal{item.Skill, item.Harness, item.Reason})
	}
	p.refusals("Skipped skills (not installed)", "SKILL", skipped)
}

// skillChanges lists Skills an update newly requires or no longer requires.
func (p *printer) skillChanges(changes []skillChange) {
	if len(changes) == 0 {
		return
	}
	fmt.Fprintln(p.w, p.styles.heading.Render("Skill dependencies"))
	for _, change := range changes {
		marker, style := "+", p.styles.create
		if change.Change == changeRemoved {
			marker, style = "-", p.styles.dim
		}
		fmt.Fprintf(p.w, "  %s %s for %s %s\n", style.Render(marker), change.Skill,
			change.Harness, p.styles.dim.Render("("+change.Change+")"))
	}
}

// path is a path as human output shows it.
func (p *printer) path(path string) string {
	return displayPath(p.root, p.home, path)
}
