package cli

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/heynemann/agnthub/adoption"
	"github.com/heynemann/agnthub/agent"
	"github.com/heynemann/agnthub/discovery"
	"github.com/heynemann/agnthub/harness"
	"github.com/heynemann/agnthub/install"
	"github.com/heynemann/agnthub/resolve"
	"github.com/heynemann/agnthub/skills"
	"github.com/heynemann/agnthub/source"
	"github.com/heynemann/agnthub/state"
)

type addOptions struct {
	list      bool
	agents    []string
	harnesses []string
	extras    []string
	global    bool
	yes       bool
	json      bool
	all       bool
}

// allSelector selects every agent (--agent '*') or every supported harness
// (--harness '*'), as skills.sh's --skill '*' and --agent '*' do.
const allSelector = "*"

// validate rejects contradictory flags before any source is opened.
func (o *addOptions) validate() error {
	if o.all && (len(o.agents) > 0 || len(o.harnesses) > 0) {
		return errors.New(
			"--all selects every agent and every supported harness; " +
				"drop --all to choose with --agent/--harness",
		)
	}
	for _, selectors := range [][]string{o.agents, o.harnesses} {
		if slices.Contains(selectors, allSelector) && len(selectors) > 1 {
			return errors.New("'*' already selects everything; drop the other values")
		}
	}
	return nil
}

func (a *App) addCommand(ctx context.Context) *cobra.Command {
	opts := &addOptions{}
	cmd := &cobra.Command{
		Use:     "add <source>",
		Aliases: []string{"a"},
		Short:   "Install agents from a repository or local directory (alias: a)",
		Long: `Install agents from a GitHub repository (owner/repo[#ref]), a Git URL or an
explicit local directory (./path). --agent selects agent definitions from the
source and --harness the coding harnesses to install them into; '*' selects all
of either. Harness settings use --extra <harness>.<key>=<value>.

In a terminal, add asks which agents and harnesses to install (detected harnesses
are preselected), previews the plan and asks before writing. --yes, --all and
--json never prompt and refuse any plan that is not entirely valid.

Required skills are reused when a compatible copy is already visible; missing ones
install first through the skill installer (skills.sh, or skillInstaller in
~/.config/agnthub/config.yaml) and are verified before any agent file is written.`,
		Example: `  # List the agents a source provides without installing
  agnthub add owner/repo -l
  # Choose agents and harnesses interactively
  agnthub add owner/repo
  agnthub add ./my-agents --agent reviewer --harness claude --harness codex
  agnthub add owner/repo#v1.2.0 --agent '*' --harness claude --yes
  agnthub add owner/repo --harness omp --global --extra omp.profile=work
  # Every agent into every harness, without prompting
  agnthub add owner/repo --all
  agnthub add owner/repo --all --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return a.runAdd(ctx, args[0], opts)
		},
	}
	flags := cmd.Flags()
	flags.BoolVarP(&opts.list, "list", "l", false, "list the source's agents without installing")
	flags.StringArrayVarP(&opts.agents, "agent", "a", nil,
		"agent to install (repeatable; '*' for all agents)")
	flags.StringArrayVarP(&opts.harnesses, "harness", "H", nil,
		"harness to install into (repeatable; '*' for all supported harnesses)")
	flags.StringArrayVar(&opts.extras, "extra", nil,
		"harness setting <harness>.<key>=<value> (repeatable), e.g. omp.profile=work")
	flags.BoolVarP(
		&opts.global,
		"global",
		"g",
		false,
		"install into user scope instead of the project",
	)
	flags.BoolVarP(&opts.yes, "yes", "y", false, "apply without prompting")
	flags.BoolVar(&opts.json, "json", false, "print machine-readable JSON; never prompts")
	flags.BoolVar(&opts.all, "all", false, "shorthand for --agent '*' --harness '*' --yes")
	return cmd
}

func (a *App) runAdd(ctx context.Context, raw string, opts *addOptions) error {
	if err := opts.validate(); err != nil {
		return err
	}
	loc, err := source.Parse(raw, a.WorkDir, a.Env)
	if err != nil {
		return err
	}
	done := a.busy(opts.json, "Fetching "+loc.Display())
	snapshot, err := a.Opener.Open(ctx, loc)
	done()
	if err != nil {
		return err
	}
	defer func() { _ = snapshot.Close() }()
	inventory, err := discovery.Discover(snapshot.Tree, a.Registry, discovery.DefaultLimits())
	if err != nil {
		return err
	}
	out := a.printer(opts.json)
	if opts.list {
		return out.inventory(snapshot, inventory)
	}
	// Only a terminal user without --yes/--all/--json is ever asked anything.
	interactive := a.Interactive && !opts.yes && !opts.all && !opts.json
	if interactive {
		// Reject malformed or invalid extras before asking anything; whether their
		// Harness is selected is checked once the selection is known.
		if _, err := harness.ResolveSettings(
			a.Registry, opts.extras, a.supportedHarnesses(),
		); err != nil {
			return err
		}
	}
	defs, err := a.selectAgents(ctx, inventory, opts, interactive)
	if err != nil {
		return err
	}
	harnesses, err := a.selectHarnesses(ctx, opts, interactive)
	if err != nil {
		return err
	}
	settings, err := harness.ResolveSettings(a.Registry, opts.extras, harnesses)
	if err != nil {
		return err
	}
	done = a.busy(opts.json, "Resolving dependencies")
	closure, err := (&resolve.Resolver{
		Opener:   a.Opener,
		Registry: a.Registry,
		Limits:   discovery.DefaultLimits(),
	}).Resolve(ctx, resolve.Root{Snapshot: snapshot, Inventory: inventory}, defs, harnesses)
	done()
	if err != nil {
		return err
	}
	defer func() { _ = closure.Close() }()
	scope := harness.ScopeProject
	if opts.global {
		scope = harness.ScopeGlobal
	}
	planner, err := a.planner(scope)
	if err != nil {
		return err
	}
	plan, err := planner.Build(install.Request{
		Source:    a.lockedSource(snapshot),
		Targets:   a.targets(closure),
		Harnesses: harnesses,
		Settings:  settings,
		Scope:     scope,
		Context:   a.harnessContext(),
	})
	if err != nil {
		return err
	}
	done = func() {}
	if len(closure.Skills) > 0 {
		done = a.busy(opts.json, "Checking required skills")
	}
	plan.Skills, err = a.planSkills(ctx, planner, a.Opener, closure.Skills, harnesses, settings,
		scope)
	done()
	if err != nil {
		return err
	}
	plan.NoteBlockedSkills()
	view := &planView{Plan: plan, Harnesses: a.harnessViews(harnesses, settings)}
	if !interactive {
		return a.applyUnattended(ctx, planner, view, out, opts.yes || opts.all, opts.json)
	}
	return a.applyInteractive(ctx, planner, view, out)
}

// planSkills preflights Skill requirements, opening their sources with opener.
// The user's installer configuration is read (and rejected when invalid) before
// anything runs.
func (a *App) planSkills(
	ctx context.Context,
	planner *install.Planner,
	opener skills.Opener,
	requirements []skills.Requirement,
	harnesses []agent.HarnessID,
	settings map[agent.HarnessID]harness.Settings,
	scope harness.Scope,
) ([]skills.Item, error) {
	if len(requirements) == 0 {
		return nil, nil
	}
	backend, err := skills.LoadInstaller(skills.ConfigPath(a.Env))
	if err != nil {
		return nil, err
	}
	return (&skills.Planner{
		Registry:  a.Registry,
		Opener:    opener,
		Installer: backend,
		Ownership: planner.Ownership,
		Lock:      planner.Lock,
	}).Plan(ctx, skills.Request{
		Requirements: requirements,
		Harnesses:    harnesses,
		Settings:     settings,
		Scope:        scope,
		Context:      a.harnessContext(),
	}), nil
}

// applyUnattended applies only an entirely valid plan: it never prompts, never
// reduces the selection and never approves a blocked item.
func (a *App) applyUnattended(
	ctx context.Context,
	planner *install.Planner,
	view *planView,
	out *printer,
	approved, asJSON bool,
) error {
	if view.Plan.Blocked() {
		out.plan(view, statusRefused)
		return &ExitError{Code: 1}
	}
	if !approved {
		out.plan(view, statusPlanned)
		if asJSON {
			return errors.New("--json never prompts; rerun with --yes to apply this plan")
		}
		return errors.New(
			"confirmation required but input is not interactive; rerun with --yes",
		)
	}
	return a.apply(ctx, planner, view, out)
}

// applyInteractive previews the plan and applies it only after explicit approval.
// A plan with blocking items may be reduced to its valid items, never silently.
func (a *App) applyInteractive(
	ctx context.Context,
	planner *install.Planner,
	view *planView,
	out *printer,
) error {
	blocked := view.Plan.Blocked()
	kept, dropped, droppedSkills := view.Plan.Reduce()
	if blocked && len(kept.Items) == 0 {
		out.plan(view, statusRefused)
		return &ExitError{Code: 1}
	}
	out.plan(view, statusPlanned)
	question, detail := "Apply this plan?", ""
	if blocked {
		lines := make([]string, 0, len(dropped)+len(droppedSkills))
		for _, item := range dropped {
			lines = append(lines, fmt.Sprintf("skip %s for %s: %s", item.Agent, item.Harness,
				item.Reason))
		}
		for _, item := range droppedSkills {
			lines = append(lines, fmt.Sprintf("skip skill %s for %s: %s", item.Skill,
				item.Harness, item.Reason))
		}
		question = fmt.Sprintf(
			"Some items are blocked. Apply only the %d valid item(s) and skip %d?",
			len(kept.Items)+len(kept.Skills), len(dropped)+len(droppedSkills),
		)
		detail = strings.Join(lines, "\n")
		view = &planView{
			Plan: kept, Harnesses: view.Harnesses, Skipped: dropped, SkippedSkills: droppedSkills,
			Report: view.Report,
		}
	}
	confirmed, err := a.Prompter.Confirm(ctx, question, detail)
	if err != nil {
		return err
	}
	if !confirmed {
		return errCancelled
	}
	return a.apply(ctx, planner, view, out)
}

// apply installs and verifies required Skills first; only when every Skill is
// verified are the Agent artifacts written. A backend or verification failure
// writes no Agent file and reports what the backend changed, without rollback.
// After a complete install or update, the artifacts it actually created or
// replaced are reported best-effort; the outcome never depends on that report.
func (a *App) apply(
	ctx context.Context,
	planner *install.Planner,
	view *planView,
	out *printer,
) error {
	if len(view.Plan.Skills) > 0 {
		done := a.busy(out.json, "Installing required skills")
		outcome, err := (&skills.Applier{
			Registry: a.Registry, Context: a.harnessContext(), Scope: view.Plan.Scope,
		}).Apply(ctx, view.Plan.Skills)
		done()
		view.Outcome = outcome
		if err != nil {
			out.plan(view, statusFailed)
			return &ExitError{Code: 1}
		}
		verified := *view.Plan
		verified.Skills = outcome.Items
		view.Plan = &verified
	}
	result, err := planner.Apply(view.Plan, a.WorkDir, a.Now())
	if result != nil {
		applied := *view.Plan
		applied.Items = result.Applied
		out.plan(&planView{
			Plan: &applied, Harnesses: view.Harnesses, Skipped: view.Skipped,
			SkippedSkills: view.SkippedSkills, Outcome: view.Outcome, Report: view.Report,
		}, statusApplied)
	}
	if err == nil && !replays(view) {
		a.Adoption.Report(ctx, placed(result.Applied))
	}
	return err
}

// replays reports a restore: it reproduces locked artifacts and is no new install.
func replays(view *planView) bool {
	return view.Report != nil && view.Report.Command == "restore"
}

// placed lists the artifacts an apply created or replaced; unchanged ones were no install.
func placed(items []install.Item) []adoption.Install {
	var out []adoption.Install
	for _, item := range items {
		if item.Action != install.ActionCreate && item.Action != install.ActionReplace {
			continue
		}
		out = append(out, adoption.Install{
			Source: item.Source, Agent: item.Agent, Origin: item.Origin, Harness: item.Harness,
		})
	}
	return out
}

func (a *App) selectAgents(
	ctx context.Context,
	inventory *discovery.Inventory,
	opts *addOptions,
	interactive bool,
) ([]*agent.Definition, error) {
	var defs []*agent.Definition
	switch {
	case opts.all || slices.Contains(opts.agents, allSelector):
		if problems := agentProblems(inventory); len(problems) > 0 {
			return nil, fmt.Errorf(
				"cannot select every agent; some are ambiguous or invalid:\n  %s",
				strings.Join(problems, "\n  "),
			)
		}
		defs = inventory.Agents
	case len(opts.agents) > 0:
		seen := map[string]bool{}
		for _, id := range opts.agents {
			if seen[id] {
				continue
			}
			seen[id] = true
			def := inventory.Find(id)
			if def == nil {
				return nil, missingAgent(inventory, id)
			}
			defs = append(defs, def)
		}
	case len(inventory.Agents) == 1 && len(agentProblems(inventory)) == 0:
		defs = inventory.Agents
	case interactive && len(inventory.Agents) > 0:
		return a.promptAgents(ctx, inventory)
	default:
		return nil, fmt.Errorf(
			"choose agents with --agent (available: %s) or use --all",
			strings.Join(agentIDs(inventory), ", "),
		)
	}
	if len(defs) == 0 {
		return nil, errors.New("the source contains no installable agents")
	}
	return defs, nil
}

func (a *App) promptAgents(
	ctx context.Context,
	inventory *discovery.Inventory,
) ([]*agent.Definition, error) {
	options := make([]Option, 0, len(inventory.Agents))
	for _, def := range inventory.Agents {
		label := def.ID
		if description := oneLine(def.Description); description != "" {
			label += " — " + description
		}
		options = append(options, Option{
			Value: def.ID, Label: label, Selected: len(inventory.Agents) == 1,
		})
	}
	var description string
	if problems := agentProblems(inventory); len(problems) > 0 {
		description = "Not installable:\n" + strings.Join(problems, "\n")
	}
	ids, err := a.Prompter.MultiSelect(ctx, "Agents to install", description, options)
	if err != nil {
		return nil, err
	}
	defs := make([]*agent.Definition, 0, len(ids))
	for _, id := range ids {
		def := inventory.Find(id)
		if def == nil {
			return nil, missingAgent(inventory, id)
		}
		defs = append(defs, def)
	}
	if len(defs) == 0 {
		return nil, errCancelled
	}
	return defs, nil
}

func agentProblems(inventory *discovery.Inventory) []string {
	var problems []string
	for _, problem := range inventory.Problems {
		if problem.Agent != "" {
			problems = append(problems, problem.String())
		}
	}
	return problems
}

func agentIDs(inventory *discovery.Inventory) []string {
	ids := make([]string, 0, len(inventory.Agents))
	for _, def := range inventory.Agents {
		ids = append(ids, def.ID)
	}
	if len(ids) == 0 {
		return []string{"none"}
	}
	return ids
}

func missingAgent(inventory *discovery.Inventory, id string) error {
	problems := inventory.ProblemsFor(id)
	if len(problems) == 0 {
		return fmt.Errorf(
			"agent %q not found (available: %s)",
			id,
			strings.Join(agentIDs(inventory), ", "),
		)
	}
	lines := make([]string, 0, len(problems))
	for _, problem := range problems {
		lines = append(lines, problem.String())
	}
	return fmt.Errorf("agent %q cannot be installed:\n  %s", id, strings.Join(lines, "\n  "))
}

// supportedHarnesses lists every Harness agnthub can install into, in display order.
func (a *App) supportedHarnesses() []agent.HarnessID {
	installers := a.Registry.Installers()
	ids := make([]agent.HarnessID, 0, len(installers))
	for _, installer := range installers {
		ids = append(ids, installer.ID())
	}
	return ids
}

func (a *App) selectHarnesses(
	ctx context.Context,
	opts *addOptions,
	interactive bool,
) ([]agent.HarnessID, error) {
	switch {
	case opts.all || slices.Contains(opts.harnesses, allSelector):
		return a.supportedHarnesses(), nil
	case len(opts.harnesses) > 0:
		ids := make([]agent.HarnessID, 0, len(opts.harnesses))
		for _, name := range opts.harnesses {
			id, err := agent.ParseHarness(name)
			if err != nil {
				return nil, err
			}
			if !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
		return ids, nil
	case interactive:
		return a.promptHarnesses(ctx)
	default:
		return nil, errors.New(
			"choose at least one --harness (e.g. --harness claude) or use --all",
		)
	}
}

// promptHarnesses offers every supported Harness; detection only preselects.
func (a *App) promptHarnesses(ctx context.Context) ([]agent.HarnessID, error) {
	hctx := a.harnessContext()
	installers := a.Registry.Installers()
	options := make([]Option, 0, len(installers))
	for _, installer := range installers {
		detected := installer.Detect(hctx)
		label := fmt.Sprintf("%-9s %s", installer.ID(), installer.Name())
		if detected {
			label += " (detected)"
		}
		options = append(options, Option{
			Value: string(installer.ID()), Label: label, Selected: detected,
		})
	}
	values, err := a.Prompter.MultiSelect(
		ctx, "Harnesses to install into", "Detected harnesses are preselected.", options,
	)
	if err != nil {
		return nil, err
	}
	ids := make([]agent.HarnessID, 0, len(values))
	for _, value := range values {
		id, err := agent.ParseHarness(value)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil, errCancelled
	}
	return ids, nil
}

// harnessViews reports detection and effective Settings for each selected Harness.
func (a *App) harnessViews(
	ids []agent.HarnessID,
	settings map[agent.HarnessID]harness.Settings,
) []harnessView {
	hctx := a.harnessContext()
	views := make([]harnessView, 0, len(ids))
	for _, id := range ids {
		view := harnessView{ID: id, Name: string(id)}
		if adapter, ok := a.Registry.Get(id); ok {
			view.Name = adapter.Name()
		}
		if installer, err := a.Registry.Installer(id); err == nil {
			view.Detected = installer.Detect(hctx)
			if reporter, ok := installer.(harness.SettingsReporter); ok {
				view.Settings = reporter.EffectiveSettings(hctx, settings[id])
			}
		}
		views = append(views, view)
	}
	return views
}

func (a *App) planner(scope harness.Scope) (*install.Planner, error) {
	lockPath := state.LockPath(a.WorkDir, a.Env.ConfigHome(), scope == harness.ScopeGlobal)
	lock, err := state.LoadLock(lockPath)
	if err != nil {
		return nil, err
	}
	ownership, err := state.LoadOwnership(state.OwnershipPath(a.Env.StateHome()))
	if err != nil {
		return nil, err
	}
	return &install.Planner{
		Registry:  a.Registry,
		Lock:      lock,
		LockPath:  lockPath,
		Ownership: ownership,
	}, nil
}

// lockedSource records a credential-free locator; local paths are project-relative
// when possible so the lock stays shareable.
func (a *App) lockedSource(snapshot *source.Snapshot) state.LockedSource {
	loc := snapshot.Locator
	if loc.Kind != source.KindLocal {
		return skills.LockedSource(loc, snapshot.Revision)
	}
	locked := state.LockedSource{
		Kind: string(loc.Kind), Locator: loc.Path, Ref: loc.Ref, Revision: snapshot.Revision,
	}
	rel, err := filepath.Rel(a.WorkDir, loc.Path)
	switch {
	case err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)):
	case rel == ".":
		locked.Locator = "."
	default:
		locked.Locator = "./" + filepath.ToSlash(rel)
	}
	return locked
}

// targets turns the resolved closure into plan targets; companions keep their own
// source but share the user's Harnesses, scope and Settings. Each Target carries
// what it requires per Harness so the lock can tell later whether a Dependency is
// still needed.
func (a *App) targets(closure *resolve.Closure) []install.Target {
	keys := make(map[string]string, len(closure.Members))
	sources := make([]state.LockedSource, len(closure.Members))
	for i, member := range closure.Members {
		sources[i] = a.lockedSource(member.Snapshot)
		keys[member.Label] = state.LockedAgent{
			Source: sources[i], Agent: member.Definition.ID,
		}.Key()
	}
	requires := map[string]map[agent.HarnessID][]string{}
	add := func(label string, harnessID agent.HarnessID, entry string) {
		byHarness := requires[label]
		if byHarness == nil {
			byHarness = map[agent.HarnessID][]string{}
			requires[label] = byHarness
		}
		if !slices.Contains(byHarness[harnessID], entry) {
			byHarness[harnessID] = append(byHarness[harnessID], entry)
		}
	}
	for _, member := range closure.Members {
		for harnessID, labels := range member.Requires {
			for _, label := range labels {
				add(member.Label, harnessID, state.RequireAgent(keys[label]))
			}
		}
	}
	for _, requirement := range closure.Skills {
		for _, need := range requirement.Needs {
			add(need.RequiredBy, need.Harness, state.RequireSkill(requirement.Skill))
		}
	}
	targets := make([]install.Target, 0, len(closure.Members))
	for i, member := range closure.Members {
		target := install.Target{
			Definition: member.Definition,
			Origin:     state.OriginDependency,
			Source:     sources[i],
			Harnesses:  member.Harnesses,
			RequiredBy: member.RequiredBy,
			Requires:   requires[member.Label],
		}
		if target.Requires == nil {
			target.Requires = map[agent.HarnessID][]string{}
		}
		if member.Direct {
			target.Origin = state.OriginDirect
		}
		targets = append(targets, target)
	}
	return targets
}
