package cli

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/heynemann/agnthub/agent"
	"github.com/heynemann/agnthub/harness"
	"github.com/heynemann/agnthub/internal/uninstall"
	"github.com/heynemann/agnthub/state"
)

type removeOptions struct {
	harnesses []string
	extras    []string
	global    bool
	yes       bool
	json      bool
	clean     bool
}

func (a *App) removeCommand(ctx context.Context) *cobra.Command {
	opts := &removeOptions{}
	cmd := &cobra.Command{
		Use:     "remove [agent...]",
		Aliases: []string{"rm"},
		Short:   "Remove installed agents or clean up unneeded dependencies (alias: rm)",
		Long: `Remove deletes the selected agents' files that agnthub installed and that are
unchanged since. Modified files, files agnthub cannot prove it owns and unrelated
harness configuration are never deleted. Companion agents and skills stay
installed: removing an agent never removes its dependencies.

--clean-dependencies is the separate, explicit cleanup. It removes companion agents
and skills only when agnthub installed them, they are unchanged, and no remaining
agent in any visible lock requires them. Reused skills (already present when
agnthub needed them), skills shared with another installation and skills whose
users agnthub cannot know are preserved and reported. Name dependencies to limit
the cleanup to them.

A file installed for several harness bindings (e.g. one project .omp/agents file
used by omp profiles hep and work) is only deleted when its last binding goes;
choose a binding with --extra. Ambiguous bindings are asked about in a terminal
and refused otherwise.

Without agent names, a terminal user chooses the agents to remove from a list.`,
		Example: `  # Choose installed agents interactively
  agnthub remove
  agnthub rm reviewer
  agnthub rm reviewer planner --yes
  agnthub remove reviewer --harness claude --harness codex --yes
  agnthub remove reviewer --harness omp --extra omp.profile=work --yes
  agnthub rm --global reviewer
  agnthub remove --clean-dependencies --yes
  agnthub remove --clean-dependencies code-review --global`,
		RunE: func(_ *cobra.Command, args []string) error {
			return a.runRemove(ctx, args, opts)
		},
	}
	flags := cmd.Flags()
	flags.StringArrayVarP(&opts.harnesses, "harness", "H", nil,
		"only remove from this harness (repeatable)")
	flags.StringArrayVar(&opts.extras, "extra", nil,
		"select the recorded harness binding <harness>.<key>=<value>, e.g. omp.profile=work")
	flags.BoolVarP(&opts.global, "global", "g", false, "remove user-scope installations")
	flags.BoolVarP(&opts.yes, "yes", "y", false, "apply without prompting")
	flags.BoolVar(&opts.json, "json", false, "print machine-readable JSON; never prompts")
	flags.BoolVar(&opts.clean, "clean-dependencies", false,
		"remove dependencies no remaining agent requires instead of agents")
	return cmd
}

// removal is one remove or cleanup run against one lock.
type removal struct {
	app         *App
	opts        *removeOptions
	lockPath    string
	projectRoot string
	inv         *uninstall.Inventory
	settings    map[agent.HarnessID]harness.Settings
	filter      []agent.HarnessID
	interactive bool
	external    map[string][]string
	unreadable  []string
}

func (a *App) runRemove(ctx context.Context, names []string, opts *removeOptions) error {
	r := &removal{
		app:         a,
		opts:        opts,
		interactive: a.Interactive && !opts.yes && !opts.json,
	}
	if !opts.clean && len(names) == 0 && !r.interactive {
		return errors.New(
			"name the agents to remove, or use --clean-dependencies to clean up dependencies",
		)
	}
	if err := r.load(); err != nil {
		return err
	}
	var selected []*uninstall.Unit
	var err error
	switch {
	case opts.clean:
		selected, err = r.dependencies(names)
	case len(names) == 0:
		selected, err = r.promptAgents(ctx)
	default:
		selected, err = r.agents(names)
	}
	if err != nil {
		return err
	}
	selected, err = r.bindings(ctx, selected)
	if err != nil {
		return err
	}
	plan := uninstall.Build(r.inv, selected, uninstall.Options{
		Cleanup: opts.clean, External: r.externalLocks,
	})
	return r.decide(ctx, plan)
}

// load reads the scope's lock and this machine's ownership records and validates
// the harness and binding filters.
func (r *removal) load() error {
	a := r.app
	for _, name := range r.opts.harnesses {
		id, err := agent.ParseHarness(name)
		if err != nil {
			return err
		}
		if !slices.Contains(r.filter, id) {
			r.filter = append(r.filter, id)
		}
	}
	selectable := r.filter
	if len(selectable) == 0 {
		selectable = a.supportedHarnesses()
	}
	settings, err := harness.ResolveSettings(a.Registry, r.opts.extras, selectable)
	if err != nil {
		return err
	}
	r.settings = settings
	r.lockPath = state.LockPath(a.WorkDir, a.Env.ConfigHome(), r.opts.global)
	if !r.opts.global {
		r.projectRoot = a.WorkDir
	}
	lock, err := state.LoadLock(r.lockPath)
	if err != nil {
		return err
	}
	ownership, err := state.LoadOwnership(state.OwnershipPath(a.Env.StateHome()))
	if err != nil {
		return err
	}
	r.inv = uninstall.NewInventory(lock, ownership, r.projectRoot, a.Env.Home())
	return nil
}

func (r *removal) scopeName() string {
	if r.opts.global {
		return "global"
	}
	return "project"
}

// agents selects every artifact of the named locked Agents.
func (r *removal) agents(names []string) ([]*uninstall.Unit, error) {
	var selected []*uninstall.Unit
	for _, name := range unique(names) {
		units := r.named(name, uninstall.KindAgent)
		if len(units) == 0 {
			return nil, fmt.Errorf("agent %q is not installed in %s scope (installed: %s)",
				name, r.scopeName(), r.installedNames(uninstall.KindAgent))
		}
		if err := ambiguousKey(name, units); err != nil {
			return nil, err
		}
		filtered := r.harnessFilter(units)
		if len(filtered) == 0 {
			return nil, fmt.Errorf("agent %q is not installed for %s (installed for %s)",
				name, joinHarnesses(r.filter), unitHarnesses(units))
		}
		selected = append(selected, filtered...)
	}
	return selected, nil
}

// promptAgents asks a terminal user which installed Agents to remove, offering
// each locked Agent (within the harness filter) once with its harnesses.
func (r *removal) promptAgents(ctx context.Context) ([]*uninstall.Unit, error) {
	var keys []string
	byKey := map[string][]*uninstall.Unit{}
	for _, unit := range r.harnessFilter(r.inv.Units) {
		if unit.Kind != uninstall.KindAgent {
			continue
		}
		if _, seen := byKey[unit.Key]; !seen {
			keys = append(keys, unit.Key)
		}
		byKey[unit.Key] = append(byKey[unit.Key], unit)
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("no agents are installed in %s scope", r.scopeName())
	}
	options := make([]Option, 0, len(keys))
	for _, key := range keys {
		units := byKey[key]
		label := fmt.Sprintf("%s — %s, from %s", units[0].Name, unitHarnesses(units),
			sourceLabel(sourceJSON(units[0].Source)))
		if units[0].Origin == state.OriginDependency {
			label += " (dependency)"
		}
		options = append(options, Option{Value: key, Label: label})
	}
	chosen, err := r.app.Prompter.MultiSelect(ctx, "Agents to remove",
		"Only unchanged files agnthub installed are deleted; dependencies stay installed.",
		options)
	if err != nil {
		return nil, err
	}
	if len(chosen) == 0 {
		return nil, errCancelled
	}
	return r.agents(chosen)
}

// dependencies selects the cleanup candidates: companion Agents and Skills, all
// of them or only the named ones.
func (r *removal) dependencies(names []string) ([]*uninstall.Unit, error) {
	if len(names) == 0 {
		var candidates []*uninstall.Unit
		for _, unit := range r.inv.Units {
			if unit.Kind == uninstall.KindSkill || unit.Origin == state.OriginDependency {
				candidates = append(candidates, unit)
			}
		}
		return r.harnessFilter(candidates), nil
	}
	var selected []*uninstall.Unit
	for _, name := range unique(names) {
		units := append(r.named(name, uninstall.KindAgent), r.named(name, uninstall.KindSkill)...)
		if len(units) == 0 {
			return nil, fmt.Errorf("%q is not an installed dependency in %s scope", name,
				r.scopeName())
		}
		if err := ambiguousKey(name, units); err != nil {
			return nil, err
		}
		if units[0].Kind == uninstall.KindAgent && units[0].Origin == state.OriginDirect {
			return nil, fmt.Errorf(
				"%s was installed directly, not as a dependency; remove it with: agnthub remove %s",
				name, name)
		}
		filtered := r.harnessFilter(units)
		if len(filtered) == 0 {
			return nil, fmt.Errorf("%q is not installed for %s (installed for %s)",
				name, joinHarnesses(r.filter), unitHarnesses(units))
		}
		selected = append(selected, filtered...)
	}
	return selected, nil
}

// named returns the units of kind whose name or full key is name.
func (r *removal) named(name string, kind uninstall.Kind) []*uninstall.Unit {
	var units []*uninstall.Unit
	for _, unit := range r.inv.Units {
		if unit.Kind == kind && (unit.Name == name || unit.Key == name) {
			units = append(units, unit)
		}
	}
	return units
}

// ambiguousKey refuses a name shared by Agents from different sources.
func ambiguousKey(name string, units []*uninstall.Unit) error {
	var keys []string
	for _, unit := range units {
		if !slices.Contains(keys, string(unit.Kind)+" "+unit.Key) {
			keys = append(keys, string(unit.Kind)+" "+unit.Key)
		}
	}
	if len(keys) < 2 {
		return nil
	}
	return fmt.Errorf("%q names several installations; use the full key:\n  %s",
		name, strings.Join(keys, "\n  "))
}

func (r *removal) harnessFilter(units []*uninstall.Unit) []*uninstall.Unit {
	if len(r.filter) == 0 {
		return units
	}
	var kept []*uninstall.Unit
	for _, unit := range units {
		if slices.Contains(r.filter, agent.HarnessID(unit.Harness)) {
			kept = append(kept, unit)
		}
	}
	return kept
}

// bindings limits the selection to the intended recorded Harness bindings:
// explicit --extra settings select matching records only; several recorded
// bindings without them are asked about in a terminal and refused otherwise.
func (r *removal) bindings(
	ctx context.Context,
	units []*uninstall.Unit,
) ([]*uninstall.Unit, error) {
	type group struct {
		label string
		units []*uninstall.Unit
	}
	var groups []*group
	index := map[string]*group{}
	for _, unit := range units {
		key := string(unit.Kind) + "|" + unit.Key + "|" + unit.Harness
		g := index[key]
		if g == nil {
			g = &group{label: unit.Name + " for " + unit.Harness}
			index[key] = g
			groups = append(groups, g)
		}
		g.units = append(g.units, unit)
	}
	var selected []*uninstall.Unit
	var ambiguous []string
	for _, g := range groups {
		harnessID := agent.HarnessID(g.units[0].Harness)
		if explicit := r.settings[harnessID]; len(explicit) > 0 {
			matched := matchSettings(g.units, explicit)
			if len(matched) == 0 && !r.opts.clean {
				return nil, fmt.Errorf("%s has no recorded binding with %s (recorded: %s)",
					g.label, formatSettings(explicit), recordedBindings(g.units))
			}
			selected = append(selected, matched...)
			continue
		}
		distinct := distinctBindings(g.units)
		if len(distinct) < 2 {
			selected = append(selected, g.units...)
			continue
		}
		if !r.interactive {
			ambiguous = append(ambiguous, fmt.Sprintf("%s is recorded for %s",
				g.label, strings.Join(distinct, "; ")))
			continue
		}
		chosen, err := r.promptBindings(ctx, g.label, g.units, distinct)
		if err != nil {
			return nil, err
		}
		selected = append(selected, chosen...)
	}
	if len(ambiguous) > 0 {
		return nil, fmt.Errorf(
			"ambiguous harness binding; nothing was removed:\n  %s\n"+
				"choose one with --extra <harness>.<key>=<value>, e.g. --extra omp.profile=<name>",
			strings.Join(ambiguous, "\n  "))
	}
	return selected, nil
}

func (r *removal) promptBindings(
	ctx context.Context,
	label string,
	units []*uninstall.Unit,
	distinct []string,
) ([]*uninstall.Unit, error) {
	options := make([]Option, 0, len(distinct))
	for _, binding := range distinct {
		options = append(options, Option{Value: binding, Label: binding})
	}
	values, err := r.app.Prompter.MultiSelect(ctx, "Bindings of "+label+" to remove",
		"Other bindings stay installed.", options)
	if err != nil {
		return nil, err
	}
	if len(values) == 0 {
		return nil, errCancelled
	}
	var chosen []*uninstall.Unit
	for _, unit := range units {
		if slices.Contains(values, bindingLabel(unit)) {
			chosen = append(chosen, unit)
		}
	}
	return chosen, nil
}

// matchSettings keeps the recorded units whose Settings include every explicit one.
func matchSettings(units []*uninstall.Unit, explicit harness.Settings) []*uninstall.Unit {
	var matched []*uninstall.Unit
	for _, unit := range units {
		if unit.Record == nil {
			continue
		}
		ok := true
		for key, value := range explicit {
			if unit.Record.Settings[key] != value {
				ok = false
				break
			}
		}
		if ok {
			matched = append(matched, unit)
		}
	}
	return matched
}

func bindingLabel(unit *uninstall.Unit) string {
	if settings := unit.Settings(); len(settings) > 0 {
		return unit.Harness + " " + formatSettings(settings)
	}
	if unit.Record == nil {
		return unit.Harness + " (no binding recorded on this machine)"
	}
	return unit.Harness + " (default settings)"
}

func distinctBindings(units []*uninstall.Unit) []string {
	var labels []string
	for _, unit := range units {
		if label := bindingLabel(unit); !slices.Contains(labels, label) {
			labels = append(labels, label)
		}
	}
	return labels
}

func recordedBindings(units []*uninstall.Unit) string {
	return strings.Join(distinctBindings(units), "; ")
}

func joinHarnesses(ids []agent.HarnessID) string {
	names := make([]string, 0, len(ids))
	for _, id := range ids {
		names = append(names, string(id))
	}
	return strings.Join(names, ", ")
}

func unitHarnesses(units []*uninstall.Unit) string {
	var names []string
	for _, unit := range units {
		if !slices.Contains(names, unit.Harness) {
			names = append(names, unit.Harness)
		}
	}
	return strings.Join(names, ", ")
}

func (r *removal) installedNames(kind uninstall.Kind) string {
	var names []string
	for _, unit := range r.inv.Units {
		if unit.Kind == kind && !slices.Contains(names, unit.Name) {
			names = append(names, unit.Name)
		}
	}
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

func unique(names []string) []string {
	var out []string
	for _, name := range names {
		if !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	return out
}

// externalLocks names the other visible locks that list a Skill directory: the
// global lock and the locks of every project this machine installed into. An
// unreadable lock cannot prove a Skill unused, so it is named for every Skill.
func (r *removal) externalLocks(path string) []string {
	if r.external == nil {
		r.loadExternal()
	}
	return append(slices.Clone(r.external[filepath.Clean(path)]), r.unreadable...)
}

func (r *removal) loadExternal() {
	a := r.app
	type visible struct{ lockPath, root string }
	var locks []visible
	if !r.opts.global {
		locks = append(locks, visible{lockPath: state.LockPath("", a.Env.ConfigHome(), true)})
	}
	var roots []string
	for _, record := range r.inv.Ownership.Records {
		if record.ProjectRoot != "" && record.ProjectRoot != r.projectRoot &&
			!slices.Contains(roots, record.ProjectRoot) {
			roots = append(roots, record.ProjectRoot)
		}
	}
	slices.Sort(roots)
	for _, root := range roots {
		locks = append(locks, visible{lockPath: state.LockPath(root, "", false), root: root})
	}
	r.external = map[string][]string{}
	for _, v := range locks {
		lock, err := state.LoadLock(v.lockPath)
		if err != nil {
			r.unreadable = append(r.unreadable, v.lockPath+" (unreadable)")
			continue
		}
		for _, skill := range lock.Skills {
			path := filepath.Clean(uninstall.AbsPath(skill.Path, v.root, a.Env.Home()))
			if !slices.Contains(r.external[path], v.lockPath) {
				r.external[path] = append(r.external[path], v.lockPath)
			}
		}
	}
}

// decide previews the plan, then applies it when allowed: --yes applies a removal
// only when every selected artifact can be removed, a terminal user is asked,
// and cleanup applies what is safe while reporting what it preserves.
func (r *removal) decide(ctx context.Context, plan *uninstall.Plan) error {
	out := r.app.printer(r.opts.json)
	changes, refused := 0, 0
	for _, entry := range plan.Entries {
		switch {
		case entry.Action.Changes():
			changes++
		case entry.Action == uninstall.ActionRefuse:
			refused++
		}
	}
	if changes == 0 {
		status := statusRefused
		if refused == 0 {
			status = statusApplied
		}
		r.print(out, plan, status)
		if refused > 0 {
			return &ExitError{Code: 1}
		}
		return nil
	}
	if !r.interactive {
		if refused > 0 {
			r.print(out, plan, statusRefused)
			return &ExitError{Code: 1}
		}
		if !r.opts.yes {
			r.print(out, plan, statusPlanned)
			if r.opts.json {
				return errors.New("--json never prompts; rerun with --yes to apply this plan")
			}
			return errors.New(
				"confirmation required but input is not interactive; rerun with --yes",
			)
		}
		return r.apply(out, plan)
	}
	r.print(out, plan, statusPlanned)
	question := "Apply this removal?"
	if refused > 0 {
		question = fmt.Sprintf("%d item(s) are refused and stay. Apply the other %d?",
			refused, changes)
	}
	confirmed, err := r.app.Prompter.Confirm(ctx, question, "")
	if err != nil {
		return err
	}
	if !confirmed {
		return errCancelled
	}
	return r.apply(out, plan)
}

func (r *removal) apply(out *printer, plan *uninstall.Plan) error {
	applyErr := uninstall.Apply(r.inv, plan)
	if err := r.inv.Lock.Save(r.lockPath); err != nil {
		r.print(out, plan, statusFailed)
		return fmt.Errorf("save lock: %w", err)
	}
	if err := r.inv.Ownership.Save(); err != nil {
		r.print(out, plan, statusFailed)
		return fmt.Errorf("save ownership records: %w", err)
	}
	if applyErr != nil {
		r.print(out, plan, statusFailed)
		return &ExitError{Code: 1}
	}
	r.print(out, plan, statusApplied)
	return nil
}

type removeJSON struct {
	Status string `json:"status"`
	// Mode is "remove" or "cleanup".
	Mode  string           `json:"mode"`
	Scope string           `json:"scope"`
	Lock  string           `json:"lock"`
	Items []removeItemJSON `json:"items"`
	// DependenciesKept lists the Dependencies of removed Agents that stay installed.
	DependenciesKept []dependencyJSON `json:"dependenciesKept,omitempty"`
	// Summary counts results once applied, planned actions before.
	Summary map[string]int `json:"summary"`
}

type removeItemJSON struct {
	Kind     string            `json:"kind"`
	Name     string            `json:"name"`
	Source   sourceJSON        `json:"source"`
	Origin   string            `json:"origin"`
	Harness  string            `json:"harness"`
	Settings map[string]string `json:"settings"`
	Path     string            `json:"path"`
	Action   string            `json:"action"`
	// Result is what happened when applied: removed, unbound, forgotten, released
	// or failed.
	Result  string   `json:"result,omitempty"`
	Reason  string   `json:"reason,omitempty"`
	KeptFor []string `json:"keptFor,omitempty"`
	// Kept is the directory a removed Skill link pointed to; it stays.
	Kept  string `json:"kept,omitempty"`
	Error string `json:"error,omitempty"`
}

type dependencyJSON struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

func (r *removal) payload(plan *uninstall.Plan, status string) removeJSON {
	payload := removeJSON{
		Status:  status,
		Mode:    "remove",
		Scope:   r.scopeName(),
		Lock:    r.lockPath,
		Items:   make([]removeItemJSON, 0, len(plan.Entries)),
		Summary: map[string]int{},
	}
	if r.opts.clean {
		payload.Mode = "cleanup"
	}
	for _, entry := range plan.Entries {
		unit := entry.Unit
		item := removeItemJSON{
			Kind:     string(unit.Kind),
			Name:     unit.Name,
			Source:   sourceJSON(unit.Source),
			Origin:   string(unit.Origin),
			Harness:  unit.Harness,
			Settings: map[string]string{},
			Path:     unit.Path,
			Action:   string(entry.Action),
			Reason:   entry.Reason,
			KeptFor:  entry.KeptFor,
			Kept:     entry.Kept,
		}
		maps.Copy(item.Settings, unit.Settings())
		if status == statusApplied || status == statusFailed {
			item.Result = result(entry)
		}
		if entry.Err != nil {
			item.Error = entry.Err.Error()
		}
		payload.Items = append(payload.Items, item)
		if item.Result != "" {
			payload.Summary[item.Result]++
		} else {
			payload.Summary[item.Action]++
		}
	}
	if !r.opts.clean {
		payload.DependenciesKept = r.keptDependencies(plan)
	}
	return payload
}

// result names what applying an entry did.
func result(entry *uninstall.Entry) string {
	if entry.Err != nil {
		return "failed"
	}
	switch entry.Action {
	case uninstall.ActionDelete:
		return "removed"
	case uninstall.ActionUnbind:
		return "unbound"
	case uninstall.ActionForget:
		return "forgotten"
	case uninstall.ActionRelease:
		return "released"
	case uninstall.ActionRefuse:
		return "refused"
	default:
		return "preserved"
	}
}

// keptDependencies lists what the removed Agents required: removal leaves it.
func (r *removal) keptDependencies(plan *uninstall.Plan) []dependencyJSON {
	removed := map[string]bool{}
	for _, entry := range plan.Entries {
		if entry.Action.Changes() {
			removed[entry.Unit.Key] = true
		}
	}
	var kept []dependencyJSON
	for _, entry := range plan.Entries {
		if !entry.Action.Changes() {
			continue
		}
		for _, requirement := range entry.Unit.Requires {
			dep := r.dependency(requirement)
			if dep.Name == "" || removed[dep.Name] || slices.Contains(kept, dep) {
				continue
			}
			kept = append(kept, dep)
		}
	}
	return kept
}

func (r *removal) dependency(requirement string) dependencyJSON {
	if name, ok := strings.CutPrefix(requirement, state.RequireSkill("")); ok {
		return dependencyJSON{Kind: string(uninstall.KindSkill), Name: name}
	}
	key, _ := strings.CutPrefix(requirement, state.RequireAgent(""))
	for _, unit := range r.inv.Units {
		if unit.Kind == uninstall.KindAgent && unit.Key == key {
			return dependencyJSON{Kind: string(uninstall.KindAgent), Name: unit.Name}
		}
	}
	return dependencyJSON{}
}

func (r *removal) print(out *printer, plan *uninstall.Plan, status string) {
	payload := r.payload(plan, status)
	if out.json {
		_ = out.emitJSON(payload)
		return
	}
	s := out.styles
	title := "Remove"
	if r.opts.clean {
		title = "Clean up dependencies"
	}
	fmt.Fprintf(out.w, "%s · %s scope · %s\n", s.title.Render(removeTitle(title, status)),
		payload.Scope, r.display(r.lockPath))
	if len(payload.Items) == 0 {
		fmt.Fprintln(out.w, "  nothing to clean up")
	}
	for _, item := range payload.Items {
		word := item.Action
		if item.Result != "" {
			word = item.Result
		}
		style := s.create
		switch {
		case item.Error != "" || item.Action == string(uninstall.ActionRefuse):
			style = s.blocked
		case !uninstall.Action(item.Action).Changes() || item.Action == "unbind":
			style = s.dim
		}
		name := item.Name
		if item.Kind == string(uninstall.KindSkill) {
			name += " (skill)"
		}
		line := fmt.Sprintf("  %s %-26s %-8s %s", style.Render(fmt.Sprintf("%-9s", word)),
			name, item.Harness, r.display(item.Path))
		if len(item.Settings) > 0 {
			line += " [" + formatSettings(item.Settings) + "]"
		}
		fmt.Fprintln(out.w, line)
		for _, note := range []string{item.Reason, item.Error} {
			if note != "" {
				fmt.Fprintf(out.w, "  %9s %s\n", "", s.dim.Render(note))
			}
		}
		if item.Kept != "" {
			fmt.Fprintf(out.w, "  %9s %s\n", "",
				s.dim.Render("removed the link only; "+r.display(item.Kept)+" stays"))
		}
	}
	if len(payload.DependenciesKept) > 0 {
		names := make([]string, 0, len(payload.DependenciesKept))
		for _, dep := range payload.DependenciesKept {
			names = append(names, dep.Name+" ("+dep.Kind+")")
		}
		fmt.Fprintf(out.w, "Dependencies stay installed: %s\n", strings.Join(names, ", "))
		fmt.Fprintln(out.w, s.dim.Render(
			"  clean up the ones no longer required with: agnthub remove --clean-dependencies"))
	}
	keys := slices.Sorted(maps.Keys(payload.Summary))
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%d %s", payload.Summary[key], key))
	}
	if len(parts) > 0 {
		fmt.Fprintf(out.w, "Summary: %s\n", strings.Join(parts, ", "))
	}
}

func removeTitle(title, status string) string {
	switch status {
	case statusApplied:
		return title + ": done"
	case statusRefused:
		return title + ": refused, nothing changed"
	case statusFailed:
		return title + ": partly failed"
	default:
		return title + " plan"
	}
}

// display shortens paths inside the project to project-relative ones.
func (r *removal) display(path string) string {
	return displayPath(r.app.WorkDir, path)
}
