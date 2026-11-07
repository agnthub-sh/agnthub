package cli

import (
	"fmt"
	"io"
	"maps"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/agnthub-sh/agnthub/agent"
	"github.com/agnthub-sh/agnthub/harness"
	"github.com/agnthub-sh/agnthub/install"
	"github.com/agnthub-sh/agnthub/skills"
	"github.com/agnthub-sh/agnthub/state"
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
	// Previewed reports that the user saw this plan before approving it, so the
	// result need not repeat its table.
	Previewed bool
	// Diff is the --json --diff patch of the plan.
	Diff *diffJSON
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
	title, heading, header, dim, scope, create, update, remove, unchanged, blocked,
	reason, reduced lipgloss.Style
}

func newPlanStyles() planStyles {
	return planStyles{
		title:     lipgloss.NewStyle().Bold(true),
		heading:   lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6")),
		header:    lipgloss.NewStyle().Bold(true).Faint(true),
		dim:       lipgloss.NewStyle().Faint(true),
		scope:     lipgloss.NewStyle().Foreground(lipgloss.Color("6")),
		create:    lipgloss.NewStyle().Foreground(lipgloss.Color("2")),
		update:    lipgloss.NewStyle().Foreground(lipgloss.Color("3")),
		remove:    lipgloss.NewStyle().Foreground(lipgloss.Color("1")),
		unchanged: lipgloss.NewStyle().Faint(true),
		blocked:   lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("1")),
		reason:    lipgloss.NewStyle().Foreground(lipgloss.Color("1")),
		reduced:   lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("3")),
	}
}

// printer is styled only when stdout is a color terminal; NO_COLOR, TERM=dumb and
// redirected output get plain text without escape sequences or emoji.
func (a *App) printer(asJSON bool) *printer {
	return a.printerTo(a.Stdout, asJSON)
}

// printerTo is printer for w, styled only when w is a color terminal.
func (a *App) printerTo(w io.Writer, asJSON bool) *printer {
	if asJSON {
		return newPrinter(w, true)
	}
	profile := colorprofile.Detect(w, a.Env.Pairs())
	plain := profile <= colorprofile.ASCII
	if plain {
		profile = colorprofile.NoTTY
	}
	space := &spacer{w: &colorprofile.Writer{Forward: w, Profile: profile}}
	p := newPrinter(space, false)
	p.space = space
	p.plain, p.width = plain, terminalWidth(w, a.Env)
	p.root, p.home = a.WorkDir, a.Env.Home()
	return p
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
	// Diff and Files are present with --diff: the patch and one entry per file.
	Diff  *string         `json:"diff,omitempty"`
	Files *[]diffFileJSON `json:"files,omitempty"`
	// previewed is planView.Previewed; human output only.
	previewed bool
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
	// Reduced lists capabilities the agent uses that the harness cannot grant,
	// so the installed agent goes without them.
	Reduced []string `json:"reduced,omitempty"`
	// OptIn names the flag (without dashes) installing it needs:
	// allow-without-shell when it loses the shell access it uses.
	OptIn string `json:"optIn,omitempty"`
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
	payload.previewed = view.Previewed
	if view.Diff != nil {
		payload.Diff, payload.Files = &view.Diff.Diff, &view.Diff.Files
	}
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
		Reduced:           item.Artifact.ReducedNames(),
	}
	if item.Artifact.WithoutShell() {
		entry.OptIn = install.OptInWithoutShell
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

// humanSkillRuns reports backend runs, failures and observed effects, each block
// after a blank line, and reports whether it printed anything.
func (p *printer) humanSkillRuns(payload *planJSON) bool {
	s := p.styles
	printed := false
	if len(payload.SkillRuns) > 0 {
		printed = true
		p.section()
		p.paragraph(0, s.heading.Render("Skill installer runs"))
		for _, run := range payload.SkillRuns {
			status := s.create.Render("ok")
			if run.Error != "" {
				status = s.blocked.Render("failed: " + run.Error)
			}
			p.paragraph(2, run.Skill+" for "+strings.Join(run.Harnesses, ", ")+": "+status)
			p.paragraph(2, "  "+s.dim.Render(skills.Display(run.Command[0], run.Command[1:])))
			if run.Error != "" && strings.TrimSpace(run.Output) != "" {
				for line := range strings.SplitSeq(strings.TrimRight(run.Output, "\n"), "\n") {
					p.paragraph(0, "  | "+line)
				}
			}
		}
	}
	if len(payload.SkillFailures) > 0 {
		printed = true
		p.section()
		for _, failure := range payload.SkillFailures {
			fmt.Fprintln(p.w, p.emoji("❌")+s.blocked.Render("verification failed: "+failure))
		}
	}
	if payload.Status == statusFailed || len(payload.SkillEffects) > 0 {
		printed = true
		p.section()
		p.paragraph(0, s.heading.Render("Changes observed in skill locations"))
		if len(payload.SkillEffects) == 0 {
			p.paragraph(0, "none")
		}
		for _, effect := range payload.SkillEffects {
			p.paragraph(0, fmt.Sprintf("%-8s %s", effect.Change, p.path(effect.Path)))
		}
		if payload.Status == statusFailed {
			p.paragraph(0, s.dim.Render(
				"agnthub does not roll back the skill installer; review or remove these yourself"))
		}
	}
	return printed
}

// skillSummaryLine counts the Skill decisions that act or refuse; reused and
// unchanged Skills are counted by the sentence under the Skills table.
func skillSummaryLine(summary map[string]int) string {
	order := []skills.Decision{
		skills.DecisionInstall, skills.DecisionShadowed, skills.DecisionConflict,
		skills.DecisionUnsatisfied, skills.DecisionDisabled, skills.DecisionUnsupported,
	}
	parts := make([]string, 0, len(order))
	for _, decision := range order {
		if n := summary[string(decision)]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, decision))
		}
	}
	return strings.Join(parts, " · ")
}

func sourceLabel(src sourceJSON) string {
	label := src.Locator
	if src.Ref != "" {
		label += "#" + src.Ref
	}
	if src.Revision != "" {
		label += "@" + shortRevision(src.Revision)
	}
	return label
}

func titleStatus(status string) string {
	switch status {
	case statusRefused:
		return "Refused before writing"
	case statusApplied:
		return "Installed"
	case statusFailed:
		return "Failed: skills not verified, no agent written"
	case statusUpToDate:
		return "Up to date"
	default:
		return "Plan"
	}
}
