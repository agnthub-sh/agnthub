package cli

import (
	"fmt"
	"maps"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/heynemann/agnthub/internal/agent"
	"github.com/heynemann/agnthub/internal/harness"
	"github.com/heynemann/agnthub/internal/install"
	"github.com/heynemann/agnthub/internal/skills"
	"github.com/heynemann/agnthub/internal/state"
)

// Plan statuses reported in human and JSON output.
const (
	statusPlanned = "planned"
	statusRefused = "refused"
	statusApplied = "applied"
	// statusFailed reports a Skill backend or verification failure: no Agent was written.
	statusFailed = "failed"
)

// planView is a plan with the Harness context a preview shows.
type planView struct {
	Plan      *install.Plan
	Harnesses []harnessView
	// Skipped lists items an interactive user chose to leave out.
	Skipped []install.Item
	// SkippedSkills lists Skill items left out with their dependents.
	SkippedSkills []skills.Item
	// Outcome reports the Skill backend runs, observed effects and verification.
	Outcome *skills.Outcome
	// Report describes what an update or restore changes relative to the lock.
	Report *lifecycleReport
}

// harnessView is one selected Harness with detection and effective Settings.
type harnessView struct {
	ID       agent.HarnessID
	Name     string
	Detected bool
	Settings []harness.EffectiveSetting
}

// planStyles are terminal styles; colorprofile strips them when the output is
// not a terminal, so redirected human output stays plain text.
type planStyles struct {
	title, heading, dim, create, unchanged, blocked lipgloss.Style
}

func newPlanStyles() planStyles {
	return planStyles{
		title:     lipgloss.NewStyle().Bold(true),
		heading:   lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6")),
		dim:       lipgloss.NewStyle().Faint(true),
		create:    lipgloss.NewStyle().Foreground(lipgloss.Color("2")),
		unchanged: lipgloss.NewStyle().Faint(true),
		blocked:   lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("1")),
	}
}

func (a *App) printer(asJSON bool) *printer {
	if asJSON {
		return newPrinter(a.Stdout, true)
	}
	return newPrinter(colorprofile.NewWriter(a.Stdout, a.Env.Pairs()), false)
}

type planJSON struct {
	Status        string         `json:"status"`
	Scope         string         `json:"scope"`
	Source        sourceJSON     `json:"source"`
	Harnesses     []harnessJSON  `json:"harnesses"`
	Skills        []skillJSON    `json:"skills,omitempty"`
	SkippedSkills []skillJSON    `json:"skippedSkills,omitempty"`
	SkillRuns     []skillRunJSON `json:"skillRuns,omitempty"`
	// SkillEffects are changes observed in watched Skill locations during the runs.
	SkillEffects  []effectJSON   `json:"skillEffects,omitempty"`
	SkillFailures []string       `json:"skillFailures,omitempty"`
	Items         []itemJSON     `json:"items"`
	Skipped       []itemJSON     `json:"skipped,omitempty"`
	Summary       map[string]int `json:"summary"`
	SkillSummary  map[string]int `json:"skillSummary,omitempty"`
	// Changes compares an update or restore with the lock.
	Changes *lifecycleReport `json:"changes,omitempty"`
}

type skillJSON struct {
	Skill       string            `json:"skill"`
	Source      string            `json:"source"`
	Harness     string            `json:"harness"`
	Settings    map[string]string `json:"settings"`
	Requirement string            `json:"requirement"`
	RequiredBy  []string          `json:"requiredBy"`
	Decision    string            `json:"decision"`
	Path        string            `json:"path,omitempty"`
	Location    string            `json:"location,omitempty"`
	Scope       string            `json:"scope,omitempty"`
	Version     string            `json:"version,omitempty"`
	Revision    string            `json:"revision,omitempty"`
	Target      string            `json:"target,omitempty"`
	Command     []string          `json:"command,omitempty"`
	Shadowed    string            `json:"shadowed,omitempty"`
	Reason      string            `json:"reason,omitempty"`
}

type skillRunJSON struct {
	Skill     string   `json:"skill"`
	Path      string   `json:"path"`
	Command   []string `json:"command"`
	Harnesses []string `json:"harnesses"`
	ExitCode  int      `json:"exitCode"`
	Error     string   `json:"error,omitempty"`
	Output    string   `json:"output,omitempty"`
}

type effectJSON struct {
	Path   string `json:"path"`
	Change string `json:"change"`
}

func newSkillJSON(item *skills.Item) skillJSON {
	entry := skillJSON{
		Skill:       item.Skill,
		Source:      item.Source.Display(),
		Harness:     string(item.Harness),
		Settings:    map[string]string{},
		Requirement: item.Requirement,
		RequiredBy:  item.RequiredBy,
		Decision:    string(item.Decision),
		Path:        item.Path,
		Location:    item.Location,
		Scope:       string(item.Scope),
		Version:     item.Version,
		Revision:    item.Revision,
		Target:      item.Target,
		Command:     item.Command,
		Shadowed:    item.Shadowed,
		Reason:      item.Reason,
	}
	maps.Copy(entry.Settings, item.Settings)
	return entry
}

// addSkills fills the Skill sections of payload from the view.
func addSkills(payload *planJSON, view *planView) {
	for i := range view.Plan.Skills {
		item := &view.Plan.Skills[i]
		if payload.SkillSummary == nil {
			payload.SkillSummary = map[string]int{}
		}
		payload.Skills = append(payload.Skills, newSkillJSON(item))
		payload.SkillSummary[string(item.Decision)]++
	}
	for i := range view.SkippedSkills {
		payload.SkippedSkills = append(payload.SkippedSkills, newSkillJSON(&view.SkippedSkills[i]))
	}
	if view.Outcome == nil {
		return
	}
	for _, run := range view.Outcome.Runs {
		harnesses := make([]string, 0, len(run.Harnesses))
		for _, h := range run.Harnesses {
			harnesses = append(harnesses, string(h))
		}
		payload.SkillRuns = append(payload.SkillRuns, skillRunJSON{
			Skill: run.Skill, Path: run.Path, Command: run.Command, Harnesses: harnesses,
			ExitCode: run.ExitCode, Error: run.Error, Output: run.Output,
		})
	}
	for _, effect := range view.Outcome.Effects {
		payload.SkillEffects = append(payload.SkillEffects, effectJSON(effect))
	}
	payload.SkillFailures = view.Outcome.Failures
}

type harnessJSON struct {
	ID       string        `json:"id"`
	Name     string        `json:"name"`
	Detected bool          `json:"detected"`
	Settings []settingJSON `json:"settings"`
}

type settingJSON struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Source string `json:"source"`
}

type sourceJSON struct {
	Kind     string `json:"kind"`
	Locator  string `json:"locator"`
	Ref      string `json:"ref,omitempty"`
	Revision string `json:"revision,omitempty"`
	// Transport and URL name an SSH clone URL; https sources omit them.
	Transport string `json:"transport,omitempty"`
	URL       string `json:"url,omitempty"`
}

type itemJSON struct {
	Agent   string `json:"agent"`
	Harness string `json:"harness"`
	// Settings are the explicit --extra values recorded with the installation.
	Settings map[string]string `json:"settings"`
	// EffectiveSettings include native defaults detected for the Harness.
	EffectiveSettings map[string]string `json:"effectiveSettings"`
	Destination       string            `json:"destination,omitempty"`
	Path              string            `json:"path,omitempty"`
	Variant           string            `json:"variant,omitempty"`
	SourcePath        string            `json:"sourcePath,omitempty"`
	Source            sourceJSON        `json:"source"`
	Origin            string            `json:"origin"`
	RequiredBy        []string          `json:"requiredBy,omitempty"`
	Action            string            `json:"action"`
	Reason            string            `json:"reason,omitempty"`
	Notes             []string          `json:"notes,omitempty"`
}

func (p *printer) plan(view *planView, status string) {
	payload := planJSON{
		Status:    status,
		Scope:     string(view.Plan.Scope),
		Source:    sourceJSON(view.Plan.Source),
		Harnesses: make([]harnessJSON, 0, len(view.Harnesses)),
		Items:     make([]itemJSON, 0, len(view.Plan.Items)),
		Changes:   view.Report,
		Summary:   map[string]int{},
	}
	effective := make(map[agent.HarnessID]map[string]string, len(view.Harnesses))
	for _, h := range view.Harnesses {
		entry := harnessJSON{
			ID: string(h.ID), Name: h.Name, Detected: h.Detected,
			Settings: make([]settingJSON, 0, len(h.Settings)),
		}
		values := make(map[string]string, len(h.Settings))
		for _, setting := range h.Settings {
			entry.Settings = append(entry.Settings, settingJSON(setting))
			values[setting.Key] = setting.Value
		}
		effective[h.ID] = values
		payload.Harnesses = append(payload.Harnesses, entry)
	}
	for _, item := range view.Plan.Items {
		payload.Items = append(payload.Items, newItemJSON(item, effective))
		payload.Summary[string(item.Action)]++
	}
	for _, item := range view.Skipped {
		payload.Skipped = append(payload.Skipped, newItemJSON(item, effective))
	}
	addSkills(&payload, view)
	if p.json {
		_ = p.emitJSON(payload)
		return
	}
	p.humanPlan(&payload)
}

func newItemJSON(item install.Item, effective map[agent.HarnessID]map[string]string) itemJSON {
	entry := itemJSON{
		Agent:             item.Agent,
		Harness:           string(item.Harness),
		Settings:          map[string]string{},
		EffectiveSettings: map[string]string{},
		Destination:       item.Destination.Label,
		Path:              item.Path,
		SourcePath:        item.SourcePath,
		Source:            sourceJSON(item.Source),
		Origin:            string(item.Origin),
		RequiredBy:        item.RequiredBy,
		Action:            string(item.Action),
		Reason:            item.Reason,
		Notes:             item.Artifact.Notes,
	}
	maps.Copy(entry.Settings, item.Settings)
	maps.Copy(entry.EffectiveSettings, effective[item.Harness])
	if item.Path != "" {
		entry.Variant = string(state.VariantNative)
		if item.Artifact.Generated {
			entry.Variant = string(state.VariantGenerated)
		}
	}
	return entry
}

func (p *printer) humanPlan(payload *planJSON) {
	s := p.styles
	if payload.Changes != nil {
		fmt.Fprintf(p.w, "%s · %s scope · lock %s\n",
			s.title.Render(lifecycleTitle(payload.Changes.Command, payload.Status)),
			payload.Scope, payload.Changes.Lock)
		p.humanReport(payload.Changes)
	} else {
		fmt.Fprintf(p.w, "%s · %s scope · from %s\n",
			s.title.Render(titleStatus(payload.Status)), payload.Scope,
			sourceLabel(payload.Source))
	}
	if len(payload.Harnesses) > 0 {
		fmt.Fprintln(p.w, s.heading.Render("  Harnesses"))
		for _, h := range payload.Harnesses {
			line := fmt.Sprintf("    %-9s %-19s", h.ID, h.Name)
			if h.Detected {
				line += " " + s.dim.Render("detected")
			}
			for _, setting := range h.Settings {
				line += fmt.Sprintf("  %s=%s %s",
					setting.Key, setting.Value, s.dim.Render("("+setting.Source+")"))
			}
			fmt.Fprintln(p.w, strings.TrimRight(line, " "))
		}
	}
	p.humanSkills("Skills", payload.Skills)
	if len(payload.SkippedSkills) > 0 {
		p.humanSkills("Skipped skills", payload.SkippedSkills)
	}
	p.humanSkillRuns(payload)
	itemsHeading := "Agents"
	if payload.Status == statusFailed {
		itemsHeading = "Agents (not written)"
	}
	p.humanItems(itemsHeading, payload.Items)
	if len(payload.Skipped) > 0 {
		p.humanItems("Skipped (not written)", payload.Skipped)
	}
	line := summaryLine(payload.Summary)
	if len(payload.SkillSummary) > 0 {
		line += "  ·  skills: " + skillSummaryLine(payload.SkillSummary)
	}
	fmt.Fprintln(p.w, "  "+line)
}

// humanSkills lists Skill requirements grouped by Skill, one line per Harness.
func (p *printer) humanSkills(heading string, items []skillJSON) {
	if len(items) == 0 {
		return
	}
	s := p.styles
	fmt.Fprintln(p.w, s.heading.Render("  "+heading))
	var group string
	for i := range items {
		item := &items[i]
		if key := item.Skill + "\x00" + item.Requirement; key != group {
			group = key
			fmt.Fprintln(p.w, "    "+s.title.Render(item.Skill)+s.dim.Render(fmt.Sprintf(
				"  %s from %s, required by %s",
				item.Requirement, item.Source, strings.Join(item.RequiredBy, ", "),
			)))
		}
		decision := skills.Decision(item.Decision)
		marker := "+"
		switch {
		case decision.Blocking():
			marker = "✗"
		case decision == skills.DecisionReuse || decision == skills.DecisionUnchanged:
			marker = "="
		}
		action := fmt.Sprintf("%s %-12s", marker, item.Decision)
		switch {
		case decision.Blocking():
			action = s.blocked.Render(action)
		case marker == "=":
			action = s.unchanged.Render(action)
		default:
			action = s.create.Render(action)
		}
		line := fmt.Sprintf("      %s %-9s %-10s", action, item.Harness, item.Version)
		if item.Path != "" {
			line += " " + item.Path
		}
		fmt.Fprintln(p.w, strings.TrimRight(line, " "))
		if item.Location != "" {
			fmt.Fprintln(p.w, "          "+s.dim.Render(item.Location))
		}
		if len(item.Command) > 0 {
			fmt.Fprintln(p.w, "          "+s.dim.Render(
				"runs "+skills.Display(item.Command[0], item.Command[1:])))
		}
		if item.Reason != "" {
			style := s.dim
			if decision.Blocking() {
				style = s.blocked
			}
			fmt.Fprintln(p.w, "          "+style.Render(item.Reason))
		}
	}
}

// humanSkillRuns reports backend runs, failures and observed effects.
func (p *printer) humanSkillRuns(payload *planJSON) {
	s := p.styles
	if len(payload.SkillRuns) > 0 {
		fmt.Fprintln(p.w, s.heading.Render("  Skill installer runs"))
		for _, run := range payload.SkillRuns {
			status := s.create.Render("ok")
			if run.Error != "" {
				status = s.blocked.Render("failed: " + run.Error)
			}
			fmt.Fprintf(p.w, "    %s for %s: %s\n      %s\n", run.Skill,
				strings.Join(run.Harnesses, ", "), status,
				s.dim.Render(skills.Display(run.Command[0], run.Command[1:])))
			if run.Error != "" && strings.TrimSpace(run.Output) != "" {
				for line := range strings.SplitSeq(strings.TrimRight(run.Output, "\n"), "\n") {
					fmt.Fprintln(p.w, "      | "+line)
				}
			}
		}
	}
	for _, failure := range payload.SkillFailures {
		fmt.Fprintln(p.w, "    "+s.blocked.Render("verification failed: "+failure))
	}
	if payload.Status == statusFailed || len(payload.SkillEffects) > 0 {
		fmt.Fprintln(p.w, s.heading.Render("  Changes observed in skill locations"))
		if len(payload.SkillEffects) == 0 {
			fmt.Fprintln(p.w, "    none")
		}
		for _, effect := range payload.SkillEffects {
			fmt.Fprintf(p.w, "    %-8s %s\n", effect.Change, effect.Path)
		}
		if payload.Status == statusFailed {
			fmt.Fprintln(p.w, "    "+s.dim.Render(
				"agnthub does not roll back the skill installer; review or remove these yourself"))
		}
	}
}

func skillSummaryLine(summary map[string]int) string {
	order := []skills.Decision{
		skills.DecisionInstall, skills.DecisionReuse, skills.DecisionUnchanged,
		skills.DecisionShadowed, skills.DecisionConflict, skills.DecisionUnsatisfied,
		skills.DecisionDisabled, skills.DecisionUnsupported,
	}
	parts := make([]string, 0, len(order))
	for _, decision := range order {
		if n := summary[string(decision)]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, decision))
		}
	}
	return strings.Join(parts, " · ")
}

func (p *printer) humanItems(heading string, items []itemJSON) {
	s := p.styles
	fmt.Fprintln(p.w, s.heading.Render("  "+heading))
	var group string
	for i := range items {
		item := &items[i]
		if key := itemGroup(item); key != group {
			group = key
			fmt.Fprintln(p.w, "    "+s.title.Render(item.Agent)+groupDetail(item, s))
		}
		action := fmt.Sprintf("%s %-12s", actionMarker(install.Action(item.Action)), item.Action)
		switch {
		case install.Action(item.Action).Blocking():
			action = s.blocked.Render(action)
		case item.Action == string(install.ActionUnchanged):
			action = s.unchanged.Render(action)
		default:
			action = s.create.Render(action)
		}
		line := fmt.Sprintf("      %s %-9s %-10s", action, item.Harness, item.Variant)
		if item.Path != "" {
			line += " " + item.Path
		}
		fmt.Fprintln(p.w, strings.TrimRight(line, " "))
		if item.Destination != "" {
			fmt.Fprintln(p.w, "          "+s.dim.Render(item.Destination))
		}
		if item.Reason != "" {
			fmt.Fprintln(p.w, "          "+s.blocked.Render(item.Reason))
		}
		for _, note := range item.Notes {
			fmt.Fprintln(p.w, "          note: "+note)
		}
	}
}

// itemGroup groups consecutive items of the same Agent and dependency context.
func itemGroup(item *itemJSON) string {
	return item.Agent + "\x00" + item.Origin + "\x00" + strings.Join(item.RequiredBy, ",") +
		"\x00" + item.Source.Locator
}

func groupDetail(item *itemJSON, s planStyles) string {
	if item.Origin != string(state.OriginDependency) {
		return ""
	}
	return s.dim.Render(fmt.Sprintf(
		"  dependency of %s, from %s",
		strings.Join(item.RequiredBy, ", "),
		sourceLabel(item.Source),
	))
}

func summaryLine(summary map[string]int) string {
	order := []install.Action{
		install.ActionCreate, install.ActionReplace, install.ActionUnchanged,
		install.ActionConflict, install.ActionIncompatible, install.ActionInvalid,
	}
	parts := make([]string, 0, len(order))
	for _, action := range order {
		if n := summary[string(action)]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, action))
		}
	}
	if len(parts) == 0 {
		return "nothing to install"
	}
	return strings.Join(parts, " · ")
}

func titleStatus(status string) string {
	switch status {
	case statusRefused:
		return "Refused before writing"
	case statusApplied:
		return "Installed"
	case statusFailed:
		return "Failed: skills not verified, no agent written"
	default:
		return "Plan"
	}
}

func sourceLabel(src sourceJSON) string {
	label := src.Locator
	if src.Ref != "" {
		label += "#" + src.Ref
	}
	if src.Revision != "" {
		label += " @ " + shortRevision(src.Revision)
	}
	return label
}

func actionMarker(action install.Action) string {
	switch {
	case action.Blocking():
		return "✗"
	case action == install.ActionUnchanged:
		return "="
	default:
		return "+"
	}
}
