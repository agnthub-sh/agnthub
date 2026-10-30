package cli

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/heynemann/agnthub/harness"
	"github.com/heynemann/agnthub/install"
	"github.com/heynemann/agnthub/state"
)

// statusUpToDate reports an update or restore with nothing to write.
const statusUpToDate = "up-to-date"

type lifecycleOptions struct {
	global bool
	yes    bool
	json   bool
	extras []string
}

func (o *lifecycleOptions) flags(cmd *cobra.Command, mutating bool) {
	flags := cmd.Flags()
	flags.BoolVarP(
		&o.global,
		"global",
		"g",
		false,
		"use the user-scope lock instead of the project's",
	)
	flags.BoolVar(&o.json, "json", false, "print machine-readable JSON; never prompts")
	if !mutating {
		return
	}
	flags.BoolVarP(&o.yes, "yes", "y", false, "apply without prompting")
	flags.StringArrayVar(&o.extras, "extra", nil,
		"binding <harness>.<key>=<value> (repeatable) when the recorded one is ambiguous or absent")
}

func (a *App) checkCommand(ctx context.Context) *cobra.Command {
	opts := &lifecycleOptions{}
	cmd := &cobra.Command{
		Use:   "check [agent...]",
		Short: "Show available source changes and local drift without installing",
		Long: `Resolve each locked agent's recorded source ref to its current revision and
compare the content agnthub would install now with the lock, per harness. Reports
updates (content, variant, converter or dependency changes), local drift (modified
or missing files), unavailable sources and the recorded harness bindings. Nothing is
written.`,
		Example: `  agnthub check
  agnthub check reviewer --json
  agnthub check --global`,
		RunE: func(_ *cobra.Command, args []string) error {
			return a.runCheck(ctx, args, opts)
		},
	}
	opts.flags(cmd, false)
	return cmd
}

func (a *App) updateCommand(ctx context.Context) *cobra.Command {
	opts := &lifecycleOptions{}
	cmd := &cobra.Command{
		Use:   "update [agent...]",
		Short: "Preview and apply newer content for installed agents",
		Long: `Resolve each selected direct agent (default: all) at the current revision of its
recorded ref, with its current dependencies, and preview content, variant,
dependency and skill changes before writing. Locally modified or unmanaged files
are never overwritten. Each artifact keeps its recorded harness binding (e.g. its
omp profile); an ambiguous or missing binding needs --extra or a prompt.`,
		Example: `  agnthub update
  agnthub update reviewer --yes
  agnthub update --global --extra omp.profile=work`,
		RunE: func(_ *cobra.Command, args []string) error {
			return a.runUpdate(ctx, args, opts)
		},
	}
	opts.flags(cmd, true)
	return cmd
}

func (a *App) restoreCommand(ctx context.Context) *cobra.Command {
	opts := &lifecycleOptions{}
	cmd := &cobra.Command{
		Use:   "restore",
		Short: "Recreate installed agents exactly as the lock records them",
		Long: `Fetch every locked agent at its locked revision, verify the source bytes and
regenerate each artifact with the recorded converter, and write only when every
artifact and skill matches the locked digests. Unavailable revisions, changed
bytes or unsupported converters refuse the restore; fresh content is never
substituted. On a machine without recorded bindings, harnesses with settings need
--extra (e.g. --extra omp.profile=default) or a prompt.`,
		Example: `  agnthub restore
  agnthub restore --yes --extra omp.profile=work`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return a.runRestore(ctx, opts)
		},
	}
	opts.flags(cmd, true)
	return cmd
}

func (a *App) runCheck(ctx context.Context, ids []string, opts *lifecycleOptions) error {
	l, err := a.newLifecycle(opts.global, false)
	if err != nil {
		return err
	}
	defer func() { _ = l.Close() }()
	include, err := selectLocked(l.planner.Lock, ids, false)
	if err != nil {
		return err
	}
	contexts, err := a.bindContexts(
		ctx, l.scope, l.planner.Lock, l.planner.Ownership, include, bindingPolicy{},
	)
	if err != nil {
		return err
	}
	lp, err := l.planLatest(ctx, contexts, false)
	if err != nil {
		return err
	}
	report := l.buildReport("check", lp, include, len(ids) == 0)
	out := a.printer(opts.json)
	if opts.json {
		return out.emitJSON(report)
	}
	fmt.Fprintf(out.w, "%s · %s scope · lock %s\n", out.styles.title.Render("Check"), l.scope,
		report.Lock)
	out.humanReport(report)
	return nil
}

func (a *App) runUpdate(ctx context.Context, ids []string, opts *lifecycleOptions) error {
	l, err := a.newLifecycle(opts.global, false)
	if err != nil {
		return err
	}
	defer func() { _ = l.Close() }()
	lock := l.planner.Lock
	include, err := selectLocked(lock, ids, true)
	if err != nil {
		return err
	}
	interactive := a.Interactive && !opts.yes && !opts.json
	contexts, err := a.lifecycleContexts(ctx, l, include, opts, bindingPolicy{
		mutating: true, interactive: interactive,
	})
	if err != nil {
		return err
	}
	lp, err := l.planLatest(ctx, contexts, true)
	if err != nil {
		return err
	}
	if err := unavailableError(lp); err != nil {
		return err
	}
	if err := retargets(contexts, lp.plan); err != nil {
		return err
	}
	report := l.buildReport("update", lp, include, len(ids) == 0)
	view := &planView{Plan: lp.plan, Harnesses: lp.views, Report: report}
	out := a.printer(opts.json)
	if !report.pending(lp.plan) {
		out.plan(view, statusUpToDate)
		return nil
	}
	if !interactive {
		return a.applyUnattended(ctx, l.planner, view, out, opts.yes, opts.json)
	}
	return a.applyInteractive(ctx, l.planner, view, out)
}

func (a *App) runRestore(ctx context.Context, opts *lifecycleOptions) error {
	l, err := a.newLifecycle(opts.global, true)
	if err != nil {
		return err
	}
	defer func() { _ = l.Close() }()
	include := func(*state.LockedAgent) bool { return true }
	interactive := a.Interactive && !opts.yes && !opts.json
	contexts, err := a.lifecycleContexts(ctx, l, include, opts, bindingPolicy{
		mutating: true, interactive: interactive, fresh: true,
	})
	if err != nil {
		return err
	}
	lp, err := l.planFrozen(ctx, contexts)
	if err != nil {
		return err
	}
	if problems := l.verifyFrozen(contexts, lp); len(problems) > 0 {
		return fmt.Errorf(
			"restore refused before writing; the locked installation cannot be reproduced "+
				"exactly:\n  - %s",
			strings.Join(problems, "\n  - "),
		)
	}
	report := l.buildReport("restore", lp, include, true)
	view := &planView{Plan: lp.plan, Harnesses: lp.views, Report: report}
	out := a.printer(opts.json)
	if !report.pending(lp.plan) {
		out.plan(view, statusUpToDate)
		return nil
	}
	if !interactive {
		return a.applyUnattended(ctx, l.planner, view, out, opts.yes, opts.json)
	}
	out.plan(view, statusPlanned)
	confirmed, err := a.Prompter.Confirm(ctx, "Restore these files?", "")
	if err != nil {
		return err
	}
	if !confirmed {
		return errCancelled
	}
	return a.apply(ctx, l.planner, view, out)
}

// lifecycleContexts validates --extra against the locked Harnesses and resolves
// every included artifact's binding.
func (a *App) lifecycleContexts(
	ctx context.Context,
	l *lifecycle,
	include func(*state.LockedAgent) bool,
	opts *lifecycleOptions,
	policy bindingPolicy,
) ([]*bindingContext, error) {
	explicit, err := harness.ResolveSettings(
		a.Registry, opts.extras, lockedHarnesses(l.planner.Lock, include),
	)
	if err != nil {
		return nil, err
	}
	policy.explicit = explicit
	return a.bindContexts(ctx, l.scope, l.planner.Lock, l.planner.Ownership, include, policy)
}

// unavailableError refuses an update when a selected Agent cannot be resolved.
func unavailableError(lp *lifecyclePlan) error {
	if len(lp.unavailable) == 0 {
		return nil
	}
	lines := make([]string, 0, len(lp.unavailable))
	for key, err := range lp.unavailable {
		lines = append(lines, key+": "+err.Error())
	}
	slices.Sort(lines)
	return fmt.Errorf("cannot update; nothing was written:\n  - %s", strings.Join(lines, "\n  - "))
}

// retargets refuses a planned item that would place a recorded installation
// somewhere else, e.g. into a profile that became active after it was installed.
func retargets(contexts []*bindingContext, plan *install.Plan) error {
	var problems []string
	for _, item := range plan.Items {
		if item.Path == "" {
			continue
		}
		var recorded []string
		for _, c := range contexts {
			for _, bound := range c.artifacts {
				if bound.agent.Agent == item.Agent &&
					bound.artifact.Harness == string(item.Harness) &&
					bound.agent.Source.Locator == item.Source.Locator {
					recorded = append(recorded, bound.path)
				}
			}
		}
		if len(recorded) > 0 && !slices.Contains(recorded, item.Path) {
			problems = append(problems, fmt.Sprintf(
				"%s for %s now resolves to %s but is recorded at %s",
				item.Agent, item.Harness, item.Path, strings.Join(recorded, ", "),
			))
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return errors.New(
		"update never retargets an installation; pass the recorded binding with --extra:\n  - " +
			strings.Join(problems, "\n  - "),
	)
}

// verifyFrozen checks the regenerated plan against the lock: identical source
// bytes, a supported converter and identical artifact and Skill digests. Shared
// artifacts bound to another binding are never replaced on this binding's behalf.
func (l *lifecycle) verifyFrozen(contexts []*bindingContext, lp *lifecyclePlan) []string {
	var problems []string
	planned := map[*state.LockedArtifact]bool{}
	for i := range lp.plan.Items {
		item := &lp.plan.Items[i]
		if item.Action.Blocking() {
			problems = append(problems, fmt.Sprintf("%s for %s: %s", item.Agent, item.Harness,
				item.Reason))
			continue
		}
		bound, ok := lockedFor(contexts, item, l.scope == harness.ScopeGlobal)
		if !ok {
			problems = append(problems, fmt.Sprintf(
				"%s for %s regenerates %s, which the lock does not record",
				item.Agent, item.Harness, item.Path,
			))
			continue
		}
		planned[bound.artifact] = true
		if problem := frozenMismatch(item, bound.artifact); problem != "" {
			problems = append(problems, problem)
			continue
		}
		if item.Action == install.ActionReplace {
			for _, record := range l.planner.Ownership.ForPath(item.Path) {
				if record.Harness == string(item.Harness) &&
					state.SettingsKey(record.Settings) != state.SettingsKey(item.Binding) {
					problems = append(problems, fmt.Sprintf(
						"%s is shared with the %s binding %s; restore it with that binding",
						item.Path, item.Harness, formatSettings(record.Settings),
					))
				}
			}
		}
	}
	for _, c := range contexts {
		for _, bound := range c.artifacts {
			if !planned[bound.artifact] {
				problems = append(problems, fmt.Sprintf(
					"%s for %s (%s) could not be regenerated from %s",
					bound.agent.Agent, bound.artifact.Harness, bound.path,
					sourceLabel(sourceJSON(bound.agent.Source)),
				))
			}
		}
	}
	for _, item := range lp.plan.Skills {
		if item.Blocking() {
			problems = append(problems, fmt.Sprintf("skill %s for %s: %s", item.Skill,
				item.Harness, item.Reason))
			continue
		}
		i := slices.IndexFunc(l.planner.Lock.Skills, func(locked state.LockedSkill) bool {
			return locked.Skill == item.Skill &&
				slices.Contains(locked.Harnesses, string(item.Harness))
		})
		switch {
		case i < 0:
			problems = append(problems, fmt.Sprintf(
				"skill %s for %s is required but not locked; run `agnthub update`",
				item.Skill, item.Harness,
			))
		case l.planner.Lock.Skills[i].Digest != item.Digest:
			problems = append(problems, fmt.Sprintf(
				"skill %s for %s: %s (%s) has digest %s, the lock records %s",
				item.Skill, item.Harness, item.Path, item.Decision, item.Digest,
				l.planner.Lock.Skills[i].Digest,
			))
		}
	}
	return problems
}

// lockedFor finds the locked artifact an item regenerates: same Agent, source,
// Harness and path. In global scope an explicitly bound fresh destination may
// differ in path from the recorded one.
func lockedFor(contexts []*bindingContext, item *install.Item, global bool) (boundArtifact, bool) {
	var fallback *boundArtifact
	for _, c := range contexts {
		for i := range c.artifacts {
			bound := &c.artifacts[i]
			if bound.agent.Agent != item.Agent || bound.artifact.Harness != string(item.Harness) ||
				bound.agent.Source.Kind != item.Source.Kind ||
				bound.agent.Source.Locator != item.Source.Locator {
				continue
			}
			if bound.path == item.Path {
				return *bound, true
			}
			if global && c.origin == bindingExplicit && fallback == nil {
				fallback = bound
			}
		}
	}
	if fallback != nil {
		return *fallback, true
	}
	return boundArtifact{}, false
}

// frozenMismatch explains why a regenerated item is not the locked artifact.
func frozenMismatch(item *install.Item, locked *state.LockedArtifact) string {
	switch {
	case item.SourcePath != locked.SourcePath || item.SourceDigest != locked.SourceDigest:
		return fmt.Sprintf(
			"%s for %s: source %s (%s) differs from the locked %s (%s)",
			item.Agent, item.Harness, item.SourcePath, item.SourceDigest,
			locked.SourcePath, locked.SourceDigest,
		)
	case locked.Variant == state.VariantGenerated &&
		!harness.ConverterReproduces(locked.Converter, item.Artifact.Converter):
		return fmt.Sprintf(
			"%s for %s was generated by converter %q, which this agnthub does not provide "+
				"(it provides %q); restore with an agnthub release that supports %q, or run "+
				"`agnthub update` to regenerate",
			item.Agent, item.Harness, locked.Converter, item.Artifact.Converter, locked.Converter,
		)
	case locked.Variant == state.VariantGenerated && item.Artifact.Converter != locked.Converter &&
		state.Digest(item.Artifact.Content) != locked.Digest:
		return fmt.Sprintf(
			"%s for %s was generated by converter %q; this agnthub's %q regenerates different "+
				"content (%s, locked %s); restore with an agnthub release that provides %q, or "+
				"run `agnthub update` to regenerate",
			item.Agent, item.Harness, locked.Converter, item.Artifact.Converter,
			state.Digest(item.Artifact.Content), locked.Digest, locked.Converter,
		)
	case state.Digest(item.Artifact.Content) != locked.Digest:
		return fmt.Sprintf(
			"%s for %s: regenerated content %s does not match the locked digest %s",
			item.Agent, item.Harness, state.Digest(item.Artifact.Content), locked.Digest,
		)
	}
	return ""
}
