package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/heynemann/agnthub/internal/agent"
	"github.com/heynemann/agnthub/internal/discovery"
	"github.com/heynemann/agnthub/internal/harness"
	"github.com/heynemann/agnthub/internal/install"
	"github.com/heynemann/agnthub/internal/source"
	"github.com/heynemann/agnthub/internal/state"
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

func (a *App) addCommand(ctx context.Context) *cobra.Command {
	opts := &addOptions{}
	cmd := &cobra.Command{
		Use:   "add <source>",
		Short: "Install agents from a source repository or local directory",
		Long: `Install agents from a GitHub repository (owner/repo[#ref]), a Git URL or an
explicit local directory (./path). Harness settings use --extra <harness>.<key>=<value>.`,
		Example: `  agnthub add ./my-agents --list
  agnthub add ./my-agents --agent reviewer --harness claude
  agnthub add owner/repo --harness omp --global --extra omp.profile=work`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return a.runAdd(ctx, args[0], opts)
		},
	}
	flags := cmd.Flags()
	flags.BoolVar(&opts.list, "list", false, "list the source's agents without installing")
	flags.StringArrayVarP(&opts.agents, "agent", "a", nil, "agent to install (repeatable)")
	flags.StringArrayVarP(&opts.harnesses, "harness", "H", nil, "target harness (repeatable)")
	flags.StringArrayVar(&opts.extras, "extra", nil,
		"harness setting <harness>.<key>=<value> (repeatable), e.g. omp.profile=work")
	flags.BoolVarP(
		&opts.global,
		"global",
		"g",
		false,
		"install into user scope instead of the project",
	)
	flags.BoolVarP(&opts.yes, "yes", "y", false, "apply without confirmation")
	flags.BoolVar(&opts.json, "json", false, "print machine-readable JSON")
	flags.BoolVar(&opts.all, "all", false,
		"select every agent and every supported harness and skip confirmation")
	return cmd
}

func (a *App) runAdd(ctx context.Context, raw string, opts *addOptions) error {
	loc, err := source.Parse(raw, a.WorkDir, a.Env)
	if err != nil {
		return err
	}
	snapshot, err := a.Opener.Open(ctx, loc)
	if err != nil {
		return err
	}
	defer func() { _ = snapshot.Close() }()
	inventory, err := discovery.Discover(snapshot.Tree, a.Registry, discovery.DefaultLimits())
	if err != nil {
		return err
	}
	out := newPrinter(a.Stdout, opts.json)
	if opts.list {
		return out.inventory(snapshot, inventory)
	}
	targets, err := selectAgents(inventory, opts)
	if err != nil {
		return err
	}
	harnesses, err := a.selectHarnesses(opts)
	if err != nil {
		return err
	}
	settings, err := harness.ResolveSettings(a.Registry, opts.extras, harnesses)
	if err != nil {
		return err
	}
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
		Targets:   targets,
		Harnesses: harnesses,
		Settings:  settings,
		Scope:     scope,
		Context:   a.harnessContext(),
	})
	if err != nil {
		return err
	}
	if err := plan.Validate(); err != nil {
		out.plan(plan, "refused")
		return &ExitError{Code: 1}
	}
	if !opts.yes && !opts.all {
		if !opts.json {
			out.plan(plan, "planned")
		}
		confirmed, err := a.confirm("Apply this plan?")
		if err != nil {
			return err
		}
		if !confirmed {
			return errors.New("cancelled; nothing was written")
		}
	}
	result, err := planner.Apply(plan, a.WorkDir, a.Now())
	if result != nil {
		applied := &install.Plan{
			Scope: plan.Scope, Source: plan.Source, LockPath: plan.LockPath, Items: result.Applied,
		}
		out.plan(applied, "applied")
	}
	return err
}

func selectAgents(inventory *discovery.Inventory, opts *addOptions) ([]install.Target, error) {
	var defs []*agent.Definition
	switch {
	case opts.all:
		if problems := agentProblems(inventory); len(problems) > 0 {
			return nil, fmt.Errorf(
				"--all cannot select an ambiguous or invalid agent:\n  %s",
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
	default:
		return nil, fmt.Errorf(
			"choose agents with --agent (available: %s) or use --all",
			strings.Join(agentIDs(inventory), ", "),
		)
	}
	if len(defs) == 0 {
		return nil, errors.New("the source contains no installable agents")
	}
	targets := make([]install.Target, 0, len(defs))
	for _, def := range defs {
		targets = append(targets, install.Target{Definition: def, Origin: state.OriginDirect})
	}
	return targets, nil
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

func (a *App) selectHarnesses(opts *addOptions) ([]agent.HarnessID, error) {
	if opts.all {
		installers := a.Registry.Installers()
		ids := make([]agent.HarnessID, 0, len(installers))
		for _, installer := range installers {
			ids = append(ids, installer.ID())
		}
		return ids, nil
	}
	if len(opts.harnesses) == 0 {
		return nil, errors.New("choose at least one --harness (e.g. --harness claude) or use --all")
	}
	var ids []agent.HarnessID
	seen := map[agent.HarnessID]bool{}
	for _, name := range opts.harnesses {
		id, err := agent.ParseHarness(name)
		if err != nil {
			return nil, err
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids, nil
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
	locked := state.LockedSource{Kind: string(loc.Kind), Ref: loc.Ref, Revision: snapshot.Revision}
	switch loc.Kind {
	case source.KindLocal:
		locked.Locator = loc.Path
		rel, err := filepath.Rel(a.WorkDir, loc.Path)
		switch {
		case err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)):
		case rel == ".":
			locked.Locator = "."
		default:
			locked.Locator = "./" + filepath.ToSlash(rel)
		}
	case source.KindGitHub:
		locked.Locator = loc.Owner + "/" + loc.Repo
	default:
		locked.Locator = loc.Host + "/" + loc.RepoPath
	}
	return locked
}

func (a *App) confirm(question string) (bool, error) {
	if !a.Interactive {
		return false, errors.New(
			"confirmation required but input is not interactive; rerun with --yes",
		)
	}
	fmt.Fprintf(a.Stdout, "%s [y/N] ", question)
	line, err := bufio.NewReader(a.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return false, errors.New("no confirmation received; nothing was written")
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}
